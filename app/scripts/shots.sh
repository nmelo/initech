#!/bin/bash
# shots.sh is the pictures of an app-tests run: a run names screens, the tests
# stop at each one, and sim-test.sh takes its picture here. A screen is a stop
# a test makes (ScreenshotTests: home, pane) or a view a test renders itself
# (blockers, progress, panes).
#
#   shots.sh check <names>               the names, cleaned, one line; exit 2 on a refusal
#   shots.sh pass <dir> <udid> <names>   picture the named stops waiting in <dir>, let every stop go
#   shots.sh collect <dir> <out> <names> copy the named pictures to <out>; exit 10 if one is missing
#
# FIXTURES ONLY: the repo is public and so are its artifacts. A picture comes
# from the test fixtures, never from a live service: check refuses a live-
# screen, and any named screen while the run is paired with a live service.
# A stop a test reached is <stop>.ready in <dir>; the test waits there for
# <stop>.done. pass writes .done for every stop, named or not, so a stop the
# run did not name never holds a test. A capture that fails leaves
# <stop>.failed. A missing picture never passes silently: collect names each
# one on stderr and exits 10.
set -u
set -f # names are words, never patterns
verb=${1:?check, pass or collect}; shift
# SHOT_SIMCTL stands in for simctl in the tests.
simctl=${SHOT_SIMCTL:-xcrun simctl}

case "$verb" in
  check)
    name='^[a-z0-9][a-z0-9-]{0,39}$'
    out=""
    for n in $(printf '%s\n' "${1-}" | tr ',' ' '); do
      case "$n" in
        live-*) echo "shots: refused: $n is a live screen, and pictures come from the test fixtures only" >&2; exit 2 ;;
      esac
      [[ "$n" =~ $name ]] || { echo "shots: refused: \"$n\" is not a screen name (lowercase letters, digits and dashes)" >&2; exit 2; }
      case " $out " in *" $n "*) ;; *) out="$out $n" ;; esac
    done
    [ -n "$out" ] || exit 0
    if [ -n "${TEST_RUNNER_LIVE_PAIRING:-}${LIVE_PAIRING:-}" ]; then
      echo "shots: refused: this run is paired with a live service, and pictures come from the test fixtures only" >&2
      exit 2
    fi
    echo "${out# }"
    ;;
  pass)
    dir=${1:?dir}; udid=${2:?udid}; names=${3-}
    set +f
    for ready in "$dir"/*.ready; do
      [ -e "$ready" ] || continue
      stop=$(basename "$ready" .ready)
      [ -e "$dir/$stop.done" ] && continue
      case " $names " in
        *" $stop "*)
          # The test asserted what is on screen before it stopped; this lets a
          # push or a sheet finish moving.
          sleep "${SHOT_SETTLE:-1}"
          if $simctl io "$udid" screenshot "$dir/$stop.png" >> "$dir/shots.log" 2>&1 && [ -s "$dir/$stop.png" ]; then
            echo "shots: took $stop"
          else
            rm -f "$dir/$stop.png"
            : > "$dir/$stop.failed"
          fi
          ;;
      esac
      : > "$dir/$stop.done"
    done
    ;;
  collect)
    dir=${1:?dir}; out=${2:?out}; names=${3-}
    [ -n "$names" ] || exit 0
    mkdir -p "$out" || exit 2
    rc=0
    for n in $names; do
      if [ -s "$dir/$n.png" ]; then
        cp "$dir/$n.png" "$out/$n.png" || exit 2
        echo "shots: screen $n: $n.png"
      elif [ -e "$dir/$n.failed" ]; then
        echo "shots: screen $n was reached, but its picture could not be taken" >&2
        rc=10
      else
        echo "shots: screen $n was not reached: no test stopped at it or drew it" >&2
        rc=10
      fi
    done
    if [ "$rc" -ne 0 ]; then
      set +f
      reached=""
      for f in "$dir"/*.ready "$dir"/*.png; do
        [ -e "$f" ] || continue
        s=$(basename "$f"); s=${s%.ready}; s=${s%.png}
        case " $reached " in *" $s "*) ;; *) reached="$reached $s" ;; esac
      done
      echo "shots: the screens this run reached:${reached:- none}" >&2
    fi
    exit "$rc"
    ;;
  *)
    echo "shots: the verb is check, pass or collect" >&2
    exit 2
    ;;
esac
