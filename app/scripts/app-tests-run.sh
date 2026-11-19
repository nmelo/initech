#!/bin/bash
# app-tests-run.sh starts the app-tests job for one device at one ref, after
# checking a Mac runner is online: a job queued for an offline self-hosted
# runner waits up to 24 hours instead of failing, so this refuses first.
#
#   app/scripts/app-tests-run.sh <branch|commit> <iphone|ipad> [screens] [tests]
#
# <commit>: GitHub starts a run only at a branch, so a commit id is run at
#   main, with the id for the job to check out, when main contains it. A commit
#   main does not contain is refused: push it to a branch and give the branch.
# [screens]: the screens to picture, comma separated (home,pane); the job
#   uploads them as the artifact app-screens-<run id>, kept one day. Fixtures
#   only (shots.sh). Empty or absent: no pictures.
# [tests]: the tests to run, comma separated, as xcodebuild's -only-testing
#   takes them (InitechAppUITests/ScreenshotTests). Empty or absent: all.
#
# Prints the run's URL; cite the run id on the bead. Exit 2: a bad argument.
# Exit 3: refused before asking GitHub for a run. Exit 4: GitHub refused the
# run. In each case one line on stderr and nothing started.
set -u
ref=${1:?a branch or a commit id}; device=${2:?iphone or ipad}; screens=${3-}; only=${4-}
case "$device" in iphone|ipad) ;; *) echo "app-tests-run: device is iphone or ipad" >&2; exit 2 ;; esac
repo=${REPO:-nmelo/initech}
here=$(cd "$(dirname "$0")" && pwd)
if [ -n "$screens" ]; then "$here/shots.sh" check "$screens" > /dev/null || exit 2; fi

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

branch=$ref
args=(-f "device=$device")
if ! gh api "repos/$repo/branches/$ref" --silent > /dev/null 2>&1; then
  full=$(gh api "repos/$repo/commits/$ref" --jq .sha 2>/dev/null) || full=""
  if [ -z "$full" ]; then
    echo "app-tests-run: $repo has no branch and no commit named $ref. Refused: nothing started." >&2
    exit 3
  fi
  # ahead or identical: main holds every commit this one does, so it is on main.
  onmain=$(gh api "repos/$repo/compare/$full...main" --jq .status 2>/dev/null) || onmain=""
  case "$onmain" in
    ahead|identical) ;;
    *)
      echo "app-tests-run: commit ${full:0:7} is not on main, and a run starts only at a commit of the branch it runs on: push it to a branch and give the branch. Refused: nothing started." >&2
      exit 3
      ;;
  esac
  branch=main
  args+=(-f "sha=$full")
fi
if [ -n "$screens" ]; then args+=(-f "screens=$screens"); fi
if [ -n "$only" ]; then args+=(-f "only=$only"); fi

if ! said=$(gh workflow run app-tests.yml --repo "$repo" --ref "$branch" "${args[@]}" 2>&1); then
  echo "app-tests-run: GitHub refused the run at $branch: $(echo "$said" | head -1 | cut -c1-160). Nothing started." >&2
  exit 4
fi
sleep "${APP_TESTS_RUN_SETTLE:-3}" # the new run takes a moment to be listed
gh run list --repo "$repo" --workflow app-tests.yml --limit 1 --json databaseId,url,status \
  --jq '.[0] | "app-tests-run: started run \(.databaseId) (\(.status)): \(.url)"'
