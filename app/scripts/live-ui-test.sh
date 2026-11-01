#!/bin/bash
# live-ui-test.sh runs PaneFlowUITests on one simulator against a real
# `initech app-serve`, as one step under super/tools/heavy.sh; the test taps,
# this script raises the prompts and checks what each key did in the rig.
#
#   app/scripts/live-ui-test.sh <iphone|ipad> <rig dir> <paneA> <paneB> <link file> <out dir>
#
# Like sim-test.sh it makes a simulator for this run alone and shuts it down
# and deletes it however the run ends; a new crash report from it fails the
# run with exit 9 (stop and tell super).
#
# <link file> holds the pairing link `initech app-serve token` printed (mode
# 0600); it reaches the test through the environment, never a command line.
# paneA runs Claude on Haiku at the default permission mode, in a rig set up
# by the fleet's scratch-HOME recipe (fleet/reference.md), never a HOME
# without the keychain link. INITECH_BIN names the initech to send with
# (default: initech). Bounded at 18 minutes.
set -u
kind=$1 rig=$2 a=$3 b=$4 linkfile=$5 out=$6
case "$kind" in
  iphone) type="iPhone 17 Pro" ;;
  ipad) type="iPad Pro 11-inch (M5)" ;;
  *) echo "live-ui-test: kind is iphone or ipad" >&2; exit 2 ;;
esac
here=$(cd "$(dirname "$0")/.." && pwd)
heavy=${HEAVY:-$HOME/Desktop/Projects/initech/super/tools/heavy.sh}
bin=${INITECH_BIN:-initech}
[ -s "$linkfile" ] || { echo "no pairing link in $linkfile" >&2; exit 2; }
mkdir -p "$out"; rm -f "$out"/prompt-*

export TEST_RUNNER_LIVE_PAIRING; TEST_RUNNER_LIVE_PAIRING=$(cat "$linkfile")
export TEST_RUNNER_LIVE_PANES="$a,$b" TEST_RUNNER_SHOT_DIR="$out"

# raise asks paneA for a write that its permission prompt must ask about.
raise() {
  (cd "$rig" && env -u INITECH_SOCKET -u INITECH_AGENT -u TMUX "$bin" send "$a" \
    "Use the Write tool to create the file app-ui-$1.txt in this folder containing the word hello. Do nothing else.")
}

# wait_for waits for a marker while the test still runs.
wait_for() { for _ in $(seq 1 600); do [ -e "$1" ] && return 0; kill -0 "$2" 2>/dev/null || return 1; sleep 1; done; return 1; }

step() {
  cd "$here" || exit 2
  mark=$(mktemp -t live-ui-mark) || exit 2
  name="${INITECH_AGENT:-live}-$kind-$(date +%H%M%S)"
  id=$(xcrun simctl create "$name" "$type" "${SIM_RUNTIME:-com.apple.CoreSimulator.SimRuntime.iOS-27-0}") || exit 2
  echo "live-ui-test: created $name $id"
  xb=""
  cleanup() {
    if [ -n "$xb" ] && kill -0 "$xb" 2>/dev/null; then
      kill -TERM "$xb" 2>/dev/null
      for _ in $(seq 1 30); do kill -0 "$xb" 2>/dev/null || break; sleep 1; done
    fi
    xcrun simctl shutdown "$id" >/dev/null 2>&1
    xcrun simctl delete "$id" >/dev/null 2>&1 && echo "live-ui-test: deleted $name"
    sleep 10 # crash reports land a few seconds after a process dies
    crashed=$(find "$HOME/Library/Logs/DiagnosticReports" -newer "$mark" -name '*.ips' -exec grep -l "SimDevice.$id" {} + 2>/dev/null)
    rm -f "$mark"
    if [ -n "$crashed" ]; then
      echo "live-ui-test: CRASH REPORT from $name, stop and tell super:"; echo "$crashed" | sed 's|.*/||'
      exit 9
    fi
  }
  trap cleanup EXIT
  trap 'exit 143' TERM INT
  perl -e 'alarm 1080; exec @ARGV' xcodebuild test -project InitechApp.xcodeproj -scheme InitechApp \
    -parallel-testing-enabled NO -destination "id=$id" -only-testing:InitechAppUITests/PaneFlowUITests \
    -test-timeouts-enabled YES -default-test-execution-time-allowance 600 > "$out/xcodebuild.log" 2>&1 &
  xb=$!
  for key in esc enter; do
    rm -f "${rig:?}/app-ui-$key.txt"
    wait_for "$out/prompt-$key.need" "$xb" || break
    raise "$key"
    wait_for "$out/prompt-$key.pressed" "$xb" || break
    sleep 20 # the pane acts on the key, and Claude writes the file or does not
    if [ -e "$rig/app-ui-$key.txt" ]; then echo "effect $key: file created"; else echo "effect $key: no file"; fi
  done
  wait "$xb"; rc=$?
  xb=""
  exit "$rc"
}
export -f step raise wait_for
export kind type rig a b out here bin
"$heavy" bash -c step | tee "$out/effects.txt"
rc=${PIPESTATUS[0]}
grep -E "Test Case .*(passed|failed|skipped|exceeded)|TEST (SUCCEEDED|FAILED)|: error: " "$out/xcodebuild.log" | sort -u | cut -c1-170
echo "expect: effect esc: no file; effect enter: file created"
echo "live-ui-test exit=$rc"
exit "$rc"
