#!/bin/bash
# wait-load.sh waits until the machine can take one simulator run: the
# 5-minute load under MAX5 and the 1-minute under MAX1, held for three checks
# 10 seconds apart. It prints the figures as it goes and refuses (exit 4)
# after LIMIT seconds, so a busy machine is named, not waited on forever.
#
#   app/scripts/wait-load.sh            # defaults: MAX5=30 MAX1=50 LIMIT=900
set -u
max5=${MAX5:-30} max1=${MAX1:-50} limit=${LIMIT:-900}
calm=0 waited=0
while :; do
  read -r one five _ <<< "$(sysctl -n vm.loadavg | tr -d '{}')"
  one=${one%.*} five=${five%.*}
  if [ "$five" -lt "$max5" ] && [ "$one" -lt "$max1" ]; then calm=$((calm + 1)); else calm=0; fi
  echo "wait-load: $(date +%H:%M:%S) 1-min $one, 5-min $five (need under $max1 and $max5), calm $calm/3"
  [ "$calm" -ge 3 ] && exit 0
  if [ "$waited" -ge "$limit" ]; then
    echo "wait-load: refused after ${limit}s: the machine is busy (1-min $one, 5-min $five)" >&2
    exit 4
  fi
  sleep 10; waited=$((waited + 10))
done
