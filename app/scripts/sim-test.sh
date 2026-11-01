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
# 18-minute bound, or heavy.sh stopping the step. After the run, a new crash
# report from that device fails the run (exit 9): stop and tell super.
# CRASH_FATAL=all (the default) fails on any report, because on a Mac with
# someone at it a crash puts a dialog on their screen. CRASH_FATAL=ui (the
# unattended app-tests runner) fails only on a report from the app, its test
# runners, SpringBoard or PosterBoard, and names any other as a warning: a new
# device's first boot often crashes unrelated system services.
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
  # Crash reports land a few seconds after a process dies, and a stopped run
  # never reaches the check below the test: look here too, after shutdown.
  sleep 10
  crashed=$(find "$reports" -newer "$mark" -name '*.ips' -exec grep -l "SimDevice.$udid" {} + 2>/dev/null)
  fatal="" warn=""
  for f in $crashed; do
    proc=$(basename "$f" | sed -E 's/-20[0-9]{2}-[0-9]{2}-[0-9]{2}-.*//')
    case "${CRASH_FATAL:-all}:$proc" in
      all:*|ui:InitechApp|ui:InitechApp*Runner|ui:*xctrunner*|ui:SpringBoard|ui:PosterBoard) fatal="$fatal $(basename "$f")" ;;
      *) warn="$warn $(basename "$f")" ;;
    esac
  done
  [ -n "$warn" ] && echo "sim-test: warning: crash reports from system services on $name (not failing the run):$warn" >&2
  if [ -n "$fatal" ]; then
    echo "sim-test: CRASH REPORT from $name, stop and tell super:$fatal" >&2
    rm -f "$mark"
    exit 9
  fi
  rm -f "$mark"
}
trap cleanup EXIT
trap 'exit 143' TERM INT

cd "$app" || exit 2
# A new device's first boot takes minutes on a busy host; xcodebuild launching
# into it before it finishes fails with "No such process". Boot it and wait
# for the boot to finish, bounded.
if ! perl -e 'alarm 420; exec @ARGV' xcrun simctl bootstatus "$udid" -b > "$log.boot" 2>&1; then
  echo "sim-test: $name did not finish booting in 7 minutes" >&2
  exit 7
fi
echo "sim-test: $name booted"
perl -e 'alarm 1080; exec @ARGV' xcodebuild test -project InitechApp.xcodeproj -scheme InitechApp \
  -parallel-testing-enabled NO -destination "id=$udid" "$@" > "$log" 2>&1 &
xb=$!
wait "$xb"; rc=$?
xb=""

# The EXIT trap shuts the device down, deletes it, and fails the run with 9
# on any new crash report naming it.
exit "$rc"
