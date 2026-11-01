#!/bin/bash
# app-serve-launchd-check.sh: prove initech app-serve under launchd without ever
# loading the real label.
#
#   scripts/app-serve-launchd-check.sh <initech binary>
#
# A temp HOME does not isolate launchd: the gui domain and the service label are
# the user's, and launchd gives a job the user's real HOME. So:
#   - install and uninstall run with a temp HOME and a FAKE launchctl first on
#     PATH: the job file and the calls are proven, the real domain is not touched;
#   - launchd itself runs a throwaway copy of that job, label
#     com.initech.app-serve.qa-<pid>, with HOME=<temp> added to its environment,
#     booted out by that label on every way out of this script.
# It refuses to start while the real label is loaded, and refuses a binary under
# ~/Desktop, ~/Documents or ~/Downloads: macOS guards those for launchd jobs, and
# running one from there can raise a permission dialog on the operator's screen.
# Copy the binary out first. Everything the job touches is under /tmp. It needs no
# agent and no TUI. The token stays in 0600 files and goes to curl with -H @file, never on a
# command line. Every wait is a counted loop. Exit 0 when every check passed.
set -u
BIN=${1:?usage: app-serve-launchd-check.sh <initech binary>}
[ -x "$BIN" ] || { echo "not executable: $BIN"; exit 2; }
BIN=$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")
BINREAL=$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$BIN")
for g in Desktop Documents Downloads; do
  case "$BINREAL/" in "$HOME/$g/"*|"$(cd "$HOME" && pwd -P)/$g/"*)
    echo "REFUSING: $BINREAL is under ~/$g, which macOS guards for launchd jobs (a dialog on the operator's screen)."
    echo "Copy it out first: cp $BIN /tmp/initech-qa-\$\$ and pass that."; exit 2;;
  esac
done
# The job inherits this PATH, and a tool lookup stats every entry.
IFS=: read -r -a PATHS <<< "$PATH"
for e in "${PATHS[@]}"; do
  for g in Desktop Documents Downloads; do
    case "$e/" in "$HOME/$g/"*)
      echo "REFUSING: PATH holds $e, under ~/$g, which macOS guards for launchd jobs (a dialog on the operator's screen)."
      echo "Run this with that entry removed from PATH."; exit 2;;
    esac
  done
done
U=$(id -u)
REAL=com.initech.app-serve
if launchctl print "gui/$U/$REAL" >/dev/null 2>&1; then
  echo "REFUSING: the real label $REAL is loaded; this script never touches it"; exit 2
fi
QA=$REAL.qa-$$
T=$(mktemp -d /tmp/iqd.XXXX)
chmod 700 "$T"
H=$T/home; mkdir -p "$H" "$T/rigs/r" "$T/fake"; chmod 700 "$H"
PORT=$(( 20000 + RANDOM % 20000 ))
LINE="initech app-serve restarted after an unclean exit"
FAILED=0
ok()   { echo "  ok    $*"; }
bad()  { echo "  FAIL  $*"; FAILED=1; }
check() { if [ "$2" = "$3" ]; then ok "$1 ($2)"; else bad "$1: got '$2', want '$3'"; fi; }

cleanup() {
  launchctl bootout "gui/$U/$QA" >/dev/null 2>&1
  if launchctl print "gui/$U/$QA" >/dev/null 2>&1; then
    echo "  FAIL  the throwaway label $QA is STILL LOADED: launchctl bootout gui/$U/$QA"; FAILED=1
  fi
  rm -rf "${T:?}"
}
trap cleanup EXIT
trap 'exit 3' INT TERM

printf 'project: r\nroot: %s\nroles: [a]\n' "$T/rigs/r" > "$T/rigs/r/initech.yaml"
printf '#!/bin/sh\necho "$*" >> %s/notify.ran\n' "$T" > "$T/notify.sh"; chmod 700 "$T/notify.sh"
printf '#!/bin/sh\necho "$*" >> %s/launchctl.calls\n' "$T" > "$T/fake/launchctl"; chmod 700 "$T/fake/launchctl"
touch "$T/notify.ran" "$T/launchctl.calls"
[ "$(cd "$T/fake" && ./launchctl probe; tail -1 "$T/launchctl.calls")" = probe ] || { echo "the fake launchctl does not run"; exit 2; }
: > "$T/launchctl.calls"

echo "== token (temp HOME)"
( umask 077; cd "$T/rigs/r" && HOME=$H "$BIN" app-serve token --listen local-network --port "$PORT" > "$T/pair.txt" 2>&1 )
URL=$(grep '^initech://pair?' "$T/pair.txt" | head -1)
[ -n "$URL" ] || { bad "no pairing URL (the token step's error, token-free, follows)"; grep -v 'token=' "$T/pair.txt" | head -5; exit 1; }
python3 - "$T" "$URL" <<'PY'
import os, sys, urllib.parse
d, url = sys.argv[1], sys.argv[2]
q = urllib.parse.parse_qs(urllib.parse.urlparse(url).query)
fd = os.open(d + '/auth.hdr', os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
os.write(fd, ('Authorization: Bearer ' + q['token'][0] + '\n').encode()); os.close(fd)
open(d + '/addr', 'w').write(q['host'][0])
PY
ADDR=$(cat "$T/addr")
if grep -q $'\xe2\x96\x80\|\xe2\x96\x84\|\xe2\x96\x88' "$T/pair.txt"; then
  python3 - "$T/pair.txt" "$T/qr.png" <<'PY'
import re, struct, sys, zlib
rows = []
for line in open(sys.argv[1], encoding='utf-8').read().splitlines():
    if '\x1b[' not in line: continue
    cells = re.sub(r'\x1b\[[0-9;]*m', '', line)
    rows += [[c in '█▀' for c in cells], [c in '█▄' for c in cells]]
S = 8; raw = b''
for r in rows: raw += (b'\x00' + b''.join((b'\x00' if m else b'\xff') * S for m in r)) * S
def chunk(t, d): return struct.pack('>I', len(d)) + t + d + struct.pack('>I', zlib.crc32(t + d))
open(sys.argv[2], 'wb').write(b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', len(rows[0]) * S, len(rows) * S, 8, 0, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(raw)) + chunk(b'IEND', b''))
PY
  if command -v zbarimg >/dev/null; then
    if [ "$(zbarimg -q --raw "$T/qr.png" | tr -d '\n')" = "$URL" ]; then ok "the printed QR decodes to the printed URL"; else bad "the printed QR does not decode to the printed URL"; fi
  else bad "zbarimg is not installed: the QR was not decoded"; fi
else
  bad "the token command printed no QR"
fi

echo "== install and uninstall (temp HOME, fake launchctl)"
FAKE="PATH=$T/fake:$PATH"
PLIST=$H/Library/LaunchAgents/$REAL.plist
INSTALL=("$BIN" app-serve install --listen local-network --port "$PORT" --projects "$T/rigs" --notify "$T/notify.sh")
env "$FAKE" HOME="$H" "${INSTALL[@]}" > "$T/install1.out" 2>&1; check "install exits" "$?" 0
[ -f "$PLIST" ] && ok "the job file is written" || bad "no job file at $PLIST"
plutil -lint "$PLIST" >/dev/null && ok "plutil accepts the job" || bad "plutil rejects the job"
check "the fake saw the bootstrap" "$(cat "$T/launchctl.calls")" "bootstrap gui/$U $PLIST"
KEYS=$(plutil -convert json -o - "$PLIST" | python3 -c 'import json,sys; print(" ".join(sorted(json.load(sys.stdin))))')
check "exactly the listed keys" "$KEYS" "EnvironmentVariables KeepAlive Label ProgramArguments StandardErrorPath StandardOutPath ThrottleInterval"
ENVKEYS=$(plutil -convert json -o - "$PLIST" | python3 -c 'import json,sys; print(" ".join(sorted(json.load(sys.stdin)["EnvironmentVariables"])))')
check "the environment is PATH alone" "$ENVKEYS" PATH
check "it is the PATH install ran with" "$(plutil -extract EnvironmentVariables.PATH raw -o - "$PLIST")" "$T/fake:$PATH"
check "KeepAlive" "$(plutil -extract KeepAlive raw -o - "$PLIST")" true
check "ThrottleInterval" "$(plutil -extract ThrottleInterval raw -o - "$PLIST")" 10
SUM=$(shasum "$PLIST"); : > "$T/launchctl.calls"
env "$FAKE" HOME="$H" "${INSTALL[@]}" > "$T/install2.out" 2>&1; check "a second install exits" "$?" 0
grep -q '^already installed' "$T/install2.out" && ok "it says already installed" || bad "second install said: $(cat "$T/install2.out")"
check "the file is byte for byte the same" "$(shasum "$PLIST")" "$SUM"
check "and no launchctl call" "$(cat "$T/launchctl.calls")" ""

echo "== launchd, throwaway label $QA"
JOB=$T/$QA.plist
cp "$PLIST" "$JOB"
plutil -replace Label -string "$QA" "$JOB"
plutil -insert EnvironmentVariables.HOME -string "$H" "$JOB"
launchctl bootstrap "gui/$U" "$JOB" || { bad "launchctl bootstrap of the throwaway label failed"; exit 1; }
job_pid() { launchctl print "gui/$U/$QA" 2>/dev/null | awk '/^[[:space:]]*pid = /{print $3; exit}'; }
rigs_code() { curl -sk -m 3 -o /dev/null -w '%{http_code}' -H @"$T/auth.hdr" "https://$ADDR:$PORT/v1/rigs"; }
# wait_new_pid <old pid>: a PID other than old, answering; prints it and the seconds taken.
wait_new_pid() {
  local i p
  for i in $(seq 1 40); do
    p=$(job_pid)
    if [ -n "$p" ] && [ "$p" != "$1" ] && [ "$(rigs_code)" = 200 ]; then echo "$p $i"; return 0; fi
    sleep 1
  done
  return 1
}
LOG=$H/Library/Logs/initech/app-serve.log
restarts() { grep -c "^$LINE\$" "$LOG" 2>/dev/null; }
MARKER=$H/.config/initech/app-serve/running

set -- $(wait_new_pid none) ; P1=${1:-}; [ -n "$P1" ] || { bad "the service never answered; its log:"; sed -E 's/token=[^& ]*/token=***/' "$LOG" | tail -5; exit 1; }
ok "the service answers GET /v1/rigs, PID $P1"
launchctl print "gui/$U/$QA" | grep -qF "PATH => $T/fake:$PATH" && ok "launchd gave the job that PATH" || bad "the job's PATH is not the one written"
check "no token is refused" "$(curl -sk -m 3 -o /dev/null -w '%{http_code}' "https://$ADDR:$PORT/v1/rigs")" 401
[ -f "$MARKER" ] && ok "the running marker is there" || bad "no running marker: the serve hook (RunMarked) is not in this binary"
check "a first start notifies nothing" "$(wc -l < "$T/notify.ran" | tr -d ' ')" 0
grep -q 'cannot read' "$LOG" && bad "a rig folder was reported unreadable: $(grep 'cannot read' "$LOG" | head -1)" || ok "no rig folder reported unreadable"

echo "== kill -9: launchd brings it back, notify runs once"
kill -9 "$P1"
set -- $(wait_new_pid "$P1"); P2=${1:-}; S2=${2:-}
[ -n "$P2" ] && ok "new PID $P2 answering after about ${S2} s (was $P1)" || { bad "no new PID after kill -9"; exit 1; }
[ "${S2:-99}" -le 15 ] && ok "within about ten seconds" || bad "took ${S2} s"
ps -o pid=,comm= -p "$P1" >/dev/null 2>&1 && bad "PID $P1 is still there" || ok "PID $P1 is gone"
check "notify ran once" "$(wc -l < "$T/notify.ran" | tr -d ' ')" 1
check "with the fixed line" "$(head -1 "$T/notify.ran")" "$LINE"
check "the log records the restart" "$(restarts)" 1

echo "== kill -9 again within the hour: restart logged, no second notify"
kill -9 "$P2"
set -- $(wait_new_pid "$P2"); P3=${1:-}
[ -n "$P3" ] && ok "new PID $P3" || { bad "no new PID after the second kill -9"; exit 1; }
check "notify still ran once" "$(wc -l < "$T/notify.ran" | tr -d ' ')" 1
check "the log records both restarts" "$(restarts)" 2

echo "== plain kill (TERM): a clean exit, no restart reported"
kill "$P3"
set -- $(wait_new_pid "$P3"); P4=${1:-}
[ -n "$P4" ] && ok "launchd started PID $P4 (KeepAlive)" || { bad "no new PID after kill"; exit 1; }
grep -q 'stopping on terminated' "$LOG" && ok "the old run stopped on the signal" || bad "the log does not show the clean stop"
check "still two restarts in the log" "$(restarts)" 2
check "notify still ran once" "$(wc -l < "$T/notify.ran" | tr -d ' ')" 1

echo "== the token appears nowhere"
TOK=$(sed -E 's/^Authorization: Bearer //' "$T/auth.hdr")
for f in "$LOG" "$T/notify.ran" "$MARKER" "$H/.config/initech/app-serve/notified-at" "$PLIST"; do
  if [ -f "$f" ] && grep -qF "$TOK" "$f"; then bad "the token is in $f"; else ok "not in $(basename "$f")"; fi
done

echo "== boot out: no label, no PID, the marker gone"
launchctl bootout "gui/$U/$QA"; check "bootout exits" "$?" 0
for i in $(seq 1 10); do launchctl print "gui/$U/$QA" >/dev/null 2>&1 || break; sleep 1; done
launchctl print "gui/$U/$QA" >/dev/null 2>&1 && bad "the label is still loaded" || ok "the label is gone"
ps -o pid=,comm= -p "$P4" >/dev/null 2>&1 && bad "PID $P4 is still there" || ok "PID $P4 is gone"
[ -f "$MARKER" ] && bad "the marker remains after a clean stop" || ok "the marker is gone"

echo "== uninstall (temp HOME, fake launchctl)"
: > "$T/launchctl.calls"
env "$FAKE" HOME="$H" "$BIN" app-serve uninstall > "$T/uninstall.out" 2>&1; check "uninstall exits" "$?" 0
check "the fake saw the bootout" "$(cat "$T/launchctl.calls")" "bootout gui/$U/$REAL"
[ -f "$PLIST" ] && bad "the job file remains" || ok "the job file is gone"
: > "$T/launchctl.calls"
env "$FAKE" HOME="$H" "$BIN" app-serve uninstall > "$T/uninstall2.out" 2>&1; check "uninstall with nothing installed exits" "$?" 0
grep -q '^not installed' "$T/uninstall2.out" && ok "it says so" || bad "it said: $(cat "$T/uninstall2.out")"
check "and no launchctl call" "$(cat "$T/launchctl.calls")" ""

launchctl print "gui/$U/$REAL" >/dev/null 2>&1 && bad "the real label is loaded" || ok "the real label was never loaded"
[ "$FAILED" = 0 ] && echo "ALL PASSED" || echo "SOME CHECKS FAILED"
exit "$FAILED"
