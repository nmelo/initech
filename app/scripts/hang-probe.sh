#!/bin/bash
# hang-probe.sh records, once, what xcodebuild is doing when it has gone quiet
# right after a test suite ended. A clean run prints "Testing started
# completed", the result bundle path and "** TEST SUCCEEDED **" within seconds
# of its last suite; a run that prints nothing more for HANG_QUIET seconds
# (60 by default) is stuck in its test operation, and this is the evidence of
# where.
#
#   app/scripts/hang-probe.sh <xcodebuild pid> <log> <device udid> <out>
#
# sim-test.sh starts it beside xcodebuild and stops it by pid. It looks every
# HANG_EVERY seconds (15) while the pid lives, writes <out> at most once, and
# prints one line saying so. <out> holds the process tree under xcodebuild
# (pid, ppid, state, elapsed time and command name: never arguments, which can
# carry other jobs' secrets), what of the app, its test runner and
# testmanagerd still runs in the device, the newest result bundle's size and
# age, and a 3-second sample of xcodebuild's threads (in <out>.sample).
set -u
xb=${1:?xcodebuild pid}; log=${2:?log}; udid=${3:?udid}; out=${4:?out}
quiet=${HANG_QUIET:-60}
every=${HANG_EVERY:-15}
derived=${HANG_DERIVED:-$HOME/Library/Developer/Xcode/DerivedData}
simctl=${HANG_SIMCTL:-xcrun simctl} # a test points this at a stub, never a simulator

# tree prints xb and its descendants, one ps line each, command names only.
tree() {
  local pids="$xb" next="$xb" p
  while [ -n "$next" ]; do
    local kids=""
    for p in $next; do kids="$kids $(pgrep -P "$p" 2>/dev/null | tr '\n' ' ')"; done
    next=$(echo $kids)
    [ -n "$next" ] && pids="$pids $next"
  done
  ps -o pid=,ppid=,stat=,etime=,comm= -p "$(echo $pids | tr ' ' ',')" 2>/dev/null
}

while kill -0 "$xb" 2>/dev/null; do
  sleep "$every"
  [ -e "$out" ] && continue
  [ -f "$log" ] || continue
  # Only right after a suite line: a test that is merely slow is not this.
  tail -n 2 "$log" | grep -qE "^Test Suite '.*' (passed|failed) at |^[[:space:]]+Executed [0-9]+ tests?, " || continue
  age=$(( $(date +%s) - $(stat -f %m "$log") ))
  [ "$age" -ge "$quiet" ] || continue
  kill -0 "$xb" 2>/dev/null || break
  {
    echo "hang probe at $(date -u +%FT%TZ): xcodebuild pid $xb, the log silent ${age}s after: $(tail -n 2 "$log" | head -n 1)"
    echo "## process tree under xcodebuild (pid ppid state elapsed command)"
    tree
    echo "## in the device $udid (launchctl labels)"
    $simctl spawn "$udid" launchctl list 2>/dev/null | grep -iE 'initech|xctrunner|testmanager|XCTest' || echo "(none, or the device did not answer)"
    echo "## newest result bundle"
    newest=$(ls -td "$derived"/InitechApp-*/Logs/Test/*.xcresult 2>/dev/null | head -n 1)
    if [ -n "$newest" ]; then
      echo "$newest: $(du -sk "$newest" 2>/dev/null | cut -f1) KB, modified $(( $(date +%s) - $(stat -f %m "$newest") ))s ago"
    else
      echo "(none under $derived)"
    fi
  } > "$out" 2>&1
  sample "$xb" 3 -file "$out.sample" >/dev/null 2>&1 || echo "(sample did not run)" >> "$out"
  echo "sim-test: xcodebuild silent ${age}s after its last suite; what it was doing is in $(basename "$out")"
done
