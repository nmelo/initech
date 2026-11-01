#!/usr/bin/env python3
"""brew_upgrade_bounded.sh: the initech link never goes away while other rigs
run. Every case uses a temp prefix and a fake brew: no network, no real Cellar."""
import json, os, shutil, stat, subprocess, sys, tarfile, tempfile, threading, time, unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.environ.get('INSTALL_SCRIPT', os.path.join(HERE, 'brew_upgrade_bounded.sh'))
OLD, NEW = '2.48.0', '2.49.0'

FAKE_BREW = r'''#!/bin/bash
# Records every call. The upgrade verb behaves as real brew does: the link goes first.
echo "$*" >> "$H/calls"
case "$1" in
  info) printf '{"formulae":[{"versions":{"stable":"%s"}}]}\n' "$NEWV";;
  fetch) [ -f "$H/fetch_fails" ] && exit 1
         if [ -f "$H/fetch_hangs" ]; then echo $$ > "$H/brew_pid"; exec sleep 86399; fi
         exit 0;;
  --cache) echo "$H/new.tar.gz";;
  upgrade) rm -f "$P/bin/initech"; sleep 1
           mkdir -p "$P/Cellar/initech/$NEWV/bin"; cp "$H/new-initech" "$P/Cellar/initech/$NEWV/bin/initech"
           ln -s "../Cellar/initech/$NEWV/bin/initech" "$P/bin/initech";;
  unlink) rm -f "$P/bin/initech";;
  link) ;;
  *) ;;
esac
'''

def alive(pid):
    try:
        os.kill(pid, 0)
    except OSError:
        return False
    return subprocess.run(['ps', '-o', 'stat=', '-p', str(pid)], capture_output=True, text=True).stdout.strip()[:1] not in ('', 'Z')


def binary(path, ver):
    with open(path, 'w') as f:
        f.write(f'#!/bin/bash\n[ "$1" = version ] && echo "initech {ver}"\nexit 0\n')
    os.chmod(path, 0o755)

class Rig:
    def __init__(self, new_binary_version=NEW, kegs=(OLD,)):
        self.h = tempfile.mkdtemp(prefix='kegtest', dir=os.environ.get('TMPDIR'))
        self.h = os.path.realpath(self.h)
        self.p = os.path.join(self.h, 'hb')
        for d in ('bin', 'opt', 'var/homebrew/linked', 'Cellar/initech',
                  'Library/Taps/nmelo/homebrew-tap/Formula'):
            os.makedirs(os.path.join(self.p, d))
        with open(os.path.join(self.p, 'Library/Taps/nmelo/homebrew-tap/Formula/initech.rb'), 'w') as f:
            f.write('class Initech < Formula\nend\n')
        for v in kegs:
            self.keg(v)
        os.symlink(f'../Cellar/initech/{OLD}/bin/initech', os.path.join(self.p, 'bin/initech'))
        os.symlink(f'../Cellar/initech/{OLD}', os.path.join(self.p, 'opt/initech'))
        os.symlink(f'../../../Cellar/initech/{OLD}', os.path.join(self.p, 'var/homebrew/linked/initech'))
        fb = os.path.join(self.h, 'fakebin'); os.makedirs(fb)
        with open(os.path.join(fb, 'brew'), 'w') as f:
            f.write(FAKE_BREW)
        os.chmod(os.path.join(fb, 'brew'), 0o755)
        self.brew = os.path.join(fb, 'brew')
        os.symlink(os.path.join(self.p, 'bin/initech'), os.path.join(fb, 'initech'))
        nb = os.path.join(self.h, 'new-initech'); binary(nb, new_binary_version)
        with tarfile.open(os.path.join(self.h, 'new.tar.gz'), 'w:gz') as t:
            t.add(nb, arcname='initech')
            for n in ('LICENSE', 'README.md'):
                q = os.path.join(self.h, n); open(q, 'w').write(n); t.add(q, arcname=n)
        self.tmp = os.path.join(self.h, 'tmp'); os.makedirs(self.tmp)
        self.env = dict(os.environ, H=self.h, P=self.p, NEWV=NEW, HB_PREFIX=self.p, BREW=self.brew,
                        PATH=fb + ':' + os.environ['PATH'], LIMIT='5', TMPDIR=self.tmp)

    def keg(self, v):
        k = os.path.join(self.p, 'Cellar/initech', v)
        os.makedirs(os.path.join(k, 'bin'))
        binary(os.path.join(k, 'bin/initech'), v)
        with open(os.path.join(k, 'INSTALL_RECEIPT.json'), 'w') as f:
            json.dump({'installed_on_request': True, 'time': 1,
                       'source': {'tap': 'nmelo/tap', 'tap_git_head': 'old',
                                  'versions': {'stable': v}}}, f)
        return k

    def run(self, *args, timeout=40):
        return subprocess.run(['bash', SCRIPT, *args], env=self.env, capture_output=True, text=True,
                              timeout=timeout, stdin=subprocess.DEVNULL)

    def link(self, rel='bin/initech'):
        return os.readlink(os.path.join(self.p, rel))

    def version(self):
        return subprocess.run([os.path.join(self.p, 'bin/initech'), 'version'],
                              capture_output=True, text=True).stdout.strip()

    def calls(self):
        c = os.path.join(self.h, 'calls')
        return open(c).read().splitlines() if os.path.exists(c) else []

    def kegs(self):
        return sorted(os.listdir(os.path.join(self.p, 'Cellar/initech')))

    def close(self):
        # A hung fake outlives a script killed at a test's timeout (a mutant that
        # drops the bound): stop it by the PID it wrote, never by name.
        for f in ('brew_pid', 'lsof_pid'):
            q = os.path.join(self.h, f)
            if os.path.exists(q):
                try:
                    os.kill(int(open(q).read()), 9)
                except (OSError, ValueError):
                    pass
        shutil.rmtree(self.h, ignore_errors=True)


class Case(unittest.TestCase):
    def rig(self, **kw):
        r = Rig(**kw); self.addCleanup(r.close); return r


class Install(Case):
    def test_all_three_links_move_to_the_new_keg(self):
        r = self.rig(); out = r.run()
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertEqual(r.link(), f'../Cellar/initech/{NEW}/bin/initech')
        self.assertEqual(r.link('opt/initech'), f'../Cellar/initech/{NEW}')
        self.assertEqual(r.link('var/homebrew/linked/initech'), f'../../../Cellar/initech/{NEW}')
        self.assertEqual(r.version(), f'initech {NEW}')

    def test_never_upgrade_unlink_or_link(self):
        r = self.rig(); r.run()
        for c in r.calls():
            self.assertNotIn(c.split()[0], ('upgrade', 'unlink', 'link', 'install', 'reinstall'), c)

    def test_link_is_never_missing_during_the_install(self):
        r = self.rig(); missing = []; stop = threading.Event(); n = [0]
        link = os.path.join(r.p, 'bin/initech')
        def watch():
            while not stop.is_set():
                n[0] += 1
                try:
                    os.stat(link)
                except OSError as e:
                    missing.append(str(e))
        t = threading.Thread(target=watch); t.start()
        try:
            out = r.run()
        finally:
            stop.set(); t.join()
        self.assertGreater(n[0], 100, 'the watcher must have really looked')
        self.assertEqual(missing[:1], [], out.stdout)
        self.assertEqual(r.link(), f'../Cellar/initech/{NEW}/bin/initech')

    def test_keg_is_complete_and_brew_can_read_it(self):
        r = self.rig(); r.run()
        k = os.path.join(r.p, 'Cellar/initech', NEW)
        self.assertEqual(stat.S_IMODE(os.stat(os.path.join(k, 'bin/initech')).st_mode), 0o555)
        for n in ('LICENSE', 'README.md', '.brew/initech.rb'):
            self.assertTrue(os.path.exists(os.path.join(k, n)), n)
        d = json.load(open(os.path.join(k, 'INSTALL_RECEIPT.json')))
        self.assertEqual(d['source']['versions']['stable'], NEW)
        self.assertTrue(d['installed_on_request'])

    def test_no_staging_left_behind(self):
        r = self.rig(); r.run()
        self.assertEqual(r.kegs(), [OLD, NEW])
        self.assertEqual([n for n in os.listdir(os.path.join(r.p, 'var/homebrew')) if 'stag' in n], [])

    def test_already_on_the_version_changes_nothing(self):
        r = self.rig(kegs=(OLD,)); r.env['NEWV'] = OLD
        out = r.run()
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertEqual(r.link(), f'../Cellar/initech/{OLD}/bin/initech')
        self.assertFalse(any(c.startswith('fetch') for c in r.calls()))
        self.assertIn('nothing to do', out.stdout)


class FailsSafe(Case):
    def assert_untouched(self, r):
        self.assertEqual(r.link(), f'../Cellar/initech/{OLD}/bin/initech')
        self.assertEqual(r.link('opt/initech'), f'../Cellar/initech/{OLD}')
        self.assertEqual(r.link('var/homebrew/linked/initech'), f'../../../Cellar/initech/{OLD}')
        self.assertEqual(r.version(), f'initech {OLD}')
        self.assertEqual([n for n in os.listdir(os.path.join(r.p, 'var/homebrew')) if 'stag' in n], [], 'no staging left')

    def test_fetch_fails(self):
        r = self.rig(); open(os.path.join(r.h, 'fetch_fails'), 'w').close()
        out = r.run(); self.assertNotEqual(out.returncode, 0); self.assert_untouched(r)
        self.assertEqual(r.kegs(), [OLD])
        self.assertIn('brew fetch failed', out.stdout)

    def test_fetch_hangs_is_bounded_and_stopped(self):
        r = self.rig(); open(os.path.join(r.h, 'fetch_hangs'), 'w').close()
        bystander = subprocess.Popen(['sleep', '60']); self.addCleanup(bystander.kill)
        t = time.time(); out = r.run()
        self.assertEqual(out.returncode, 2, out.stdout + out.stderr)
        self.assertLess(time.time() - t, 25); self.assert_untouched(r)
        pid = int(open(os.path.join(r.h, 'brew_pid')).read())
        self.assertFalse(alive(pid), 'the hung tree is stopped')
        self.assertTrue(alive(bystander.pid), 'only its own tree is stopped')
        self.assertRegex(out.stdout, rf'pid={pid} .*sleep')
        self.assertNotIn('86399', out.stdout, 'comm only, never arguments')

    def test_binary_reports_the_wrong_version(self):
        r = self.rig(new_binary_version='2.47.9')
        out = r.run(); self.assertNotEqual(out.returncode, 0); self.assert_untouched(r)
        self.assertEqual(r.kegs(), [OLD])
        self.assertIn('does not report initech', out.stdout)

    def test_tarball_without_the_binary(self):
        r = self.rig()
        with tarfile.open(os.path.join(r.h, 'new.tar.gz'), 'w:gz') as t:
            q = os.path.join(r.h, 'LICENSE'); open(q, 'w').write('x'); t.add(q, arcname='LICENSE')
        out = r.run(); self.assertNotEqual(out.returncode, 0); self.assert_untouched(r)
        self.assertIn('holds no initech binary', out.stdout)
        self.assertEqual(r.kegs(), [OLD])

    def test_formula_version_unreadable(self):
        r = self.rig(); r.env['NEWV'] = ''
        out = r.run(); self.assertNotEqual(out.returncode, 0); self.assert_untouched(r)
        self.assertIn('no version read', out.stdout)

    def test_a_keg_already_there_with_the_wrong_binary_is_not_linked(self):
        r = self.rig(); k = r.keg(NEW); binary(os.path.join(k, 'bin/initech'), '2.47.9')
        out = r.run(); self.assertNotEqual(out.returncode, 0); self.assert_untouched(r)
        self.assertIn('is not initech', out.stdout)

    def test_a_good_keg_already_there_is_linked(self):
        r = self.rig(); r.keg(NEW)
        out = r.run(); self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertEqual(r.version(), f'initech {NEW}')


class Leaves(Case):
    def assert_tmp_empty(self, r, out):
        self.assertEqual(os.listdir(r.tmp), [], out.stdout)

    def test_no_scratch_file_after_an_install(self):
        r = self.rig(); out = r.run('--prune'); self.assertEqual(out.returncode, 0, out.stdout)
        self.assert_tmp_empty(r, out)

    def test_no_scratch_file_after_a_refusal(self):
        r = self.rig(); open(os.path.join(r.h, 'fetch_fails'), 'w').close()
        out = r.run(); self.assertNotEqual(out.returncode, 0); self.assert_tmp_empty(r, out)

    def test_no_scratch_file_after_a_bound_hit(self):
        r = self.rig(); open(os.path.join(r.h, 'fetch_hangs'), 'w').close()
        out = r.run(); self.assertEqual(out.returncode, 2); self.assert_tmp_empty(r, out)


class TwoRuns(Case):
    def lock(self, r):
        return os.path.join(r.p, 'var/homebrew/initech-install.lock')

    def test_a_held_lock_refuses_and_stays(self):
        r = self.rig(); os.makedirs(self.lock(r))
        with open(os.path.join(self.lock(r), 'pid'), 'w') as f:
            f.write('4242')
        out = r.run()
        self.assertNotEqual(out.returncode, 0)
        self.assertIn('another install holds', out.stdout); self.assertIn('4242', out.stdout)
        self.assertTrue(os.path.isdir(self.lock(r)), 'the other run\'s lock is not ours to remove')
        self.assertEqual(r.calls(), [], 'nothing runs behind a held lock')
        self.assertEqual(r.link(), f'../Cellar/initech/{OLD}/bin/initech'); self.assertEqual(r.kegs(), [OLD])

    def test_the_lock_is_held_during_the_run_and_removed_after(self):
        r = self.rig(); saw = []
        def watch():
            while not done.is_set():
                if os.path.exists(os.path.join(self.lock(r), 'pid')):
                    saw.append(open(os.path.join(self.lock(r), 'pid')).read())
                time.sleep(0.005)
        done = threading.Event(); t = threading.Thread(target=watch); t.start()
        out = r.run(); done.set(); t.join()
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertTrue(saw, 'the lock was never seen'); self.assertFalse(os.path.exists(self.lock(r)))

    def test_the_lock_is_removed_after_a_failure(self):
        r = self.rig(); open(os.path.join(r.h, 'fetch_fails'), 'w').close()
        out = r.run(); self.assertNotEqual(out.returncode, 0)
        self.assertFalse(os.path.exists(self.lock(r)))

    def test_a_keg_appearing_between_stage_and_move_is_not_nested(self):
        # The staged binary, run for its version proof, makes the other keg.
        r = self.rig(); keg = os.path.join(r.p, 'Cellar/initech', NEW)
        with open(os.path.join(r.h, 'new-initech'), 'w') as f:
            f.write(f'#!/bin/bash\nmkdir -p "{keg}/other"\necho "initech {NEW}"\n')
        os.chmod(os.path.join(r.h, 'new-initech'), 0o755)
        with tarfile.open(os.path.join(r.h, 'new.tar.gz'), 'w:gz') as t:
            t.add(os.path.join(r.h, 'new-initech'), arcname='initech')
        out = r.run()
        self.assertNotEqual(out.returncode, 0, out.stdout)
        self.assertIn('already exists', out.stdout)
        self.assertEqual(os.listdir(keg), ['other'], 'nothing moved inside the other keg')
        self.assertEqual(r.link(), f'../Cellar/initech/{OLD}/bin/initech')
        self.assertEqual([n for n in os.listdir(os.path.join(r.p, 'var/homebrew')) if 'stag' in n], [])


class Sampler(Case):
    TOOL = os.path.join(HERE, 'sample_link.py')

    def sampled(self, r, during, old=OLD, new=NEW):
        """Run the sampler around during(); returns its rc, report and samples."""
        out, stop = os.path.join(r.h, 'samples'), os.path.join(r.h, 'stop')
        p = subprocess.Popen([sys.executable, self.TOOL, r.p, out, stop, old, new, '0.01'],
                             stdout=subprocess.PIPE, text=True)
        time.sleep(0.3); during(); time.sleep(0.2)
        open(stop, 'w').close()
        report = p.communicate(timeout=20)[0]
        return p.returncode, report, open(out).read().splitlines()

    def test_every_sample_through_an_install_is_old_or_new(self):
        r = self.rig(); res = []
        rc, report, lines = self.sampled(r, lambda: res.append(r.run()))
        self.assertEqual(res[0].returncode, 0, res[0].stdout)
        self.assertEqual(rc, 0, report)
        self.assertTrue(any(OLD in l for l in lines) and any(NEW in l for l in lines),
                        'the samples must span the swap')

    def test_the_sampler_fires_on_a_missing_link(self):
        r = self.rig()
        link = os.path.join(r.p, 'bin/initech')
        rc, report, _ = self.sampled(r, lambda: os.remove(link))
        self.assertEqual(rc, 1, report); self.assertIn('link missing', report)

    def test_the_sampler_fires_on_a_binary_that_does_not_run(self):
        r = self.rig()
        rc, report, _ = self.sampled(r, lambda: os.chmod(os.path.join(r.p, f'Cellar/initech/{OLD}/bin/initech'), 0o644))
        self.assertEqual(rc, 1, report); self.assertIn('binary said', report)

    def test_the_sampler_fires_on_a_third_keg(self):
        r = self.rig(kegs=(OLD, '2.40.0')); link = os.path.join(r.p, 'bin/initech')
        def swap():
            os.remove(link); os.symlink('../Cellar/initech/2.40.0/bin/initech', link)
        rc, report, _ = self.sampled(r, swap)
        self.assertEqual(rc, 1, report); self.assertIn('link names', report)


class Prune(Case):
    VERS = ('2.9.0', '2.10.0', '2.46.0', '2.47.0', OLD)

    def test_keeps_the_installed_and_previous_only(self):
        r = self.rig(kegs=self.VERS)
        out = r.run('--prune'); self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertEqual(r.kegs(), [OLD, NEW])
        self.assertEqual(r.version(), f'initech {NEW}')

    def test_previous_is_numeric_not_lexical(self):
        # Lexically 2.10.0 sorts first, so it would have no previous and 2.9.0 would go.
        r = self.rig(kegs=('2.8.0', '2.9.0', '2.10.0'))
        r.env['NEWV'] = '2.10.0'
        for rel, target in (('bin/initech', '../Cellar/initech/2.10.0/bin/initech'),
                            ('opt/initech', '../Cellar/initech/2.10.0'),
                            ('var/homebrew/linked/initech', '../../../Cellar/initech/2.10.0')):
            os.remove(os.path.join(r.p, rel)); os.symlink(target, os.path.join(r.p, rel))
        out = r.run('--prune'); self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertEqual(set(r.kegs()), {'2.9.0', '2.10.0'})

    def test_no_prune_without_the_flag(self):
        r = self.rig(kegs=self.VERS); r.run()
        self.assertEqual(set(r.kegs()), set(self.VERS) | {NEW})

    def test_failed_install_prunes_nothing(self):
        r = self.rig(kegs=self.VERS); open(os.path.join(r.h, 'fetch_fails'), 'w').close()
        out = r.run('--prune'); self.assertNotEqual(out.returncode, 0)
        self.assertEqual(set(r.kegs()), set(self.VERS))

    def running(self, r, ver, via):
        """A process running ver's keg binary, started the way real ones are."""
        keep = os.path.join(r.p, f'Cellar/initech/{ver}/bin/initech')
        os.remove(keep); shutil.copy('/bin/sleep', keep)
        subprocess.run(['codesign', '-f', '-s', '-', keep], capture_output=True, check=True)
        d = tempfile.mkdtemp(dir=r.h); link = os.path.join(d, 'initech'); os.symlink(keep, link)
        if via == 'link':
            p = subprocess.Popen([link, '60'])
        else:
            p = subprocess.Popen(['initech', '60'], env=dict(os.environ, PATH=d + ':' + os.environ['PATH']))
        self.addCleanup(p.kill)
        time.sleep(0.3)
        self.assertIsNone(p.poll(), 'the running keg fixture must really be running')
        comm = subprocess.run(['ps', '-o', 'comm=', '-p', str(p.pid)], capture_output=True, text=True).stdout
        self.assertNotIn('Cellar', comm, 'the fixture must look as a real initech does: no keg in comm')

    @unittest.skipUnless(sys.platform == 'darwin', 'the running-keg fixture is a macOS binary re-signed with codesign: it runs on the Mac, in rigs.yml\'s darwin job')
    def test_a_keg_running_through_a_link_stays(self):
        r = self.rig(kegs=self.VERS); self.running(r, '2.46.0', 'link')
        out = r.run('--prune'); self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertEqual(set(r.kegs()), {'2.46.0', OLD, NEW}); self.assertIn('kept 2.46.0 (running)', out.stdout)

    @unittest.skipUnless(sys.platform == 'darwin', 'the running-keg fixture is a macOS binary re-signed with codesign: it runs on the Mac, in rigs.yml\'s darwin job')
    def test_a_keg_running_from_path_stays(self):
        r = self.rig(kegs=self.VERS); self.running(r, '2.47.0', 'path')
        out = r.run('--prune'); self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertEqual(set(r.kegs()), {'2.47.0', OLD, NEW}); self.assertIn('kept 2.47.0 (running)', out.stdout)

    def fake_lsof(self, r, body):
        p = os.path.join(r.h, 'fake-lsof')
        with open(p, 'w') as f:
            f.write('#!/bin/bash\n' + body + '\n')
        os.chmod(p, 0o755); r.env['LSOF'] = p

    def assert_kept_all(self, r, out):
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertEqual(set(r.kegs()), set(self.VERS) | {NEW})
        self.assertIn('nothing pruned', out.stdout)

    def test_lsof_missing_prunes_nothing(self):
        r = self.rig(kegs=self.VERS); r.env['LSOF'] = os.path.join(r.h, 'no-such-lsof')
        self.assert_kept_all(r, r.run('--prune'))

    def test_lsof_failing_prunes_nothing(self):
        r = self.rig(kegs=self.VERS); self.fake_lsof(r, 'echo broken >&2; exit 1')
        self.assert_kept_all(r, r.run('--prune'))

    def test_lsof_silently_empty_prunes_nothing(self):
        # Empty looks like "no initech running"; the control on this shell tells them apart.
        r = self.rig(kegs=self.VERS); self.fake_lsof(r, 'exit 1')
        self.assert_kept_all(r, r.run('--prune'))

    def test_lsof_failing_only_on_the_real_query_prunes_nothing(self):
        r = self.rig(kegs=self.VERS)
        self.fake_lsof(r, 'case "$*" in *-c*) echo broken >&2; exit 1;; *) echo p1; echo n/bin/bash;; esac')
        self.assert_kept_all(r, r.run('--prune'))

    def test_lsof_past_the_bound_prunes_nothing(self):
        r = self.rig(kegs=self.VERS); self.fake_lsof(r, 'echo $$ > "$H/lsof_pid"; exec sleep 86399')
        t = time.time(); out = r.run('--prune')
        self.assert_kept_all(r, out); self.assertLess(time.time() - t, 25)
        self.assertEqual(r.version(), f'initech {NEW}', 'the install itself stands')

    def test_only_version_named_directories_go(self):
        r = self.rig(kegs=self.VERS)
        other = os.path.join(r.p, 'Cellar/initech/notes'); os.makedirs(other)
        r.run('--prune')
        self.assertTrue(os.path.isdir(other))

    def test_a_symlinked_keg_is_not_followed(self):
        r = self.rig(kegs=self.VERS)
        outside = os.path.join(r.h, 'outside'); os.makedirs(outside)
        open(os.path.join(outside, 'precious'), 'w').close()
        os.symlink(outside, os.path.join(r.p, 'Cellar/initech/2.1.0'))
        r.run('--prune')
        self.assertTrue(os.path.exists(os.path.join(outside, 'precious')))
        self.assertTrue(os.path.islink(os.path.join(r.p, 'Cellar/initech/2.1.0')), 'the symlink itself stays')

    def test_nothing_to_read_deletes_nothing(self):
        r = self.rig(kegs=self.VERS); r.env['NEWV'] = OLD
        os.remove(os.path.join(r.p, 'bin/initech'))
        out = r.run('--prune'); self.assertNotEqual(out.returncode, 0)
        self.assertEqual(set(r.kegs()), set(self.VERS))


if __name__ == '__main__':
    unittest.main(verbosity=1)
