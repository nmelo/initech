#!/usr/bin/env bash
# rigs-resources.sh: what a rigs job used on the shared workbench (ini-c5a.7,
# theguy's condition: each rigs job records its peak memory, process count
# and disk use in its own log).
#
#   rigs-resources.sh start   # in the foreground, from the job's first step
#   rigs-resources.sh report  # in an always() step at the job's end
#
# start finds the job's scope in the foreground, while its step's shell is
# still its parent (a backgrounded start loses that chain to Runner.Worker
# when the shell exits), then returns and leaves one background loop that
# samples every 5 seconds, for at most RIGS_SAMPLES samples (default
# 1440, two hours, so it never outlives its job), THIS JOB's memory and
# process count, appending to $RUNNER_TEMP/rigs-resources. A runner holds one
# job at a time, so the job is: on Linux, the runner's own cgroup
# (memory.current and pids.current; one runner's unit); on macOS, the process
# tree under the job's Runner.Worker (pid, ppid and rss only, never command
# lines). Never the user's total: every runner shares one user. Where neither
# scope is found it records nothing and report says so.
# report prints one line: the peak of each, the scope, the sample count, and
# the disk used by the job's workspace and the shared Go build cache, with the
# home filesystem's free space. On Linux it also says what the peak was made
# of and when: memory.current counts the page cache as well as process memory,
# so each sample also records memory.stat's anon (process memory) and file
# (page cache) lines; the report names the peak's time with its anon and file
# parts, and the anon peak with its time. One RIGS SAMPLE line per sample
# (UTC time, total, anon, file, processes) follows, so the job's own
# timestamped log lines (a race shard's package lines) place any sample.
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
  (
    for _ in $(seq 1 "${RIGS_SAMPLES:-1440}"); do
      if [ -n "$cg" ]; then
        kb=$(( $(cat "$cg/memory.current") / 1024 )); n=$(cat "$cg/pids.current")
        read -r anon file < <(awk '$1=="anon" {a=$2} $1=="file" {f=$2} END {print int(a/1024), int(f/1024)}' "$cg/memory.stat" 2>/dev/null)
      else
        read -r kb n < <(ps -A -o pid=,ppid=,rss= | awk -v root="$worker" '
          {pp[$1]=$2; rss[$1]=$3}
          END {for (p in pp) {q=p; while (q!="" && q!=root && q>1) q=pp[q]; if (q==root) {s+=rss[p]; c++}} print s+0, c+0}')
      fi
      # anon and file only where memory.stat exists (the Linux cgroup); a
      # macOS sample stays time, kilobytes, processes.
      if [ -n "$cg" ]; then
        echo "$(date +%s) $kb $n ${anon:--} ${file:--}" >> "$f"
      else
        echo "$(date +%s) $kb $n" >> "$f"
      fi
      sleep 5
    done
  ) < /dev/null > /dev/null 2>&1 &
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
  # What the peak was made of, and when (Linux cgroup scope only).
  awk '$1!="scope" && $4!="-" && $4!="" {
      if ($2>t) {t=$2; tt=$1; ta=$4; tf=$5}
      if ($4>a) {a=$4; at=$1}
      seen=1
    }
    END {if (seen) printf "%d %s %d %d %d %s\n", t, tt, ta, tf, a, at}' "$f" | {
    read -r t tt ta tf a at || exit 0
    utc() { date -u -d "@$1" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -r "$1" +%Y-%m-%dT%H:%M:%SZ; }
    echo "RIGS RESOURCES: peak at $(utc "$tt"): anon=$((ta / 1024))MB file=$((tf / 1024))MB; anon peak $((a / 1024))MB at $(utc "$at")"
  }
  awk '$1!="scope" {print}' "$f" | while read -r ts kb n anon file; do
    when=$(date -u -d "@$ts" +%H:%M:%S 2>/dev/null || date -u -r "$ts" +%H:%M:%S)
    if [ "${anon:--}" = "-" ]; then parts="anon=- file=-"; else parts="anon=$((anon / 1024))MB file=$((file / 1024))MB"; fi
    echo "RIGS SAMPLE $when total=$((kb / 1024))MB $parts procs=$n"
  done
  ;;
*)
  echo "usage: rigs-resources.sh start|report" >&2
  exit 2
  ;;
esac
