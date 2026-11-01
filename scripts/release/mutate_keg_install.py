#!/usr/bin/env python3
"""Mutation table for brew_upgrade_bounded.sh: each mutant must apply, must still
parse, and must turn test_keg_install.py red. A survivor is printed as SURVIVED."""
import os, re, subprocess, sys, tempfile
HERE = os.path.dirname(os.path.abspath(__file__))
SRC = open(os.path.join(HERE, 'brew_upgrade_bounded.sh')).read()
M = [
 ('ln -sfn, not a rename', 'os.symlink(target, tmp)\nos.replace(tmp, link)', 'os.path.lexists(link) and os.remove(link)\nimport time; time.sleep(0.05)\nos.symlink(target, link)'),
 ('upgrade verb called', 'install_keg || exit 1\n', '"$BREW" upgrade "$FORMULA" >/dev/null 2>&1\ninstall_keg || exit 1\n'),
 ('staged binary not proven', 'prints_version "$STAGE/keg/bin/initech" "$ver" || { echo "the new binary does not report initech $ver; refused"; rm -f "$out"; return 1; }\n', ''),
 ('tarball binary not required', '[ -f "$STAGE/unpack/initech" ] || { echo "the tarball holds no initech binary; refused"; rm -f "$out"; return 1; }\n', ''),
 ('existing keg not proven', 'prints_version "$keg/bin/initech" "$ver" || { echo "keg $keg is there and its binary is not initech $ver; refused"; rm -f "$out"; return 1; }\n', ''),
 ('no timeout on a bounded call', 'for ((t=0; t<LIMIT*5; t++)); do kill -0 "$bp" 2>/dev/null || break; sleep 0.2; done\n  if kill -0 "$bp" 2>/dev/null; then', 'if false; then'),
 ('tree printed with arguments', 'ps -o etime=,%cpu=,stat=,comm= -p', 'ps -o etime=,%cpu=,stat=,args= -p'),
 ('tree not stopped', 'for p in $pids; do kill "$p" 2>/dev/null; done\n    sleep 1\n    for p in $pids; do kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null; done\n    wait "$bp" 2>/dev/null\n', ''),
 ('bound does not stop the run', 'if [ $rc -eq 124 ]; then echo "link untouched: $(readlink "$LINK")"; exit 2; fi\n', ''),
 ('no lock', 'if ! mkdir "$LOCK" 2>/dev/null; then', 'mkdir -p "$LOCK"; if false; then'),
 ('lock kept after the run', '  rm -rf "${LOCK:?}"\n', ''),
 ('a held lock is taken', 'refused, link untouched. If that run is dead, remove the lock by hand."\n  exit 1\n', 'refused, link untouched. If that run is dead, remove the lock by hand."\n'),
 ('mv in place of rename', "python3 -c 'import os,sys; os.rename(sys.argv[1], sys.argv[2])' \"$STAGE/keg\" \"$keg\" 2>/dev/null", 'mv "$STAGE/keg" "$keg" 2>/dev/null'),
 ('lsof control gone', 'if [ $rc -ne 0 ] || ! grep -q \'^n/\' "$out"; then rm -f "$out" "$out.err"; return 1; fi\n', ''),
 ('lsof failure read as none running', 'if [ $rc -ne 0 ] && { [ $rc -ne 1 ] || [ -s "$out.err" ]; }; then rm -f "$out" "$out.err"; return 1; fi\n', ''),
 ('prune goes on when lsof cannot say', 'echo "prune: lsof could not say which kegs are running; nothing pruned"\n    return 0\n', 'echo "prune: lsof could not say which kegs are running; nothing pruned"\n'),
 ('running kegs not read from real paths', 'n$real/', 'n$CELLAR.never/'),
 ('fetch failure ignored', 'fetch --formula "$FORMULA" || { echo "brew fetch failed: $(tail -1 "$out")"; rm -f "$out"; return 1; }', 'fetch --formula "$FORMULA" || true'),
 ('version not validated', "if ! [[ \"$ver\" =~ ^[0-9]+\\.[0-9]+\\.[0-9]+$ ]]; then echo \"no version read from the formula: '$ver'\"; rm -f \"$out\"; return 1; fi\n", ''),
 ('already-installed check gone', 'if [ "$cur" = "$ver" ] && prints_version "$LINK" "$ver"; then', 'if false; then'),
 ('opt link not moved', 'swap_link "../Cellar/initech/$ver" "$P/opt/initech" || { echo "opt link failed; bin link untouched"; return 1; }\n', ''),
 ('linked record not moved', 'swap_link "../../../Cellar/initech/$ver" "$P/var/homebrew/linked/initech" || { echo "linked record failed; bin link untouched"; return 1; }\n', ''),
 ('bin link not moved', 'swap_link "../Cellar/initech/$ver/bin/initech" "$LINK" || { echo "bin link failed"; return 1; }\n', ''),
 ('binary left writable', ' && chmod 555 "$STAGE/keg/bin/initech"', ''),
 ('formula copy missing', 'cp "$P/Library/Taps/nmelo/homebrew-tap/Formula/initech.rb" "$STAGE/keg/.brew/initech.rb" || { rm -f "$out"; return 1; }\n', ''),
 ('receipt keeps old version', 's.setdefault("versions", {})["stable"] = ver\n', ''),
 ('staging kept on failure', '  [ -n "$STAGE" ] && rm -rf "${STAGE:?}"\n', ''),
 ('staging kept on success', 'rm -rf "${STAGE:?}"; STAGE=\n', 'STAGE=\n'),
 ('prunes without the flag', 'if [ "$PRUNE" = 1 ]; then', 'if true; then'),
 ('prunes after a failed install', 'install_keg || exit 1\n', 'install_keg || true\n'),
 ('prunes after a broken link', '[ "$LINK_WAS_GOOD" = 1 ] || { echo "prune: the link was broken before this run; nothing removed"; exit 1; }\n', ''),
 ('previous by lexical sort', 'sort -t. -k1,1n -k2,2n -k3,3n', 'sort'),
 ('previous not kept', 'if [ "$name" = "$cur" ] || [ "$name" = "$prev" ]; then', 'if [ "$name" = "$cur" ]; then'),
 ('installed not kept', 'if [ "$name" = "$cur" ] || [ "$name" = "$prev" ]; then', 'if [ "$name" = "$prev" ]; then'),
 ('running keg removed', 'if grep -qx "$name" <<<"$running"; then echo "kept $name (running)"; continue; fi\n', ''),
 ('any directory name removed', '''[[ "$name" =~ ^[0-9]+\\.[0-9]+\\.[0-9]+$ ]] || continue\n''', ''),
 ('symlinked keg removed', '[ -d "$dir" ] && [ ! -L "$dir" ] || continue\n', ''),
]
survived = 0
for name, old, new in M:
    assert SRC.count(old) == 1, (name, SRC.count(old))
    with tempfile.NamedTemporaryFile('w', suffix='.sh', delete=False) as f:
        f.write(SRC.replace(old, new))
    subprocess.run(['bash', '-n', f.name], check=True)
    env = dict(os.environ, INSTALL_SCRIPT=f.name)
    r = subprocess.run([sys.executable, os.path.join(HERE, 'test_keg_install.py')], env=env, capture_output=True, text=True)
    red = re.findall(r'^(?:FAIL|ERROR): (\w+)', r.stderr, re.M)
    os.unlink(f.name)
    print(f"{'KILLED ' if red else 'SURVIVED'} {name}: {', '.join(red[:3])}", flush=True)
    survived += not red
print(f'{len(M)} mutants, {survived} survived')
sys.exit(1 if survived else 0)
