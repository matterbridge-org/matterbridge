# Integration tests

This directory is for integration tests only. Unit tests reside in their
respective modules.

## Motivation

Unit testing only checks that assumptions within the code are held. Integration
tests can provide real-life testing capabilities to ensure that a feature is
implemented properly, even when going through the remote chat service.

## Methodology

Each matterbrige supported protocol has its own corresponding folder in this
`tests` folder. Each protocol's integration's tests:

- are run in a specific CI job with a specific environment
- are documented to be run locally with a specific environment
- provides implementation for incoming tests, and outgoing tests to avoid
  feature mismatch (for example, a bridge that could receive attachments
  from other networks but not the other way around)
- are run sequentially, although different protocols are tested in parallel

> [!NOTE]
> `incoming` refers to a message received on that protocol (to be delivered
> to others via matterbridge), while `outgoing` refers to a message received
> from another matterbridge protocol to be delivered to this protocol.

## Architecture

At the moment, integration tests require 3 components to play together:

- a real matterbridge instance configured with a protocol account (`pa`) to a protocol room (`pr`)
- another protocol client (`pc`), connected to the same protocol room (`pr`), following a test scenario
- a matterbridge API client (`ac`), connected client following the same test scenario (called `ac`)

> [!NOTE]
> For each tested protocol, the test suite requires two accounts on the remote server.

The `matterbridge.toml` in each integration test folder determines the config
used for the matterbridge daemon used in the tests.

The API client is located in `tests/api/api.go` and accepts as arguments:

- one of the supported tests (eg. `incoming-message`)
- a timeout in seconds, after which it will automatically fail (default: 5)

The protocol client is located in `tests/PROTOCOL/PROTOCOL.go`, is a proper
go module that can be run with `go run .`, and accepts as arguments:

- one of the supported tests (eg. `incoming-message`)
- a timeout in seconds, after which it will automatically fail (default: 5)

Both binaries:

- run in the background, with the same set of CLI arguments
- fail the entire pipeline if they individually fail
- success as soon as the testing criteria is met

For example, in the case of the `incoming-message` test, the protocol client
will successfully exit as soon as the message is sent without errors,
but the API client will wait and either:

- successfully exit when receiving the `test-incoming-message`
- fail after the timeout was reached

## Supported tests

The following test scenarios are supported at the moment:

- `incoming-message` steps:
  - `pc` sends `test-incoming-message` in `pr` using the native protocol
  - `ac` receives `<pc> test-incoming-message` in `pr` using the matterbridge API
- `outgoing-message` steps:
  - `ac` sends `test-outgoing-message` in `pr` using the matterbridge API
  - `pc` receives `<ac> test-outgoing-message` in `pr` using the native protocol
