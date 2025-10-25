#! /usr/bin/env bash

BASEDIR="$(dirname "$0")"

prosody --config "$BASEDIR"/prosody.cfg.lua -F
