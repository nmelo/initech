#!/usr/bin/env bash
# lint-test-names.sh — fail when *_test.go files contain test functions whose
# names admit "we just call it and see if it crashes" intent.
#
# Banned suffixes after Test... separator: NoOp, NoPanic, DoesNotPanic, Smoke.
# Match boundary [^a-z0-9]|$ so _NoPanicOnNarrow still matches (the O is
# an uppercase camelCase continuation, a separate "word") but _NoPanicky
# does not (the k is lowercase, extending Panic into a different word).
#
# Override: if the line DIRECTLY above the function definition contains
# "lint:test-name-allow", the match is suppressed. Use sparingly — the point
# is to force a real assertion or an honest name.
#
# Why this exists: the repo audit found 88 zero-assertion test functions, many
# named for what they admit they do (TestX_NoPanic, TestX_Smoke). Coverage %
# stays high; mutation kill rate stays near zero. The cheapest cultural lever
# is rejecting the names at PR time so authors either add an assertion or
# pick a name describing a real contract. ini-ybe.1.
#
# Exit codes:
#   0 — no banned matches.
#   1 — at least one match.
#   2 — usage / internal error.

set -euo pipefail

usage() {
    cat <<USAGE >&2
usage: $0 [path...]

Scan the given paths (default: current directory) for *_test.go files
containing function names with banned suffixes (NoOp, NoPanic, DoesNotPanic,
Smoke). Honors // lint:test-name-allow on the line directly above the
function definition.
USAGE
    exit 2
}

if [[ $# -eq 1 && ( "$1" == "-h" || "$1" == "--help" ) ]]; then
    usage
fi

# Default to current directory if no paths given.
roots=("$@")
if [[ ${#roots[@]} -eq 0 ]]; then
    roots=(".")
fi

# Collect every *_test.go path under the requested roots, excluding common
# vendored / generated trees. Print null-separated so spaces in paths survive.
files=()
while IFS= read -r -d '' f; do
    files+=("$f")
done < <(
    find "${roots[@]}" \
        -type d \( -name vendor -o -name node_modules -o -name ".git" -o -name ".wrangler" \) -prune \
        -o -type f -name "*_test.go" -print0
)

if [[ ${#files[@]} -eq 0 ]]; then
    # Nothing to scan — vacuously clean.
    exit 0
fi

# awk does the heavy lifting in a single pass per file. The prev variable
# holds the previous line so we can check for the override annotation
# directly above each match. NR resets per file (FILENAME changes), but we
# track prev independently so the file boundary doesn't leak.
awk '
    FNR == 1 { prev = "" }
    /^func Test[A-Za-z0-9_]*_(NoOp|NoPanic|DoesNotPanic|Smoke)([^a-z0-9]|$)/ {
        if (prev !~ /lint:test-name-allow/) {
            printf "%s:%d: banned suffix in test name: %s\n", FILENAME, FNR, $0 > "/dev/stderr"
            failures++
        }
    }
    { prev = $0 }
    END {
        if (failures > 0) {
            printf "\nlint-test-names: %d banned test name(s) found.\n", failures > "/dev/stderr"
            print "Test names ending in _NoOp / _NoPanic / _DoesNotPanic / _Smoke admit the test only verifies absence-of-crash." > "/dev/stderr"
            print "Either add a real assertion, or rename the test to describe the contract it verifies." > "/dev/stderr"
            print "If the test legitimately verifies a no-op contract (with assertions), add the override:" > "/dev/stderr"
            print "  // lint:test-name-allow <short reason>" > "/dev/stderr"
            print "directly above the function definition. Use sparingly." > "/dev/stderr"
            exit 1
        }
    }
' "${files[@]}"
