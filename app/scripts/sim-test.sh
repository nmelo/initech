#!/bin/bash
# sim-test.sh runs the app's tests on a simulator made for this run alone:
# create, test, shut down, delete. A simulator kept from one run to the next
# carries its state with it, and one such device crashed PosterBoard at every
# boot, putting a crash dialog on the Mac's screen. Never reuse one.
#
#   app/scripts/sim-test.sh <iphone|ipad> <log file> [xcodebuild test args...]
#
# Run it under the rig lock, one at a time:
#   ~/Desktop/Projects/initech/super/tools/heavy.sh app/scripts/sim-test.sh iphone /tmp/run.log
#
# The device is shut down and deleted however the run ends: pass, fail, the
# 18-minute bound, or heavy.sh stopping the step. After the run, any new crash
# report from that device fails the run (exit 9): stop and tell super.
set -u
kind=${1:?iphone or ipad}; log=${2:?log file}; shift 2
case "$kind" in
  iphone) type="iPhone 17 Pro" ;;
  ipad) type="iPad Pro 11-inch (M5)" ;;
  *) echo "sim-test: kind is iphone or ipad" >&2; exit 2 ;;
esac
runtime=${SIM_RUNTIME:-com.apple.CoreSimulator.SimRuntime.iOS-27-0}
app=$(cd "$(dirname "$0")/.." && pwd)
reports="$HOME/Library/Logs/DiagnosticReports"
mark=$(mktemp -t sim-test-mark) || exit 2

name="${INITECH_AGENT:-run}-$kind-$(date +%H%M%S)"
udid=$(xcrun simctl create "$name" "$type" "$runtime") || { echo "sim-test: could not create $name" >&2; exit 2; }
echo "sim-test: created $name $udid"

xb=""
cleanup() {
  # Stop xcodebuild gently, then shut the device down before deleting it:
  # never a hard kill while it boots.
  if [ -n "$xb" ] && kill -0 "$xb" 2>/dev/null; then
    kill -TERM "$xb" 2>/dev/null
    for _ in $(seq 1 30); do kill -0 "$xb" 2>/dev/null || break; sleep 1; done
  fi
  xcrun simctl shutdown "$udid" >/dev/null 2>&1
  xcrun simctl delete "$udid" >/dev/null 2>&1 && echo "sim-test: deleted $name"
  rm -f "$mark"
}
trap cleanup EXIT
trap 'exit 143' TERM INT

cd "$app" || exit 2
perl -e 'alarm 1080; exec @ARGV' xcodebuild test -project InitechApp.xcodeproj -scheme InitechApp \
  -parallel-testing-enabled NO -destination "id=$udid" "$@" > "$log" 2>&1 &
xb=$!
wait "$xb"; rc=$?
xb=""

# A crash report names the device in its coalition. Any new one from this
# device means the run hurt the simulator: fail loudly.
crashed=$(find "$reports" -newer "$mark" -name '*.ips' -exec grep -l "SimDevice.$udid" {} + 2>/dev/null)
if [ -n "$crashed" ]; then
  echo "sim-test: CRASH REPORT from $name, stop and tell super:" >&2
  echo "$crashed" | sed 's|.*/||' >&2
  exit 9
fi
exit "$rc"
