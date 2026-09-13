#!/bin/bash
# branch-cut-check.sh -- prove a release-branch cut contains what it should and
# nothing it should not, by ANCESTRY AND CONTENT rather than by reading the log.
#
# Written for v2.13.4 (ini-cctz), the first cut taken from a release branch
# instead of from main, where main carried an unfinished feature that could not
# ship. Reading `git log` is not enough for that question: a cherry-pick gets a
# NEW SHA, so an ancestry check alone passes a re-applied commit, and a subject
# grep alone passes a commit whose subject was reworded.
#
# Usage:
#   scripts/branch-cut-check.sh --base v2.13.3 --cut <sha> [--main origin/main]
#                               [--expect N] [--exclude-grep RE]
#
# Checks, all of which must pass:
#   a  --base is an ancestor of --cut
#   b  no commit that is on --main but not on --base is an ancestor of --cut
#   c  (with --exclude-grep) no commit on the cut matches the pattern, and none
#      shares a git patch-id with an excluded commit -- this is the check that
#      sees a cherry-pick, which a and b cannot
#   d  (with --exclude-grep) no file touched by an excluded commit is touched by
#      the cut's diff
#   e  (with --expect) the cut carries exactly that many commits past the base
#
# WHY --exclude-grep IS REQUIRED TO MATCH SOMETHING: cherry-picking FROM main is
# the normal thing a release branch does, so "shares a patch-id with main" is
# not by itself wrong -- only sharing one with the work being EXCLUDED is. The
# tool cannot know which work that is, so the caller names it; and a pattern
# that matches no commit is refused rather than passed, because a stale pattern
# reports a clean cut for every input (the vacuous-guard shape, ini-j0er).
set -u

BASE=""; CUT=""; MAIN="origin/main"; EXPECT=""; EXRE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --base) BASE="${2:-}"; shift 2 ;;
    --cut) CUT="${2:-}"; shift 2 ;;
    --main) MAIN="${2:-}"; shift 2 ;;
    --expect) EXPECT="${2:-}"; shift 2 ;;
    --exclude-grep) EXRE="${2:-}"; shift 2 ;;
    -h|--help) sed -n '2,32p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$BASE" ] && [ -n "$CUT" ] || { echo "usage: $0 --base <ref> --cut <ref> [--main <ref>] [--expect N] [--exclude-grep RE]" >&2; exit 2; }
cd "$(git rev-parse --show-toplevel)" || exit 2

resolve(){ git rev-parse --verify --quiet "$1^{commit}" || { echo "cannot resolve $2 ($1) -- fetch first; an unfetched ref is not an absent one" >&2; exit 2; }; }
BASE_SHA=$(resolve "$BASE" base) || exit 2
CUT_SHA=$(resolve "$CUT" cut) || exit 2
MAIN_SHA=$(resolve "$MAIN" main) || exit 2

fail=0
say(){ printf '  %s %-4s %s\n' "$1" "$2" "$3"; }
printf 'base=%s cut=%s main=%s\n' "$(git rev-parse --short "$BASE_SHA")" "$(git rev-parse --short "$CUT_SHA")" "$(git rev-parse --short "$MAIN_SHA")"

# a
if git merge-base --is-ancestor "$BASE_SHA" "$CUT_SHA"; then say a PASS "$BASE is an ancestor of the cut"
else say a FAIL "$BASE is NOT an ancestor of the cut"; fail=1; fi

# b
mainonly=$(git rev-list --no-merges "$BASE_SHA..$MAIN_SHA")
n_main=$(printf '%s\n' "$mainonly" | grep -c . || true)
hits=0
for c in $mainonly; do
  if git merge-base --is-ancestor "$c" "$CUT_SHA"; then hits=$((hits+1)); printf '        on the cut: %s\n' "$(git log --oneline -1 "$c" | cut -c1-80)"; fi
done
if [ "$hits" -eq 0 ]; then say b PASS "none of $n_main ${MAIN}-only commits is an ancestor of the cut"
else say b FAIL "$hits ${MAIN}-only commit(s) are ancestors of the cut"; fail=1; fi

cutcommits=$(git rev-list --no-merges "$BASE_SHA..$CUT_SHA")
n_cut=$(printf '%s\n' "$cutcommits" | grep -c . || true)

if [ -n "$EXRE" ]; then
  excluded=""
  for c in $mainonly; do
    if git log -1 --format='%s%n%b' "$c" | grep -qiE "$EXRE"; then excluded="$excluded $c"; fi
  done
  n_ex=$(printf '%s\n' $excluded | grep -c . || true)
  if [ "$n_ex" -eq 0 ]; then
    say c REFUSE "--exclude-grep '$EXRE' matches no ${MAIN}-only commit; a pattern that excludes nothing passes every cut"
    fail=1
  else
    # c(i): no commit on the cut matches the pattern
    cm=0
    for c in $cutcommits; do
      if git log -1 --format='%s%n%b' "$c" | grep -qiE "$EXRE"; then cm=$((cm+1)); printf '        matches: %s\n' "$(git log --oneline -1 "$c" | cut -c1-80)"; fi
    done
    # c(ii): no commit on the cut shares a patch-id with an excluded commit
    exids=$(for c in $excluded; do git show "$c" | git patch-id --stable | cut -d' ' -f1; done)
    pm=0
    for c in $cutcommits; do
      pid=$(git show "$c" | git patch-id --stable | cut -d' ' -f1)
      if [ -n "$pid" ] && printf '%s\n' "$exids" | grep -qx "$pid"; then pm=$((pm+1)); printf '        re-applied: %s\n' "$(git log --oneline -1 "$c" | cut -c1-80)"; fi
    done
    if [ $((cm+pm)) -eq 0 ]; then say c PASS "$n_cut cut commit(s) match none of the $n_ex excluded commits, by subject or patch-id"
    else say c FAIL "$cm by subject, $pm by patch-id"; fail=1; fi

    # d: files
    exfiles=$(for c in $excluded; do git show --name-only --format='' "$c"; done | sed '/^$/d' | sort -u)
    cutfiles=$(git diff --name-only "$BASE_SHA" "$CUT_SHA" | sort -u)
    overlap=$(printf '%s\n' "$cutfiles" | grep -Fxf <(printf '%s\n' "$exfiles") || true)
    if [ -z "$overlap" ]; then say d PASS "the cut touches none of the $(printf '%s\n' "$exfiles" | grep -c .) excluded files"
    else say d FAIL "the cut touches excluded files:"; printf '%s\n' "$overlap" | sed 's/^/          /'; fail=1; fi
  fi
else
  say c SKIP "no --exclude-grep given; content exclusion not checked"
fi

# e
if [ -n "$EXPECT" ]; then
  if [ "$n_cut" = "$EXPECT" ]; then say e PASS "$n_cut commit(s) past the base, as expected"
  else say e FAIL "$n_cut commit(s) past the base, expected $EXPECT"; git log --oneline "$BASE_SHA..$CUT_SHA" | sed 's/^/          /'; fail=1; fi
elif [ "$n_cut" -eq 0 ]; then
  say e FAIL "the cut carries no commits past the base; pass --expect 0 if that is deliberate"; fail=1
else
  say e INFO "$n_cut commit(s) past the base (no --expect given)"
fi

[ $fail -eq 0 ] && echo "RESULT: PASS" || echo "RESULT: FAIL"
exit $fail
