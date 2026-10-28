#!/bin/bash
# redraw-cpu.sh [seconds] -- what drawing the fleet costs (ini-ysr.1).
#
# Samples %cpu of Ghostty and of every initech process once every 5 seconds
# for the given time (default 60) and prints each one's average, the folder
# each initech runs in (so a session can be named), how many agent panes are
# up (claude processes) and the load average. Reads only `ps -o pid=,comm=`,
# `ps -o %cpu=` and each initech's working folder: never a command line,
# which can carry another agent's secrets. Runs on the stock macOS bash 3.2.
#
# The redraw epic is judged by this command: the same rigs and panes up,
# before and after, numbers on the epic.
set -u
secs=${1:-60}
step=5
n=$((secs / step))
[ "$n" -ge 1 ] || n=1

# pid and basename of comm for Ghostty and initech, taken once: a process
# that starts during the minute is not counted, one that exits stops adding.
targets=$(ps -A -o pid=,comm= | awk '{ c = $2; sub(/.*\//, "", c); if (c == "ghostty" || c == "initech") print $1, c }')
if [ -z "$targets" ]; then
	echo "no ghostty or initech process found"
	exit 1
fi

samples=""
for _ in $(seq 1 "$n"); do
	while read -r pid name; do
		c=$(ps -o %cpu= -p "$pid" 2>/dev/null | tr -d ' ')
		[ -n "$c" ] && samples="$samples$pid $name $c"$'\n'
	done <<<"$targets"
	sleep "$step"
done

echo "sampled ${secs}s, every ${step}s, ending $(date '+%Y-%m-%d %H:%M')"
printf '%s' "$samples" | awk -v n="$n" '
	NF == 3 { sum[$1] += $3; name[$1] = $2 }
	END { for (p in sum) printf "%7.1f%%  %-8s pid %s\n", sum[p] / n, name[p], p }' |
	sort -rn |
	while read -r avg name _ pid; do
		dir=""
		[ "$name" = initech ] && dir=$(lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | tail -1)
		printf '%8s  %-8s pid %-6s %s\n' "$avg" "$name" "$pid" "$dir"
	done
echo "agent panes (claude processes): $(ps -A -o comm= | awk '{ c = $1; sub(/.*\//, "", c); if (c == "claude") k++ } END { print k + 0 }')"
echo "load: $(sysctl -n vm.loadavg)"
