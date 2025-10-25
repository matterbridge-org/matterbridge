#! /usr/bin/env bash

# Don't stop on first error because we still want to terminate background PIDs
set -ux

# ./test.sh PROTOCOL SCENARIO [TIMEOUT]
PROTOCOL="$1"
SCENARIO="$2"
TIMEOUT="${3:-20}"

# Setup steps are performed in this order:
#
# - setup.sh comes online (mocking protocol server)
# - matterbridge comes online (connecting to the protocol server)
# - API client comes online (connecting to matterbridge)
# - $PROTOCOL client comes online (connecting to protocol server)
# - test.sh sends SIGUSR1 to API client to notify that $PROTOCOL client is ready
#   so that it can start performing the test (read-only tests are not affected
#   by this signal)
# 
# What counts in the timeouts:
#
# - no timeout: compilation time for matterbridge, api client, and $PROTOCOL client
# - STARTUP_TIMEOUT: time for setup.sh to come online ; time for matterbridge to come online
# - TIMEOUT: time for api/$PROTOCOL client to become "READY" then perform the test

# The number of second to wait for matterbridge and setup.sh each
# to be done with startup. Otherwise, tests are aborted.
STARTUP_TIMEOUT=30
# Time allocated for the various clients to each finish shutting down.
TEARDOWN_TIMEOUT=30

################################################################################

# We start matterbridge and wait for the "Now relaying messages" output
start_matterbridge() {
  echo "[$PROTOCOL] Starting matterbridge"
  # Building the matterbridge binary in the foreground so build time doesn't count in the timeout
  if go build .; then
    # Start matterbridge instance in background
    ./matterbridge -conf tests/$PROTOCOL/matterbridge.toml -debug 2>&1 >tests/$PROTOCOL/matterbridge.log &
    MATTERBRIDGE_PID=$!
    echo "MATTERBRIDGE_PID=$MATTERBRIDGE_PID"
  
    # Wait some magic seconds
    for i in $(seq 1 $STARTUP_TIMEOUT); do
      if grep "Now relaying messages" tests/$PROTOCOL/matterbridge.log  2>&1 >/dev/null; then
        echo "[$PROTOCOL] Started matterbridge successfully"
        return
      fi

      sleep 1
    done
  fi

  echo "[$PROTOCOL] Failed to start matterbridge"
  return 1
}

# We start the additional setup.sh steps if they exist
# If setup_status.sh exists, its exit code is determines whether the setup is finished
# Otherwise, setup.sh is assumed to be instantly successful
start_setup() {
  echo "[$PROTOCOL] Starting setup.sh"
  if [ -f tests/$PROTOCOL/setup.sh ]; then
    ./tests/$PROTOCOL/setup.sh 2>&1 >tests/$PROTOCOL/setup.log &
    SETUP_PID=$!
  fi

  if [ ! -f tests/$PROTOCOL/setup_status.sh ]; then
    # Setup assumed successful because we have no way to test
    return
  fi

  # Wait some magic seconds
  for i in $(seq 1 $STARTUP_TIMEOUT); do
    if tests/$PROTOCOL/setup_status.sh; then
      echo "[$PROTOCOL] Started setup.sh successfully"
      return
    fi
    sleep 1
  done

  echo "[$PROTOCOL] Failed to start setup.sh"
  return 1
}

wait_kill() {
  if ps -p $1; then
    kill $1
  fi
  for i in $(seq 1 $TEARDOWN_TIMEOUT); do
    if ! ps -p $1 > /dev/null; then
      return
    fi
  done

  # Timeout. Start KILLing things for real.
  echo "[$PROTOCOL] wait_kill timeout. Now sending SIGKILL."
  kill -9 $1
}

teardown() {
  if [[ "${MATTERBRIDGE_PID:-FOO}" != "FOO" ]]; then
    echo "[$PROTOCOL] Shutting down matterbridge…"
    wait_kill $MATTERBRIDGE_PID
  fi
  if [ -f tests/$PROTOCOL/setup.sh ] && [[ "${SETUP_PID:-FOO}" != "FOO" ]]; then
    echo "[$PROTOCOL] Shutting down setup.sh…"
    wait_kill $SETUP_PID
  fi
  if [[ "${PROTOCOL_PID:-FOO}" != "FOO" ]]; then
    echo "[$PROTOCOL] Shutting down $PROTOCOL client…"
    wait_kill $PROTOCOL_PID
  fi
  if [[ "${API_PID:-FOO}" != "FOO" ]]; then
    echo "[$PROTOCOL] Shutting down API client…"
    wait_kill $API_PID
  fi
}

go_build() {
  echo "[$PROTOCOL] Building $1 client"
  cd $1
  if ! go build .; then
    echo "[$PROTOCOL] Building $1 failed… Aborting tests."
    teardown
    exit 1
  fi
  cd ../..
}

echo "Current working dir: $(pwd)"

# Wait for setup.sh/matterbridge to start and populate the SETUP_PID/MATTERBRIDGE_PID variables
if ! start_setup || ! start_matterbridge; then
  echo "[$PROTOCOL] Failed test setup steps… Aborting."
  teardown
  exit 1
fi

echo "[$PROTOCOL] Successful setup steps… Continuing."
# Building the binaries does not count in the timeout
go_build tests/$PROTOCOL
go_build tests/api
echo "[$PROTOCOL] Successful client builds… Continuing."

# Start PROTOCOL client in background
cd tests/$PROTOCOL
timeout -s KILL $TIMEOUT ./$PROTOCOL $SCENARIO $TIMEOUT &>$PROTOCOL.log &
PROTOCOL_PID=$!
# Wait for protocol client to be done with setup
PROTOCOL_READY=0
for i in 1 2 3; do
  if grep "Running scenario $SCENARIO" $PROTOCOL.log 2>&1 >/dev/null; then
    echo "[$PROTOCOL] Protocol client ready to start test."
    PROTOCOL_READY=1
    break
  fi
  sleep 1
done
cd ../..

if [[ $PROTOCOL_READY == 0 ]]; then
  echo "[$PROTOCOL] Protocol client never reported ready to start test."
  teardown
  exit 1
fi


# Start API client in background
cd tests/api
timeout -s KILL $TIMEOUT ./api $SCENARIO $TIMEOUT &>../$PROTOCOL/api.log &
API_PID=$!
cd ../..

STATUS=0

# Now wait for tests to finish or timeout
if ! wait $API_PID; then
  echo "[$PROTOCOL] API client failed. See tests/$PROTOCOL/api.log"
  STATUS=1
fi

if ! wait $PROTOCOL_PID; then
  echo "[$PROTOCOL] PROTOCOL client failed. See tests/$PROTOCOL/$PROTOCOL.log"
  STATUS=1
fi

echo "[$PROTOCOL] Reported test status: $STATUS. Now performing teardown."
teardown

if [[ $STATUS == 0 ]]; then
  echo "OK $SCENARIO"
else
  echo "FAIL $SCENARIO"
fi

exit $STATUS
