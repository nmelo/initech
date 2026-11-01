#!/usr/bin/env python3
"""sign_notarize.sh: signs and notarises the darwin binaries of a draft release,
then replaces them in the draft. Every case runs with fake gh, security, codesign
and notarytool and a temp folder: no network, no keychain, no Apple service."""
import hashlib, json, os, re, shutil, stat, subprocess, sys, tarfile, tempfile, unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.environ.get('SIGN_SCRIPT', os.path.join(HERE, 'sign_notarize.sh'))
TAG = 'v9.9.9'
IDENT = 'com.neblihq.initech'
TEAM = 'K7Z8YKVA8G'
SHA1 = 'E73758A491D91D088ADB8BC50B04CCB49E0351A4'
KEY_BYTES = 'FIXTURE-KEY-7d1e0c9a-NEVER-PRINT'
KEY_ID = 'KEYID9X7Q2'
ISSUER = '0f1e2d3c-issuer-fixture'
DARWIN = ('initech_darwin_amd64.tar.gz', 'initech_darwin_arm64.tar.gz')
OTHER = ('initech_linux_amd64.tar.gz', 'initech_linux_arm64.tar.gz', 'initech_windows_amd64.zip')

FAKE_GH = r'''#!/bin/bash
echo "gh $*" >> "$H/calls"
case "$1 $2" in
  "release view") cat "$H/is_draft";;
  "release download")
    shift 2; dir=; pats=()
    while [ $# -gt 0 ]; do
      case "$1" in --dir) dir=$2; shift;; --pattern) pats+=("$2"); shift;; --repo) shift;; esac; shift
    done
    for p in "${pats[@]}"; do for f in "$H"/assets/$p; do [ -f "$f" ] && cp "$f" "$dir/"; done; done;;
  "release upload")
    [ -f "$H/upload_fails" ] && exit 1
    shift 3
    for f in "$@"; do case "$f" in --*) ;; *) [ -f "$f" ] && cp "$f" "$H/assets/";; esac; done;;
  "release edit") ;;
esac
'''

FAKE_SECURITY = r'''#!/bin/bash
echo "security $*" >> "$H/calls"
cat "$H/identities"
'''

# The fake codesign signs by appending a marker; -dvv reads the marker back.
FAKE_CODESIGN = r'''#!/bin/bash
echo "codesign $*" >> "$H/calls"
if [ "$1" = -dvv ]; then
  f=$2; m=$(grep -a '^SIGNED ' "$f" | tail -1)
  [ -n "$m" ] || { echo "$f: code object is not signed at all" >&2; exit 1; }
  ident=$(sed -n 's/.* -i \([^ ]*\).*/\1/p' <<<"$m"); team=$(cat "$H/team")
  [ -f "$H/readback_ident" ] && ident=$(cat "$H/readback_ident")
  {
    echo "Executable=$f"; echo "Identifier=$ident"; echo "Format=Mach-O thin (arm64)"
    flags=0x0; grep -q -- '-o runtime' <<<"$m" && [ ! -f "$H/readback_no_runtime" ] && flags='0x10000(runtime)'
    echo "CodeDirectory v=20500 size=1 flags=$flags hashes=1+7 location=embedded"
    echo "Authority=Developer ID Application: Fixture ($team)"
    grep -q -- '--timestamp' <<<"$m" && [ ! -f "$H/readback_no_timestamp" ] && echo "Timestamp=1 Nov 2026 at 08:00:00"
    echo "TeamIdentifier=$team"
  } >&2
  exit 0
fi
[ -f "$H/codesign_hangs" ] && { echo $$ > "$H/codesign_pid"; exec sleep 86399; }
[ -f "$H/codesign_fails" ] && { echo "errSecInternalComponent" >&2; exit 1; }
f=${@: -1}
printf '\nSIGNED %s\n' "$*" >> "$f"
'''

# The fake notarytool echoes every argument, so a key printed by the script
# would be visible; the script must never pass its output through.
FAKE_NOTARYTOOL = r'''#!/bin/bash
echo "notarytool $*" >> "$H/calls"
echo "notarytool args: $*"
echo "notarytool args: $*" >&2
case "$1" in
  submit)
    [ -f "$H/notary_hangs" ] && { echo $$ > "$H/notary_pid"; exec sleep 86399; }
    printf '{"id": "fixture-sub-1", "status": "%s", "message": "done"}\n' "$(cat "$H/notary_status")";;
  log) echo '{"issues": [{"message": "fixture issue"}]}';;
esac
'''


def sha256(path):
    with open(path, 'rb') as f:
        return hashlib.sha256(f.read()).hexdigest()


class Rig:
    def __init__(self):
        self.h = os.path.realpath(tempfile.mkdtemp(prefix='signtest', dir=os.environ.get('TMPDIR')))
        self.assets = os.path.join(self.h, 'assets'); os.makedirs(self.assets)
        src = os.path.join(self.h, 'src'); os.makedirs(src)
        for n in ('LICENSE', 'README.md'):
            open(os.path.join(src, n), 'w').write(n)
        lines = []
        for name in DARWIN + OTHER:
            b = os.path.join(src, 'initech'); open(b, 'w').write(f'#!/bin/bash\necho "initech 9.9.9 {name}"\n')
            os.chmod(b, 0o755)
            p = os.path.join(self.assets, name)
            if name.endswith('.zip'):
                open(p, 'w').write('zip ' + name)
            else:
                with tarfile.open(p, 'w:gz') as t:
                    for n in ('initech', 'LICENSE', 'README.md'):
                        t.add(os.path.join(src, n), arcname=n)
            lines.append(f'{sha256(p)}  {name}')
        open(os.path.join(self.assets, 'checksums.txt'), 'w').write('\n'.join(lines) + '\n')
        self.before = {n: sha256(os.path.join(self.assets, n)) for n in os.listdir(self.assets)}
        fb = os.path.join(self.h, 'fakebin'); os.makedirs(fb)
        for name, body in (('gh', FAKE_GH), ('security', FAKE_SECURITY), ('codesign', FAKE_CODESIGN),
                           ('notarytool', FAKE_NOTARYTOOL)):
            open(os.path.join(fb, name), 'w').write(body); os.chmod(os.path.join(fb, name), 0o755)
        open(os.path.join(self.h, 'is_draft'), 'w').write('true\n')
        open(os.path.join(self.h, 'team'), 'w').write(TEAM)
        open(os.path.join(self.h, 'notary_status'), 'w').write('Accepted')
        open(os.path.join(self.h, 'identities'), 'w').write(
            f'  1) 197FDF3E9AD3E83EA6C6570A1307AC4CB50A9E39 "Developer ID Application: Fixture ({TEAM})"\n'
            f'  2) {SHA1} "Developer ID Application: Fixture ({TEAM})"\n'
            '     2 valid identities found\n')
        self.key = os.path.join(self.h, 'AuthKey_fixture.p8')
        open(self.key, 'w').write(KEY_BYTES + '\n'); os.chmod(self.key, 0o600)
        self.config = os.path.join(self.h, 'initech.yaml')
        open(self.config, 'w').write(
            'project: fixture\napp_store_connect:\n'
            f'  key_path: {self.key}\n  key_id: {KEY_ID}\n  issuer_id: {ISSUER}\n  team_id: {TEAM}\n')
        self.env = dict(os.environ, H=self.h, PATH=fb + ':' + os.environ['PATH'],
                        NOTARYTOOL=os.path.join(fb, 'notarytool'), NOTARY_LIMIT='5')

    def run(self, *extra, timeout=60):
        return subprocess.run(['bash', SCRIPT, TAG, '--config', self.config, *extra], env=self.env,
                              capture_output=True, text=True, timeout=timeout, stdin=subprocess.DEVNULL)

    def flag(self, name, text=''):
        open(os.path.join(self.h, name), 'w').write(text)

    def calls(self):
        p = os.path.join(self.h, 'calls')
        return open(p).read().splitlines() if os.path.exists(p) else []

    def now(self):
        return {n: sha256(os.path.join(self.assets, n)) for n in os.listdir(self.assets)}

    def close(self):
        # A hung fake outlives a script killed at a test's timeout (a mutant that
        # drops a bound): stop it by the PID it wrote, never by name.
        for f in ('notary_pid', 'codesign_pid'):
            p = os.path.join(self.h, f)
            if os.path.exists(p):
                try:
                    os.kill(int(open(p).read()), 9)
                except (OSError, ValueError):
                    pass
        shutil.rmtree(self.h, ignore_errors=True)


class Case(unittest.TestCase):
    def rig(self):
        r = Rig(); self.addCleanup(r.close); return r

    def assert_nothing_uploaded(self, r, out):
        self.assertEqual(out.returncode, 1, out.stdout + out.stderr)
        self.assertFalse([c for c in r.calls() if c.startswith('gh release upload')], 'nothing uploaded')
        self.assertFalse([c for c in r.calls() if c.startswith('gh release edit')], 'nothing published')
        self.assertEqual(r.now(), r.before, 'the draft is unchanged')


class Signs(Case):
    def test_signs_notarises_and_replaces_the_darwin_assets(self):
        r = self.rig(); out = r.run()
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        now = r.now()
        for n in DARWIN:
            self.assertNotEqual(now[n], r.before[n], f'{n} replaced')
        for n in OTHER:
            self.assertEqual(now[n], r.before[n], f'{n} untouched')
        lines = dict(reversed(l.split()) for l in open(os.path.join(r.assets, 'checksums.txt')).read().split('\n') if l)
        self.assertEqual(set(lines), set(DARWIN + OTHER))
        for n in DARWIN + OTHER:
            self.assertEqual(lines[n], now[n], f'checksums.txt describes the published {n}')
        self.assertIn(f'Identifier={IDENT}', out.stdout); self.assertIn(f'TeamIdentifier={TEAM}', out.stdout)
        self.assertIn('Accepted', out.stdout); self.assertIn('fixture-sub-1', out.stdout)

    def test_codesign_gets_the_identity_identifier_runtime_and_timestamp(self):
        r = self.rig(); r.run()
        signs = [c for c in r.calls() if c.startswith('codesign -f')]
        self.assertEqual(len(signs), len(DARWIN))
        for c in signs:
            for part in (f'-s {SHA1}', f'-i {IDENT}', '-o runtime', '--timestamp'):
                self.assertIn(part, c)

    def test_the_repacked_tarball_holds_the_signed_binary_and_the_same_files(self):
        r = self.rig(); r.run()
        for n in DARWIN:
            with tarfile.open(os.path.join(r.assets, n)) as t:
                self.assertEqual(sorted(t.getnames()), ['LICENSE', 'README.md', 'initech'])
                self.assertIn(b'SIGNED ', t.extractfile('initech').read())
                self.assertTrue(t.getmember('initech').mode & stat.S_IXUSR, 'still executable')

    def test_order_sign_then_notarise_then_upload_and_never_publish(self):
        r = self.rig(); r.run(); c = r.calls()
        idx = lambda pfx: [i for i, x in enumerate(c) if x.startswith(pfx)]
        self.assertTrue(max(idx('codesign -f')) < min(idx('notarytool submit')) < min(idx('gh release upload')))
        self.assertTrue(max(idx('codesign -dvv')) < min(idx('notarytool submit')), 'read back before notarising')
        self.assertEqual(idx('gh release edit'), [], 'the signing step never publishes')

    def test_notarytool_gets_the_key_by_path(self):
        r = self.rig(); r.run()
        sub = [c for c in r.calls() if c.startswith('notarytool submit')][0]
        self.assertIn(f'--key {r.key}', sub); self.assertIn('--wait', sub)


class Secrets(Case):
    def test_no_key_bytes_and_no_key_ids_in_any_output(self):
        r = self.rig()
        for setup in (lambda: None, lambda: r.flag('notary_status', 'Invalid')):
            setup(); out = r.run()
            for secret in (KEY_BYTES, KEY_ID, ISSUER):
                self.assertNotIn(secret, out.stdout); self.assertNotIn(secret, out.stderr)

    def test_a_key_file_not_0600_is_refused(self):
        r = self.rig(); os.chmod(r.key, 0o644)
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('mode', out.stdout)
        self.assertEqual([c for c in r.calls() if c.startswith(('codesign', 'notarytool'))], [])

    def test_a_missing_key_field_is_refused(self):
        r = self.rig(); open(r.config, 'w').write('app_store_connect:\n  key_id: x\n')
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('app_store_connect', out.stdout)


class Config(Case):
    def test_runs_with_no_third_party_module(self):
        # A release runner's python has the standard library only.
        r = self.rig(); nb = os.path.join(r.h, 'nosite'); os.makedirs(nb)
        p = os.path.join(nb, 'python3')
        open(p, 'w').write(f'#!/bin/bash\nexec {shutil.which("python3")} -S "$@"\n'); os.chmod(p, 0o755)
        r.env['PATH'] = nb + ':' + r.env['PATH']
        out = r.run(); self.assertEqual(out.returncode, 0, out.stdout + out.stderr)

    def test_reads_the_block_among_others_with_quotes_and_comments(self):
        r = self.rig()
        open(r.config, 'w').write(
            '# fixture\nproject: fixture\nroles: [a, b]\napp_store_connect:\n'
            f'  # the notarising key\n  key_path: "{r.key}"\n  key_id: \'{KEY_ID}\'  # id\n'
            f'  issuer_id: {ISSUER}\nother:\n  key_path: /nowhere\n')
        out = r.run(); self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        sub = [c for c in r.calls() if c.startswith('notarytool submit')][0]
        self.assertIn(f'--key {r.key} --key-id {KEY_ID} --issuer {ISSUER}', sub)

    def test_a_key_outside_the_block_is_not_read(self):
        r = self.rig()
        open(r.config, 'w').write(f'key_path: {r.key}\nkey_id: {KEY_ID}\nissuer_id: {ISSUER}\napp_store_connect:\n  team_id: {TEAM}\n')
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('app_store_connect lacks', out.stdout)


class Refuses(Case):
    def test_a_published_release(self):
        r = self.rig(); r.flag('is_draft', 'false\n')
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('is published', out.stdout)
        self.assertEqual([c for c in r.calls() if c.startswith(('codesign', 'notarytool'))], [])

    def test_a_download_that_does_not_match_its_checksum(self):
        r = self.rig()
        with open(os.path.join(r.assets, DARWIN[1]), 'ab') as f:
            f.write(b'tampered')
        r.before = r.now()
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('does not match', out.stdout)

    def test_a_darwin_asset_missing(self):
        r = self.rig(); os.remove(os.path.join(r.assets, DARWIN[0])); r.before = r.now()
        out = r.run(); self.assert_nothing_uploaded(r, out)
        self.assertIn(f'{DARWIN[0]} is in checksums.txt and not in the draft', out.stdout)

    def test_no_signing_identity(self):
        r = self.rig(); r.flag('identities', '     0 valid identities found\n')
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('identity', out.stdout)

    def test_codesign_failing(self):
        r = self.rig(); r.flag('codesign_fails')
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('codesign failed', out.stdout)

    def test_a_read_back_identifier_that_is_not_the_constant(self):
        r = self.rig(); r.flag('readback_ident', 'a.out')
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('Identifier', out.stdout)
        self.assertFalse([c for c in r.calls() if c.startswith('notarytool')], 'refused before notarising')

    def test_a_read_back_team_that_is_not_the_constant(self):
        r = self.rig(); r.flag('team', 'ZZZZZZZZZZ')
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('TeamIdentifier', out.stdout)

    def test_a_read_back_without_the_hardened_runtime(self):
        r = self.rig(); r.flag('readback_no_runtime')
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('hardened runtime', out.stdout)

    def test_a_read_back_without_a_timestamp(self):
        r = self.rig(); r.flag('readback_no_timestamp')
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('secure timestamp', out.stdout)

    def test_codesign_past_its_bound_is_stopped(self):
        r = self.rig(); r.flag('codesign_hangs'); r.env['CODESIGN_LIMIT'] = '2'
        out = r.run(timeout=40); self.assert_nothing_uploaded(r, out)
        self.assertIn('keychain prompt', out.stdout)
        self.assertEqual([c for c in r.calls() if c.startswith('notarytool')], [])
        pid = int(open(os.path.join(r.h, 'codesign_pid')).read())
        state = subprocess.run(['ps', '-o', 'stat=', '-p', str(pid)], capture_output=True, text=True).stdout.strip()
        self.assertIn(state[:1], ('', 'Z'), 'the hung codesign is stopped')

    def test_notarytool_invalid_fetches_the_log(self):
        r = self.rig(); r.flag('notary_status', 'Invalid')
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('Invalid', out.stdout)
        self.assertTrue([c for c in r.calls() if c.startswith('notarytool log fixture-sub-1')])
        m = re.search(r'notarytool log saved to (\S+)', out.stdout)
        self.assertTrue(m and os.path.exists(m[1]), out.stdout)
        self.assertIn('fixture issue', open(m[1]).read())
        self.addCleanup(lambda: os.path.exists(m[1]) and os.remove(m[1]))

    def test_notarytool_in_progress_is_not_accepted(self):
        r = self.rig(); r.flag('notary_status', 'In Progress')
        out = r.run(); self.assert_nothing_uploaded(r, out)

    def test_notarytool_past_the_bound(self):
        r = self.rig(); r.flag('notary_hangs')
        out = r.run(timeout=40); self.assert_nothing_uploaded(r, out); self.assertIn('bound', out.stdout)

    def test_an_upload_that_fails_says_so(self):
        r = self.rig(); r.flag('upload_fails')
        out = r.run(); self.assertEqual(out.returncode, 1); self.assertIn('upload', out.stdout)

    def test_a_repacked_file_that_does_not_match_the_new_checksums(self):
        # REPACK_HOOK runs after the re-pack and the new checksums file: a change there must refuse.
        r = self.rig(); r.env['REPACK_HOOK'] = f'echo x >> "$STAGE/out/{DARWIN[0]}"'
        out = r.run(); self.assert_nothing_uploaded(r, out); self.assertIn('re-packed', out.stdout)

    def test_the_staging_folder_is_removed(self):
        r = self.rig(); tmp = tempfile.mkdtemp(dir=r.h); r.env['TMPDIR'] = tmp
        r.run(); self.assertEqual(os.listdir(tmp), [])
        r.flag('codesign_fails'); r.run(); self.assertEqual(os.listdir(tmp), [])


if __name__ == '__main__':
    unittest.main(verbosity=1)
