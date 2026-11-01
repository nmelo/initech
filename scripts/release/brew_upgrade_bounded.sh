#!/bin/bash
# The initech install on a Mac with rigs running: the link never goes away.
# brew downloads and checks the tarball; this script builds the new keg beside
# the old one, proves its binary, then renames the links over, one step each.
# Never brew upgrade, unlink or link here: an upgrade removes the link before it
# builds, and a hang there leaves the fleet with no initech on PATH.
#
#   brew_upgrade_bounded.sh           install the tap formula's version
#   brew_upgrade_bounded.sh --prune   then remove old kegs, keeping the installed
#                                     one, the previous one and any still running
#
# Exit: 0 installed or already there; 1 refused or failed, link untouched;
# 2 a brew step hit the bound, stopped by PID, link untouched.
set -u
LIMIT=${LIMIT:-300}
BREW=${BREW:-brew}
LSOF=${LSOF:-lsof}
P=${HB_PREFIX:-$("$BREW" --prefix)}
FORMULA=nmelo/tap/initech
CELLAR=$P/Cellar/initech
LINK=$P/bin/initech
PRUNE=0
[ "${1:-}" = --prune ] && PRUNE=1
export NONINTERACTIVE=1 HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1
cd "$P" || exit 1

# One install at a time: a second run refuses, and a lock left by a killed run
# is removed by hand, since taking it over would reopen the race.
LOCK=$P/var/homebrew/initech-install.lock
if ! mkdir "$LOCK" 2>/dev/null; then
  echo "another install holds $LOCK (pid $(cat "$LOCK/pid" 2>/dev/null || echo unknown)); refused, link untouched. If that run is dead, remove the lock by hand."
  exit 1
fi
echo $$ > "$LOCK/pid"
STAGE=
cleanup() {
  [ -n "$STAGE" ] && rm -rf "${STAGE:?}"
  rm -rf "${LOCK:?}"
}
trap cleanup EXIT

tree() { local p=$1; echo "$p"; for c in $(pgrep -P "$p"); do tree "$c"; done; }

# bounded <out> <cmd...>: one call within LIMIT. Past it, prints the call's own
# process tree (pid, etime, cpu, stat, comm: never arguments), stops only those
# PIDs and returns 124. The link is not in play in any bounded call.
bounded() {
  local out=$1 bp t p pids; shift
  "$@" </dev/null >"$out" 2>"$out.err" &
  bp=$!
  for ((t=0; t<LIMIT*5; t++)); do kill -0 "$bp" 2>/dev/null || break; sleep 0.2; done
  if kill -0 "$bp" 2>/dev/null; then
    echo "TIMEOUT after ${LIMIT}s; process tree:"
    pids=$(tree "$bp")
    for p in $pids; do echo "  pid=$p $(ps -o etime=,%cpu=,stat=,comm= -p "$p" 2>/dev/null)"; done
    for p in $pids; do kill "$p" 2>/dev/null; done
    sleep 1
    for p in $pids; do kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null; done
    wait "$bp" 2>/dev/null
    return 124
  fi
  wait "$bp"
}

# bounded_brew <out> <args...>: a brew call; past the bound the whole run stops.
bounded_brew() {
  local out=$1 rc; shift
  bounded "$out" "$BREW" "$@"; rc=$?
  if [ $rc -eq 124 ]; then echo "link untouched: $(readlink "$LINK")"; exit 2; fi
  return $rc
}

# prints_version <binary> <version>: the binary runs and says it is that version.
prints_version() { [ "$("$1" version 2>/dev/null)" = "initech $2" ]; }

# swap_link <target> <link>: rename(2) a new symlink over the old one. ln -sfn
# unlinks first on macOS, which leaves a moment with no link; a rename does not.
swap_link() {
  python3 -c '
import os, sys
target, link = sys.argv[1:]
tmp = f"{link}.new.{os.getpid()}"
os.symlink(target, tmp)
os.replace(tmp, link)' "$1" "$2"
}

installed() { # the version the link points at
  local l; l=$(readlink "$LINK") || return 1
  case "$l" in ../Cellar/initech/*/bin/initech) l=${l#../Cellar/initech/}; echo "${l%%/*}";; *) return 1;; esac
}

install_keg() {
  local out ver cur tarball keg
  out=$(mktemp "${TMPDIR:-/tmp}/brew-out.XXXX")
  echo "before: $(readlink "$LINK") / $("$LINK" version 2>&1)"
  bounded_brew "$out" info --json=v2 "$FORMULA" || { echo "brew info failed"; rm -f "$out"; return 1; }
  ver=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["formulae"][0]["versions"]["stable"])' "$out" 2>/dev/null)
  if ! [[ "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then echo "no version read from the formula: '$ver'"; rm -f "$out"; return 1; fi
  cur=$(installed) || cur=
  if [ "$cur" = "$ver" ] && prints_version "$LINK" "$ver"; then
    echo "already on $ver; nothing to do"; rm -f "$out"; return 0
  fi

  keg=$CELLAR/$ver
  if [ -e "$keg" ]; then
    # A keg already there is used only if its binary proves itself.
    prints_version "$keg/bin/initech" "$ver" || { echo "keg $keg is there and its binary is not initech $ver; refused"; rm -f "$out"; return 1; }
    echo "keg $ver already in place"
  else
    bounded_brew "$out" fetch --formula "$FORMULA" || { echo "brew fetch failed: $(tail -1 "$out")"; rm -f "$out"; return 1; }
    bounded_brew "$out" --cache "$FORMULA" || { echo "brew --cache failed"; rm -f "$out"; return 1; }
    tarball=$(tail -1 "$out")
    [ -f "$tarball" ] || { echo "no download at '$tarball'"; rm -f "$out"; return 1; }

    STAGE=$(mktemp -d "$P/var/homebrew/initech-staging.XXXX") || { rm -f "$out"; return 1; }
    mkdir -p "$STAGE/keg/bin" "$STAGE/keg/.brew" "$STAGE/unpack"
    tar xzf "$tarball" -C "$STAGE/unpack" || { echo "unpack failed"; rm -f "$out"; return 1; }
    [ -f "$STAGE/unpack/initech" ] || { echo "the tarball holds no initech binary; refused"; rm -f "$out"; return 1; }
    mv "$STAGE/unpack/initech" "$STAGE/keg/bin/initech" && chmod 555 "$STAGE/keg/bin/initech"
    for f in LICENSE README.md; do [ -f "$STAGE/unpack/$f" ] && mv "$STAGE/unpack/$f" "$STAGE/keg/$f"; done
    cp "$P/Library/Taps/nmelo/homebrew-tap/Formula/initech.rb" "$STAGE/keg/.brew/initech.rb" || { rm -f "$out"; return 1; }
    prints_version "$STAGE/keg/bin/initech" "$ver" || { echo "the new binary does not report initech $ver; refused"; rm -f "$out"; return 1; }
    # brew reads the receipt: the last keg's, with this version and tap head.
    python3 - "$CELLAR/$cur/INSTALL_RECEIPT.json" "$STAGE/keg/INSTALL_RECEIPT.json" "$ver" "$P/Library/Taps/nmelo/homebrew-tap" <<'PY' || { echo "receipt failed"; rm -f "$out"; return 1; }
import json, subprocess, sys, time
src, dst, ver, tap = sys.argv[1:]
try:
    d = json.load(open(src))
except OSError:
    d = {"source": {"tap": "nmelo/tap", "spec": "stable", "versions": {}}}
d["time"] = int(time.time())
d["installed_on_request"] = True
s = d.setdefault("source", {})
s.setdefault("versions", {})["stable"] = ver
r = subprocess.run(["git", "-C", tap, "rev-parse", "HEAD"], capture_output=True, text=True)
if r.returncode == 0:
    s["tap_git_head"] = r.stdout.strip()
json.dump(d, open(dst, "w"))
PY
    # rename(2) is the existence check and the move in one step: it refuses a
    # non-empty directory, where mv would nest the keg inside it.
    python3 -c 'import os,sys; os.rename(sys.argv[1], sys.argv[2])' "$STAGE/keg" "$keg" 2>/dev/null \
      || { echo "$keg already exists or could not be moved; refused, link untouched"; rm -f "$out" "$out.err"; return 1; }
    rm -rf "${STAGE:?}"; STAGE=
    echo "keg in place: $keg"
  fi
  rm -f "$out" "$out.err"

  # opt and linked first, so the binary goes live with brew's records already right.
  swap_link "../Cellar/initech/$ver" "$P/opt/initech" || { echo "opt link failed; bin link untouched"; return 1; }
  swap_link "../../../Cellar/initech/$ver" "$P/var/homebrew/linked/initech" || { echo "linked record failed; bin link untouched"; return 1; }
  swap_link "../Cellar/initech/$ver/bin/initech" "$LINK" || { echo "bin link failed"; return 1; }
  echo "after: $(readlink "$LINK") / $("$LINK" version 2>&1)"
  prints_version "$LINK" "$ver"
}

# running_kegs: the kegs initech processes run from, by the real path of each
# process's binary (comm shows the link or a bare name, not the keg). Prints one
# version per line; returns non-zero when that cannot be known. The control on
# this shell tells "no initech running" from an lsof that answers nothing.
running_kegs() {
  local out rc real
  out=$(mktemp "${TMPDIR:-/tmp}/lsof-out.XXXX")
  bounded "$out" "$LSOF" -w -a -p $$ -d txt -Fn; rc=$?
  if [ $rc -ne 0 ] || ! grep -q '^n/' "$out"; then rm -f "$out" "$out.err"; return 1; fi
  bounded "$out" "$LSOF" -w -a -c initech -d txt -Fn; rc=$?
  if [ $rc -ne 0 ] && { [ $rc -ne 1 ] || [ -s "$out.err" ]; }; then rm -f "$out" "$out.err"; return 1; fi
  real=$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$CELLAR")
  sed -n "s|^n$real/\([^/]*\)/bin/initech\$|\1|p" "$out" | sort -u
  rm -f "$out" "$out.err"
}

prune_kegs() {
  local cur prev name dir running
  cur=$(installed) || { echo "prune: cannot read the installed version; nothing removed"; return 1; }
  [ -d "$CELLAR/$cur" ] && prints_version "$LINK" "$cur" || { echo "prune: the link does not run $cur; nothing removed"; return 1; }
  prev=$(ls "$CELLAR" | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | sort -t. -k1,1n -k2,2n -k3,3n | grep -B1 -x "$cur" | head -1)
  [ "$prev" = "$cur" ] && prev=
  if ! running=$(running_kegs); then
    echo "prune: lsof could not say which kegs are running; nothing pruned"
    return 0
  fi
  for name in $(ls "$CELLAR"); do
    dir=$CELLAR/$name
    [[ "$name" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || continue
    [ -d "$dir" ] && [ ! -L "$dir" ] || continue
    if [ "$name" = "$cur" ] || [ "$name" = "$prev" ]; then echo "kept $name"; continue; fi
    if grep -qx "$name" <<<"$running"; then echo "kept $name (running)"; continue; fi
    rm -rf "${dir:?}" && echo "pruned $name"
  done
}

# A link that was broken before this run is repaired above, but its Cellar is
# not trusted enough to delete from in the same run.
LINK_WAS_GOOD=1
installed >/dev/null && "$LINK" version >/dev/null 2>&1 || LINK_WAS_GOOD=0
install_keg || exit 1
if [ "$PRUNE" = 1 ]; then
  [ "$LINK_WAS_GOOD" = 1 ] || { echo "prune: the link was broken before this run; nothing removed"; exit 1; }
  prune_kegs || exit 1
fi
exit 0
