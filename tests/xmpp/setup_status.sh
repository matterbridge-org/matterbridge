#! /usr/bin/env bash

set -eu

BASEDIR="$(dirname "$0")"
grep "Startup complete" "$BASEDIR"/setup.log 2>&1>/dev/null