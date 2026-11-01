#!/usr/bin/env python3
"""publish_signed.sh: publishes a draft release only once its darwin binaries are
signed, then pushes the tap formula with the signed checksums. Every case runs
with fake gh and codesign, a temp folder and a local bare git repository as the
tap: no network, no keychain, no Apple service."""
import hashlib, os, re, shutil, subprocess, sys, tarfile, tempfile, unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.environ.get('PUBLISH_SCRIPT', os.path.join(HERE, 'publish_signed.sh'))
TAG, VER = 'v9.9.9', '9.9.9'
IDENT, TEAM = 'com.neblihq.initech', 'K7Z8YKVA8G'
DARWIN = ('initech_darwin_amd64.tar.gz', 'initech_darwin_arm64.tar.gz')
LINUX = ('initech_linux_amd64.tar.gz', 'initech_linux_arm64.tar.gz')

FAKE_GH = r'''#!/bin/bash
echo "gh $*" >> "$H/calls"
case "$1 $2" in
  "release view")
    case "$*" in *isDraft*) cat "$H/is_draft";; *) echo "{}";; esac;;
  "release download")
    shift 2; dir=; pats=()
    while [ $# -gt 0 ]; do case "$1" in --dir) dir=$2; shift;; --pattern) pats+=("$2"); shift;; --repo) shift;; esac; shift; done
    for p in "${pats[@]}"; do for f in "$H"/assets/$p; do [ -f "$f" ] && cp "$f" "$dir/"; done; done;;
  "release edit")
    [ -f "$H/edit_fails" ] && exit 1
    [ -f "$H/edit_noop" ] && exit 0
    case "$*" in *--draft=false*) echo false > "$H/is_draft";; esac;;
  "run list") cat "$H/runs";;
  "run download")
    [ -f "$H/no_artifact" ] && exit 1
    shift 2; dir=
    while [ $# -gt 0 ]; do case "$1" in -D|--dir) dir=$2; shift;; esac; shift; done
    mkdir -p "$dir"; cp "$H/formula.rb" "$dir/initech.rb";;
esac
'''

FAKE_CODESIGN = r'''#!/bin/bash
echo "codesign $*" >> "$H/calls"
f=${@: -1}
grep -aq '^SIGNED' "$f" || { echo "$f: code object is not signed at all" >&2; exit 1; }
case "$1" in
  --verify) [ -f "$H/verify_fails" ] && { echo "invalid signature" >&2; exit 1; }; exit 0;;
  -dvv) { echo "Identifier=$(cat "$H/ident")"; echo "TeamIdentifier=$TEAM_FIXTURE"; } >&2; exit 0;;
esac
'''


def sha(p):
    with open(p, 'rb') as f:
        return hashlib.sha256(f.read()).hexdigest()


def formula(sums, version=VER):
    """A formula shaped as goreleaser writes it, with the given checksums."""
    out = ['class Initech < Formula', f'  version "{version}"', '  on_macos do']
    for arch, name in (('intel', DARWIN[0]), ('arm', DARWIN[1])):
        out += [f'    if Hardware::CPU.{arch}?',
                f'      url "https://github.com/nmelo/initech/releases/download/{TAG}/{name}"',
                f'      sha256 "{sums[name]}"', '    end']
    out += ['  end', '  on_linux do']
    for name in LINUX:
        out += [f'      url "https://github.com/nmelo/initech/releases/download/{TAG}/{name}"',
                f'      sha256 "{sums[name]}"']
    out += ['  end', 'end']
    return '\n'.join(out) + '\n'


class Rig:
    def __init__(self, signed=True):
        self.h = os.path.realpath(tempfile.mkdtemp(prefix='pubtest', dir=os.environ.get('TMPDIR')))
        self.assets = os.path.join(self.h, 'assets'); os.makedirs(self.assets)
        src = os.path.join(self.h, 'src'); os.makedirs(src)
        self.unsigned = {}
        for name in DARWIN + LINUX:
            b = os.path.join(src, 'initech')
            open(b, 'w').write(f'#!/bin/bash\necho "initech {VER}"\n')
            p = os.path.join(self.assets, name)
            with tarfile.open(p, 'w:gz') as t:
                t.add(b, arcname='initech')
            self.unsigned[name] = sha(p)
            if signed and name in DARWIN:
                open(b, 'a').write('\nSIGNED fixture\n')
                with tarfile.open(p, 'w:gz') as t:
                    t.add(b, arcname='initech')
        self.sums = {n: sha(os.path.join(self.assets, n)) for n in DARWIN + LINUX}
        open(os.path.join(self.assets, 'checksums.txt'), 'w').write(
            ''.join(f'{self.sums[n]}  {n}\n' for n in DARWIN + LINUX))
        # CI's formula describes CI's (unsigned) tarballs.
        open(os.path.join(self.h, 'formula.rb'), 'w').write(formula(dict(self.unsigned)))
        # The tap: a bare repository holding the previous formula.
        self.tap = os.path.join(self.h, 'tap.git')
        seed = os.path.join(self.h, 'seed'); os.makedirs(os.path.join(seed, 'Formula'))
        open(os.path.join(seed, 'Formula', 'initech.rb'), 'w').write(formula(self.unsigned, '9.9.8'))
        g = ['git', '-c', 'user.name=t', '-c', 'user.email=t@t', '-C', seed]
        subprocess.run(['git', 'init', '-q', '-b', 'main', seed], check=True)
        subprocess.run(g + ['add', '.'], check=True); subprocess.run(g + ['commit', '-qm', 'seed'], check=True)
        subprocess.run(['git', 'clone', '-q', '--bare', seed, self.tap], check=True)
        # The tap records, at each push, whether the release was published yet.
        hook = os.path.join(self.tap, 'hooks', 'pre-receive')
        open(hook, 'w').write(f'#!/bin/bash\necho "draft=$(cat {self.h}/is_draft)" >> {self.h}/push_log\n')
        os.chmod(hook, 0o755)
        fb = os.path.join(self.h, 'fakebin'); os.makedirs(fb)
        for n, body in (('gh', FAKE_GH), ('codesign', FAKE_CODESIGN)):
            open(os.path.join(fb, n), 'w').write(body); os.chmod(os.path.join(fb, n), 0o755)
        for n, v in (('is_draft', 'true\n'), ('ident', IDENT), ('runs', '4242 success\n')):
            open(os.path.join(self.h, n), 'w').write(v)
        self.tmp = os.path.join(self.h, 'tmp'); os.makedirs(self.tmp)
        self.env = dict(os.environ, H=self.h, TEAM_FIXTURE=TEAM, PATH=fb + ':' + os.environ['PATH'],
                        TAP_URL=self.tap, TMPDIR=self.tmp, GIT_AUTHOR_NAME='t', GIT_AUTHOR_EMAIL='t@t',
                        GIT_COMMITTER_NAME='t', GIT_COMMITTER_EMAIL='t@t')

    def run(self, *extra):
        return subprocess.run(['bash', SCRIPT, TAG, *extra], env=self.env, capture_output=True,
                              text=True, timeout=60, stdin=subprocess.DEVNULL)

    def flag(self, n, v=''):
        open(os.path.join(self.h, n), 'w').write(v)

    def calls(self):
        p = os.path.join(self.h, 'calls')
        return open(p).read().splitlines() if os.path.exists(p) else []

    def tap_formula(self):
        return subprocess.run(['git', '--git-dir', self.tap, 'show', 'main:Formula/initech.rb'],
                              capture_output=True, text=True).stdout

    def tap_commits(self):
        return subprocess.run(['git', '--git-dir', self.tap, 'rev-list', '--count', 'main'],
                              capture_output=True, text=True).stdout.strip()

    def close(self):
        shutil.rmtree(self.h, ignore_errors=True)


class Case(unittest.TestCase):
    def rig(self, **kw):
        r = Rig(**kw); self.addCleanup(r.close); return r

    def assert_held(self, r, out, words):
        self.assertEqual(out.returncode, 1, out.stdout + out.stderr)
        self.assertIn(words, out.stdout)
        self.assertFalse([c for c in r.calls() if '--draft=false' in c], 'not published')
        self.assertEqual(r.tap_commits(), '1', 'the tap is untouched')


class Publishes(Case):
    def test_publishes_then_pushes_the_signed_formula(self):
        r = self.rig(); out = r.run()
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        f = r.tap_formula()
        self.assertIn(f'version "{VER}"', f)
        for n in DARWIN + LINUX:
            self.assertIn(f'sha256 "{r.sums[n]}"', f, f'{n} carries the published checksum')
        for n in DARWIN:
            self.assertNotIn(r.unsigned[n], f, f'the unsigned {n} checksum is gone')

    def test_publish_comes_before_the_formula(self):
        r = self.rig(); out = r.run(); self.assertEqual(out.returncode, 0, out.stdout)
        pub = [c for c in r.calls() if '--draft=false' in c]
        self.assertEqual(len(pub), 1); self.assertIn('--latest', pub[0])
        # The tap saw exactly one push, and the release was already published then.
        self.assertEqual(open(os.path.join(r.h, 'push_log')).read().split(), ['draft=false'])

    def test_verifies_deep_and_reads_back_identity(self):
        r = self.rig(); r.run()
        self.assertEqual(len([c for c in r.calls() if c.startswith('codesign --verify --strict --deep')]), 2)
        self.assertEqual(len([c for c in r.calls() if c.startswith('codesign -dvv')]), 2)

    def test_formula_only_pushes_without_publishing_again(self):
        r = self.rig(); r.flag('is_draft', 'false\n')
        out = r.run('--formula-only'); self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertFalse([c for c in r.calls() if '--draft=false' in c])
        self.assertIn(f'sha256 "{r.sums[DARWIN[0]]}"', r.tap_formula())

    def test_a_second_run_changes_nothing(self):
        r = self.rig(); r.run(); r.flag('is_draft', 'false\n')
        out = r.run('--formula-only'); self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertEqual(r.tap_commits(), '2'); self.assertIn('already', out.stdout)


class Holds(Case):
    def test_an_unsigned_darwin_binary(self):
        r = self.rig(signed=False); out = r.run()
        self.assert_held(r, out, 'the binary is not signed')

    def test_a_signature_that_does_not_verify(self):
        r = self.rig(); r.flag('verify_fails'); out = r.run()
        self.assert_held(r, out, 'does not verify')

    def test_a_wrong_identifier(self):
        r = self.rig(); r.flag('ident', 'a.out'); out = r.run()
        self.assert_held(r, out, 'Identifier')

    def test_a_wrong_team(self):
        r = self.rig(); r.env['TEAM_FIXTURE'] = 'ZZZZZZZZZZ'; out = r.run()
        self.assert_held(r, out, 'TeamIdentifier')

    def test_a_tarball_that_does_not_match_its_checksum(self):
        r = self.rig()
        with open(os.path.join(r.assets, DARWIN[0]), 'ab') as f:
            f.write(b'x')
        out = r.run(); self.assert_held(r, out, 'does not match')

    def test_a_release_already_published(self):
        r = self.rig(); r.flag('is_draft', 'false\n'); out = r.run()
        self.assert_held(r, out, 'already published')

    def test_no_release_run_for_the_tag(self):
        r = self.rig(); r.flag('runs', ''); out = r.run()
        self.assert_held(r, out, 'release run')

    def test_a_release_run_that_failed(self):
        r = self.rig(); r.flag('runs', '4242 failure\n'); out = r.run()
        self.assert_held(r, out, 'release run')

    def test_no_formula_artifact(self):
        r = self.rig(); r.flag('no_artifact'); out = r.run()
        self.assert_held(r, out, 'left no homebrew-formula artifact')

    def test_a_formula_for_another_version(self):
        r = self.rig(); open(os.path.join(r.h, 'formula.rb'), 'w').write(formula(r.unsigned, '9.9.7'))
        out = r.run(); self.assert_held(r, out, 'version')

    def test_a_formula_whose_linux_checksums_disagree(self):
        r = self.rig(); bad = dict(r.unsigned); bad[LINUX[0]] = '0' * 64
        open(os.path.join(r.h, 'formula.rb'), 'w').write(formula(bad))
        out = r.run(); self.assert_held(r, out, LINUX[0])

    def test_a_formula_missing_a_darwin_line(self):
        r = self.rig(); f = formula(r.unsigned).replace(f'/{DARWIN[1]}"', '/other.tar.gz"')
        open(os.path.join(r.h, 'formula.rb'), 'w').write(f)
        out = r.run(); self.assert_held(r, out, DARWIN[1])

    def test_a_publish_that_fails_pushes_no_formula(self):
        r = self.rig(); r.flag('edit_fails'); out = r.run()
        self.assertEqual(out.returncode, 1); self.assertIn('the publish call failed', out.stdout)
        self.assertEqual(r.tap_commits(), '1')

    def test_a_publish_that_leaves_a_draft_pushes_no_formula(self):
        r = self.rig(); r.flag('edit_noop'); out = r.run()
        self.assertEqual(out.returncode, 1); self.assertIn('still reads as a draft', out.stdout)
        self.assertEqual(r.tap_commits(), '1')

    def test_a_clone_that_fails_says_how_to_finish(self):
        r = self.rig(); r.env['TAP_URL'] = os.path.join(r.h, 'nowhere.git')
        out = r.run(); self.assertEqual(out.returncode, 1); self.assertIn('could not clone the tap', out.stdout)
        self.assertIn('--formula-only', out.stdout)

    def test_a_push_the_tap_refuses_says_how_to_finish(self):
        r = self.rig()
        hook = os.path.join(r.tap, 'hooks', 'pre-receive')
        open(hook, 'w').write('#!/bin/bash\necho refused >&2\nexit 1\n'); os.chmod(hook, 0o755)
        out = r.run(); self.assertEqual(out.returncode, 1)
        self.assertIn('the formula push did not complete', out.stdout); self.assertIn('--formula-only', out.stdout)
        self.assertEqual(r.tap_commits(), '1')

    def test_formula_only_on_a_draft_is_refused(self):
        r = self.rig(); out = r.run('--formula-only')
        self.assert_held(r, out, 'is not published')

    def test_a_darwin_asset_missing(self):
        r = self.rig(); os.remove(os.path.join(r.assets, DARWIN[0])); out = r.run()
        self.assert_held(r, out, f'{DARWIN[0]} is in checksums.txt and not in the release')

    def test_a_darwin_tarball_named_twice_in_the_formula(self):
        r = self.rig(); f = formula(r.unsigned)
        line = f'      url "https://github.com/nmelo/initech/releases/download/{TAG}/{DARWIN[0]}"\n      sha256 "{r.unsigned[DARWIN[0]]}"\n'
        open(os.path.join(r.h, 'formula.rb'), 'w').write(f.replace('  on_linux do\n', line + '  on_linux do\n'))
        out = r.run(); self.assert_held(r, out, f'{DARWIN[0]} is named 2 times')

    def test_no_scratch_left(self):
        r = self.rig(); r.run(); self.assertEqual(os.listdir(r.tmp), [])
        r2 = self.rig(signed=False); r2.run(); self.assertEqual(os.listdir(r2.tmp), [])


if __name__ == '__main__':
    unittest.main(verbosity=1)
