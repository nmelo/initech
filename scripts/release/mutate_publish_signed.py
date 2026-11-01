#!/usr/bin/env python3
"""Mutation table for publish_signed.sh: each mutant must apply exactly once, must
still parse, and must turn test_publish_signed.py red. A survivor, or a mutant
that does not apply or parse, fails the run; a run that prints nothing is a
finding, not a pass."""
import os, re, subprocess, sys, tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = open(os.path.join(HERE, 'publish_signed.sh')).read()
M = [
    ('a published release accepted', '[ "$draft" = true ] || hold', '[ -n "$draft" ] || hold'),
    ('formula-only on a draft', '[ "$draft" = false ] || hold', 'true || hold'),
    ('checksum mismatch accepted', '[ "$want" = "$got" ] || hold "$n does not match its checksums.txt line"', 'true'),
    ('missing darwin asset accepted', '[ -f "$STAGE/in/$n" ] || hold "$n is in checksums.txt and not in the release"', 'true'),
    ('unsigned accepted', '"$CODESIGN" -dvv "$b" >"$STAGE/dvv" 2>&1 || hold "$n: the binary is not signed"', '"$CODESIGN" -dvv "$b" >"$STAGE/dvv" 2>&1 || true'),
    ('verify failure ignored', '"$CODESIGN" --verify --strict --deep "$b" >"$STAGE/verify" 2>&1 || hold', '"$CODESIGN" --verify --strict --deep "$b" >"$STAGE/verify" 2>&1 || true || hold'),
    ('verify not deep', '--verify --strict --deep "$b"', '--verify --strict "$b"'),
    ('identifier not compared', 'grep -qx "Identifier=$IDENTIFIER" "$STAGE/dvv" || hold', 'true || hold'),
    ('team not compared', 'grep -qx "TeamIdentifier=$TEAM" "$STAGE/dvv" || hold', 'true || hold'),
    ('a failed release run accepted', '[ "${CONC:-}" = success ]', 'true'),
    ('no artifact accepted', '-D "$STAGE/formula" >/dev/null 2>&1 \\\n  || hold "release run $RID left no homebrew-formula artifact"', '-D "$STAGE/formula" >/dev/null 2>&1 \\\n  || true'),
    ('formula version not checked', "    print(f'formula: no version \"{ver}\" line'); sys.exit(1)", "    pass"),
    ('a darwin line named twice accepted', "if len(hits) != 1:", "if len(hits) == 0:"),
    ('linux checksum not compared', "    elif m[2] != want:", "    elif False:"),
    ('darwin checksum not rewritten', "        lines[i] = f'{m[1]}sha256 \"{want}\"'", "        pass"),
    ('publish skipped', '  "$GH" release edit "$TAG" --repo "$REPO" --draft=false --latest >/dev/null 2>&1 \\\n    || hold', '  true \\\n    || hold'),
    ('not marked latest', '--draft=false --latest', '--draft=false'),
    ('publish failure ignored', '|| hold "the publish call failed; the release is a draft still"', '|| true'),
    ('still-draft not checked', '|| hold "the release still reads as a draft after the publish call"', '|| true'),
    ('formula pushed before publish', '# Publish, then the formula.\nif [ "$ONLY" = 0 ]; then', '# Publish, then the formula.\nif false; then'),
    ('push failure reported as done', '  && git -C "$STAGE/tap" push -q origin HEAD 2>/dev/null \\\n  || {', '  && git -C "$STAGE/tap" push -q origin HEAD 2>/dev/null \\\n  || true || {'),
    ('staging kept', "trap 'rm -rf \"${STAGE:?}\"' EXIT\n", ''),
]

bad = 0
for name, old, new in M:
    n = SRC.count(old)
    if n != 1:
        print(f'NOT APPLIED {name}: pattern found {n} times'); bad += 1; continue
    with tempfile.NamedTemporaryFile('w', suffix='.sh', delete=False) as f:
        f.write(SRC.replace(old, new))
    if subprocess.run(['bash', '-n', f.name], capture_output=True).returncode != 0:
        print(f'DOES NOT PARSE {name}'); bad += 1; os.unlink(f.name); continue
    r = subprocess.run([sys.executable, '-W', 'ignore', os.path.join(HERE, 'test_publish_signed.py')],
                       env=dict(os.environ, PUBLISH_SCRIPT=f.name), capture_output=True, text=True)
    red = re.findall(r'^(?:FAIL|ERROR): (\w+)', r.stderr, re.M)
    os.unlink(f.name)
    print(f"{'KILLED ' if red else 'SURVIVED'} {name}: {', '.join(red[:3])}", flush=True)
    bad += not red
print(f'{len(M)} mutants, {bad} survived or did not apply')
sys.exit(1 if bad else 0)
