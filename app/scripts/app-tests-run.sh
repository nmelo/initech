#!/bin/bash
# app-tests-run.sh starts the app-tests job for one device at one ref, after
# checking a Mac runner is online: a job queued for an offline self-hosted
# runner waits up to 24 hours instead of failing, so this refuses first.
#
#   app/scripts/app-tests-run.sh <ref> <iphone|ipad>
#
# Prints the run's URL; cite the run id on the bead.
set -u
ref=${1:?ref (a sha or branch)}; device=${2:?iphone or ipad}
case "$device" in iphone|ipad) ;; *) echo "app-tests-run: device is iphone or ipad" >&2; exit 2 ;; esac
repo=${REPO:-nmelo/initech}

label=$(gh variable get MAC_RUNNER --repo "$repo" 2>/dev/null)
[ -n "$label" ] || { echo "app-tests-run: MAC_RUNNER is not set; the job would run on GitHub's macos-latest, not ours. Refused." >&2; exit 3; }
online=$(gh api "repos/$repo/actions/runners" --paginate \
  --jq "[.runners[] | select(.status == \"online\") | select([.labels[].name] | index(\"$label\"))] | length")
if [ "${online:-0}" -eq 0 ]; then
  echo "app-tests-run: no runner labelled $label is online. Refused: nothing started." >&2
  exit 3
fi
if [ "$(gh variable get APP_TESTS --repo "$repo" 2>/dev/null)" != "on" ]; then
  echo "app-tests-run: APP_TESTS is not 'on' (set only once a load sample shows the runners' machine can take one simulator). Refused: nothing started." >&2
  exit 3
fi

gh workflow run app-tests.yml --repo "$repo" --ref "$ref" -f device="$device" || exit 1
sleep 3
gh run list --repo "$repo" --workflow app-tests.yml --limit 1 --json databaseId,url,status \
  --jq '.[0] | "app-tests-run: started run \(.databaseId) (\(.status)): \(.url)"'
