#!/usr/bin/env bash
# Self-test for scripts/lint-test-names.sh.
#
# Drives the lint with a fixture tree containing each interesting case:
#   - clean test name -> must pass (exit 0)
#   - banned suffix without override -> must fail (exit 1) and stderr names the file
#   - banned suffix WITH override on the line above -> must pass
#   - banned suffix with override above a blank line -> must fail (override locality)
#   - non-test files containing the suffix in unrelated text -> must pass
#
# Run with: bash scripts/lint-test-names_test.sh
#
# Bash test harness rather than Go because the production script is bash;
# mismatched toolchains would mask environmental issues. ini-ybe.1.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT="$SCRIPT_DIR/lint-test-names.sh"

if [[ ! -x "$LINT" ]]; then
    echo "FAIL: $LINT not found or not executable" >&2
    exit 2
fi

PASS=0
FAIL=0

run_case() {
    local name="$1" want_exit="$2" fixture_setup="$3" want_stderr_substring="${4:-}"

    local tmpdir
    tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' RETURN

    # Run fixture setup in subshell so its cwd doesn't leak.
    (
        cd "$tmpdir"
        eval "$fixture_setup"
    )

    local stderr_file="$tmpdir/stderr"
    local got_exit=0
    bash "$LINT" "$tmpdir" 2>"$stderr_file" || got_exit=$?

    local stderr
    stderr="$(cat "$stderr_file")"

    if [[ "$got_exit" -ne "$want_exit" ]]; then
        echo "FAIL [$name]: want exit $want_exit, got $got_exit" >&2
        echo "  stderr: $stderr" >&2
        FAIL=$((FAIL + 1))
        return
    fi

    if [[ -n "$want_stderr_substring" ]] && ! grep -qF "$want_stderr_substring" <<<"$stderr"; then
        echo "FAIL [$name]: stderr missing substring %q" >&2
        echo "  want substring: $want_stderr_substring" >&2
        echo "  stderr: $stderr" >&2
        FAIL=$((FAIL + 1))
        return
    fi

    echo "PASS [$name]"
    PASS=$((PASS + 1))
}

# Case 1: clean test name -> exit 0.
run_case "clean name passes" 0 '
cat >clean_test.go <<EOF
package x

import "testing"

func TestSomething_RealContract(t *testing.T) {
    t.Log("ok")
}
EOF
'

# Case 2: banned suffix without override -> exit 1, stderr names the file.
run_case "banned _NoPanic without override fails" 1 '
cat >banned_test.go <<EOF
package x

import "testing"

func TestThing_NoPanic(t *testing.T) {
    _ = 1
}
EOF
' "banned suffix in test name"

# Case 3: banned suffix WITH override directly above -> exit 0.
run_case "banned _NoOp with override passes" 0 '
cat >ok_test.go <<EOF
package x

import "testing"

// lint:test-name-allow legitimate-no-op
func TestThing_NoOp(t *testing.T) {
    _ = 1
}
EOF
'

# Case 4: override above a blank line -> override does NOT apply.
run_case "override separated by blank line still fails" 1 '
cat >stale_override_test.go <<EOF
package x

import "testing"

// lint:test-name-allow stale

func TestThing_DoesNotPanic(t *testing.T) {
    _ = 1
}
EOF
' "banned suffix in test name"

# Case 5: banned word in non-test file is ignored (lint scope is *_test.go).
run_case "non-test file with banned word ignored" 0 '
cat >prod.go <<EOF
package x

// TestThing_NoPanic is not a test function (this file is not _test.go).
var _ = "TestThing_NoPanic"
EOF
'

# Case 6: each banned suffix is caught.
for suffix in NoOp NoPanic DoesNotPanic Smoke; do
    run_case "_$suffix is caught" 1 "
cat >${suffix}_test.go <<EOF
package x

import \"testing\"

func TestThing_${suffix}(t *testing.T) { _ = 1 }
EOF
" "banned suffix in test name"
done

# Case 7: suffix-with-words-after still matches (e.g. _NoPanicOnNarrow).
run_case "suffix prefix of compound still matches" 1 '
cat >compound_test.go <<EOF
package x

import "testing"

func TestThing_NoPanicOnNarrowTerminal(t *testing.T) { _ = 1 }
EOF
' "banned suffix in test name"

# Case 8: suffix that is a prefix of a longer word does NOT match
# (e.g. _NoPanicky should NOT match — "Panicky" is a different word).
# The regex boundary [^A-Za-z0-9_]|$ enforces this.
run_case "non-boundary longer word does not match" 0 '
cat >prefix_test.go <<EOF
package x

import "testing"

func TestThing_NoPanicky(t *testing.T) { _ = 1 }
EOF
'

# Case 9: empty tree -> exit 0 (vacuously clean).
run_case "empty tree passes" 0 '
mkdir -p subdir
'

echo
echo "=========================================="
echo "lint-test-names self-test: $PASS passed, $FAIL failed"
echo "=========================================="

if [[ "$FAIL" -gt 0 ]]; then
    exit 1
fi
