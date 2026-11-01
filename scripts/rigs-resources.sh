#!/usr/bin/env bash
# rigs-resources.sh: what a rigs job used on the shared workbench (ini-c5a.7,
# theguy's condition: each rigs job records its peak memory, process count
# and disk use in its own log).
#
#   rigs-resources.sh start   # in the background, from the job's first step
#   rigs-resources.sh report  # in an always() step at the job's end
#
# start samples every 5 seconds, for at most RIGS_SAMPLES samples (default
# 1440, two hours, so it never outlives its job), this
# user's total resident memory and process count (ps sees only this user's
# processes in the runner sandbox), appending to $RUNNER_TEMP/rigs-resources.
# report prints one line: the peak of each, the sample count, and the disk
# used by the job's workspace and the shared Go build cache, with the home
# filesystem's free space.
set -u
f="${RUNNER_TEMP:-/tmp}/rigs-resources"
case "${1:-}" in
start)
  : > "$f"
  for _ in $(seq 1 "${RIGS_SAMPLES:-1440}"); do
    rss=$(ps -u "$(id -u)" -o rss= | awk '{s+=$1} END {print s+0}')
    n=$(ps -u "$(id -u)" -o pid= | wc -l | tr -d ' ')
    echo "$(date +%s) $rss $n" >> "$f"
    sleep 5
  done
  ;;
report)
  if [ ! -s "$f" ]; then
    echo "RIGS RESOURCES: no samples (the sampler never ran)"
    exit 0
  fi
  read -r samples peak_kb peak_n < <(awk '{c++; if ($2>r) r=$2; if ($3>n) n=$3} END {print c, r, n}' "$f")
  ws=$(du -sh "${GITHUB_WORKSPACE:-.}" 2>/dev/null | cut -f1)
  cache=$(du -sh "$(go env GOCACHE 2>/dev/null || echo "$HOME/.cache/go-build")" 2>/dev/null | cut -f1)
  free=$(df -h "$HOME" 2>/dev/null | awk 'NR==2 {print $4 " free of " $2}')
  echo "RIGS RESOURCES: peak_rss=$((peak_kb / 1024))MB peak_processes=$peak_n samples=$samples workspace=${ws:-?} gocache=${cache:-?} home_fs=${free:-?}"
  ;;
*)
  echo "usage: rigs-resources.sh start|report" >&2
  exit 2
  ;;
esac
