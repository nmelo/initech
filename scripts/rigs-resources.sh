#!/usr/bin/env bash
# rigs-resources.sh: what a rigs job used on the shared workbench (ini-c5a.7,
# theguy's condition: each rigs job records its peak memory, process count
# and disk use in its own log).
#
#   rigs-resources.sh start   # in the background, from the job's first step
#   rigs-resources.sh report  # in an always() step at the job's end
#
# start samples every 5 seconds, for at most RIGS_SAMPLES samples (default
# 1440, two hours, so it never outlives its job), THIS JOB's memory and
# process count, appending to $RUNNER_TEMP/rigs-resources. A runner holds one
# job at a time, so the job is: on Linux, the runner's own cgroup
# (memory.current and pids.current; one runner's unit); on macOS, the process
# tree under the job's Runner.Worker (pid, ppid and rss only, never command
# lines). Never the user's total: every runner shares one user. Where neither
# scope is found it records nothing and report says so.
# report prints one line: the peak of each, the scope, the sample count, and
# the disk used by the job's workspace and the shared Go build cache, with the
# home filesystem's free space.
set -u
f="${RUNNER_TEMP:-/tmp}/rigs-resources"
case "${1:-}" in
start)
  : > "$f"
  cg=""
  if [ -r /proc/self/cgroup ]; then
    cg="/sys/fs/cgroup$(cut -d: -f3 /proc/self/cgroup | head -1)"
    [ -r "$cg/memory.current" ] && [ -r "$cg/pids.current" ] || cg=""
  fi
  worker=""
  if [ -z "$cg" ]; then
    p=$$
    while [ -n "$p" ] && [ "$p" -gt 1 ]; do
      case "$(ps -o comm= -p "$p" 2>/dev/null)" in *Runner.Worker*) worker=$p; break ;; esac
      p=$(ps -o ppid= -p "$p" 2>/dev/null | tr -d ' ')
    done
  fi
  [ -n "$cg" ] && echo "scope cgroup ${cg#/sys/fs/cgroup}" >> "$f"
  [ -n "$worker" ] && echo "scope process-tree Runner.Worker pid $worker" >> "$f"
  [ -z "$cg$worker" ] && { echo "scope none" >> "$f"; exit 0; }
  for _ in $(seq 1 "${RIGS_SAMPLES:-1440}"); do
    if [ -n "$cg" ]; then
      kb=$(( $(cat "$cg/memory.current") / 1024 )); n=$(cat "$cg/pids.current")
    else
      read -r kb n < <(ps -A -o pid=,ppid=,rss= | awk -v root="$worker" '
        {pp[$1]=$2; rss[$1]=$3}
        END {for (p in pp) {q=p; while (q!="" && q!=root && q>1) q=pp[q]; if (q==root) {s+=rss[p]; c++}} print s+0, c+0}')
    fi
    echo "$(date +%s) $kb $n" >> "$f"
    sleep 5
  done
  ;;
report)
  if [ ! -s "$f" ]; then
    echo "RIGS RESOURCES: no samples (the sampler never ran)"
    exit 0
  fi
  scope=$(awk '$1=="scope" {sub(/^scope /, ""); print; exit}' "$f")
  read -r samples peak_kb peak_n < <(awk '$1!="scope" {c++; if ($2>r) r=$2; if ($3>n) n=$3} END {print c+0, r+0, n+0}' "$f")
  ws=$(du -sh "${GITHUB_WORKSPACE:-.}" 2>/dev/null | cut -f1)
  cache=$(du -sh "$(go env GOCACHE 2>/dev/null || echo "$HOME/.cache/go-build")" 2>/dev/null | cut -f1)
  free=$(df -h "$HOME" 2>/dev/null | awk 'NR==2 {print $4 " free of " $2}')
  echo "RIGS RESOURCES: scope=[${scope:-unknown}] peak_memory=$((peak_kb / 1024))MB peak_processes=$peak_n samples=$samples workspace=${ws:-?} gocache=${cache:-?} home_fs=${free:-?}"
  ;;
*)
  echo "usage: rigs-resources.sh start|report" >&2
  exit 2
  ;;
esac
