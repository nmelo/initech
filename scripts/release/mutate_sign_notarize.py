#!/usr/bin/env python3
"""Mutation table for sign_notarize.sh: each mutant must apply exactly once, must
still parse, and must turn test_sign_notarize.py red. A survivor is printed as
SURVIVED and fails the run; a run that prints nothing is a finding, not a pass."""
import os, re, subprocess, sys, tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = open(os.path.join(HERE, 'sign_notarize.sh')).read()
M = [
    # AC5, as the bead lists it
    ('identifier flag dropped', ' -i "$IDENTIFIER" -o runtime', ' -o runtime'),
    ('hardened runtime dropped', ' -o runtime --timestamp', ' --timestamp'),
    ('timestamp dropped', '-o runtime --timestamp "$d/initech"', '-o runtime "$d/initech"'),
    ('read-back skipped', 'info=$("$CODESIGN" -dvv "$d/initech" 2>&1) || refuse "codesign could not read back $n"',
     'info="Identifier=$IDENTIFIER\nTeamIdentifier=$TEAM\nflags=0x10000(runtime)\nTimestamp=x"'),
    ('any notarytool status accepted', 'if [ "$STATUS" != Accepted ]; then', 'if false; then'),
    ('published before notarisation', '# 5. Notarise both binaries', '"$GH" release edit "$TAG" --repo "$REPO" --draft=false >/dev/null 2>&1\n# 5. Notarise both binaries'),
    ('key printed', 'unset CREDS\n', 'unset CREDS\ncat "$KEY_PATH"\n'),
    ('key ids printed', 'unset CREDS\n', 'unset CREDS\necho "using $KEY_ID $ISSUER"\n'),
    ('a third-party module imported', 'import os, stat, sys\n', 'import os, stat, sys\nimport yaml\n'),
    ('reader crash ignored', ') || refuse "the config reader failed (python exited non-zero); nothing signed"', ')'),
    ('empty credentials accepted', '[ -n "$KEY_PATH" ] && [ -n "$KEY_ID" ] && [ -n "$ISSUER" ] || refuse', 'true || refuse'),
    ('a key outside the block read', "            inside = line.split('#', 1)[0].strip() == 'app_store_connect:'\n", "            inside = True\n"),
    ('quotes kept', '                v = v[1:-1]\n', '                pass\n'),
    ('0644 key accepted', "if mode != 0o600:", "if mode not in (0o600, 0o644):"),
    # AC4, each refusal
    ('a published release signed', '[ "$draft" = true ] || refuse', '[ -n "$draft" ] || refuse'),
    ('checksum mismatch accepted', '[ "$want" = "$got" ] || refuse "$n does not match its checksums.txt line"', 'true'),
    ('missing darwin asset accepted', '[ -f "$STAGE/in/$n" ] || refuse "$n is in checksums.txt and not in the draft"', 'true'),
    ('identity not checked', '|| refuse "the signing identity $IDENTITY of team $TEAM is not a valid identity in the keychain"', '|| true'),
    ('codesign failure ignored', 'wait "$cp_" || refuse "codesign failed on $n: $(tail -1 "$STAGE/codesign.out")"', 'wait "$cp_" || true'),
    ('codesign bound gone', 'for ((t=0; t<CODESIGN_LIMIT*5; t++)); do kill -0 "$cp_" 2>/dev/null || break; sleep 0.2; done\n  if kill -0 "$cp_" 2>/dev/null; then', 'wait "$cp_"\n  if false; then'),
    ('identifier not compared', 'grep -qx "Identifier=$IDENTIFIER" <<<"$info" || refuse', 'true || refuse'),
    ('team not compared', 'grep -qx "TeamIdentifier=$TEAM" <<<"$info" || refuse', 'true || refuse'),
    ('runtime not compared', "grep -q 'flags=0x[0-9a-f]*(.*runtime' <<<\"$info\" || refuse", 'true || refuse'),
    ('timestamp not compared', "grep -q '^Timestamp=' <<<\"$info\" || refuse", 'true || refuse'),
    ('notary bound gone', 'for ((t=0; t<(NOTARY_LIMIT+30)*5; t++)); do kill -0 "$np" 2>/dev/null || break; sleep 0.2; done\nif kill -0 "$np" 2>/dev/null; then',
     'wait "$np"\nif false; then'),
    ('notary log not fetched', '"${NOTARYTOOL[@]}" log "$SUB"', 'true "$SUB"'),
    ('re-pack mismatch accepted', '[ "$want" = "$got" ] || refuse "the re-packed $n does not match the new checksums.txt"', 'true'),
    ('re-pack adds metadata files', 'COPYFILE_DISABLE=1 tar czf "$STAGE/out/$n" --no-mac-metadata --no-xattrs -C', 'tar czf "$STAGE/out/$n" -C'),
    ('upload failure reported as success', '|| { echo "FAILED: the upload to the draft did not complete', '|| true || { echo "FAILED: the upload to the draft did not complete'),
    ('staging kept', "trap 'rm -rf \"${STAGE:?}\"' EXIT\n", ''),
]

survived = 0
for name, old, new in M:
    n = SRC.count(old)
    if n != 1:
        print(f'NOT APPLIED {name}: pattern found {n} times'); survived += 1; continue
    with tempfile.NamedTemporaryFile('w', suffix='.sh', delete=False) as f:
        f.write(SRC.replace(old, new))
    if subprocess.run(['bash', '-n', f.name], capture_output=True).returncode != 0:
        print(f'DOES NOT PARSE {name}'); survived += 1; os.unlink(f.name); continue
    r = subprocess.run([sys.executable, '-W', 'ignore', os.path.join(HERE, 'test_sign_notarize.py')],
                       env=dict(os.environ, SIGN_SCRIPT=f.name), capture_output=True, text=True)
    red = re.findall(r'^(?:FAIL|ERROR): (\w+)', r.stderr, re.M)
    os.unlink(f.name)
    print(f"{'KILLED ' if red else 'SURVIVED'} {name}: {', '.join(red[:3])}", flush=True)
    survived += not red
print(f'{len(M)} mutants, {survived} survived or did not apply')
sys.exit(1 if survived else 0)
