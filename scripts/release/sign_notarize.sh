#!/bin/bash
# Signs and notarises the darwin binaries of a DRAFT release, then replaces
# them, and checksums.txt, in that draft. It never publishes: publishing is the
# next step, and a published release refuses here, so no unsigned darwin asset
# is ever public.
#
#   sign_notarize.sh <tag> [--config <initech.yaml>]
#
# Steps, each refusing with its reason (exit 1) and leaving the draft as it was:
#   1. the release at <tag> is a draft
#   2. the notarising key: key_path, key_id and issuer_id from the config's
#      app_store_connect; the key file is mode 0600. The key goes to notarytool
#      by path only; none of the three is ever printed.
#   3. the darwin tarballs and checksums.txt, each tarball matching its line
#   4. codesign with the pinned Developer ID identity, the fixed identifier,
#      the hardened runtime and a secure timestamp; codesign -dvv read back must
#      show the identifier, the team, the runtime flag and a timestamp
#   5. notarytool submit --wait, bounded; anything but Accepted refuses, and its
#      log is saved to a file named in the output
#   6. the tarballs re-packed with the same files, a new checksums.txt (other
#      lines unchanged), every re-packed file checked against it, then uploaded
#      over the draft's: nothing is replaced until its replacement has passed
set -u
IDENTIFIER=com.neblihq.initech
TEAM=K7Z8YKVA8G
# The Developer ID Application certificate signing releases, by SHA-1: the
# keychain holds two for this team, so a name would be ambiguous.
IDENTITY=E73758A491D91D088ADB8BC50B04CCB49E0351A4
REPO=${REPO:-nmelo/initech}
GH=${GH:-gh}
CODESIGN=${CODESIGN:-codesign}
SECURITY=${SECURITY:-security}
NOTARY_LIMIT=${NOTARY_LIMIT:-1800}
# codesign answers in seconds; one still running after this is waiting on a
# keychain prompt, which must never sit on the operator's screen.
CODESIGN_LIMIT=${CODESIGN_LIMIT:-120}
read -r -a NOTARYTOOL <<<"${NOTARYTOOL:-xcrun notarytool}"

TAG=${1:-}
[ -n "$TAG" ] || { echo "usage: sign_notarize.sh <tag> [--config <initech.yaml>]"; exit 2; }
shift
CONFIG=initech.yaml
[ "${1:-}" = --config ] && CONFIG=${2:?--config needs a file}

refuse() { echo "REFUSED: $*; the draft is unchanged"; exit 1; }

STAGE=$(mktemp -d "${TMPDIR:-/tmp}/sign-notarize.XXXX") || exit 1
trap 'rm -rf "${STAGE:?}"' EXIT
mkdir -p "$STAGE/in" "$STAGE/out" "$STAGE/bin"

# 1. A draft only.
draft=$("$GH" release view "$TAG" --repo "$REPO" --json isDraft -q .isDraft 2>/dev/null)
[ "$draft" = true ] || refuse "release $TAG is published or missing (isDraft: '${draft}'); the signing step signs a draft only"

# 2. The key, by path. The values stay in this process; nothing prints them.
CREDS=$(python3 - "$CONFIG" <<'PY'
import os, stat, sys
def block(path):
    # The app_store_connect mapping of initech.yaml, read with the standard
    # library only (a release runner has no YAML module): the top-level key,
    # then its indented "key: value" lines, quotes and trailing comments dropped.
    out, inside = {}, False
    for raw in open(path, encoding='utf-8'):
        line = raw.rstrip('\n')
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        if not line[0].isspace():
            inside = line.split('#', 1)[0].strip() == 'app_store_connect:'
            continue
        if inside and ':' in line:
            k, v = line.strip().split(':', 1)
            v = v.split(' #', 1)[0].strip()
            if len(v) >= 2 and v[0] == v[-1] and v[0] in (chr(34), chr(39)):
                v = v[1:-1]
            out[k.strip()] = v
    return out
try:
    a = block(sys.argv[1])
except (OSError, UnicodeDecodeError) as e:
    print(f'ERR cannot read the config: {type(e).__name__}'); sys.exit(0)
missing = [k for k in ('key_path', 'key_id', 'issuer_id') if not a.get(k)]
if missing:
    print('ERR app_store_connect lacks ' + ', '.join(missing)); sys.exit(0)
p = os.path.expanduser(str(a['key_path']))
if not os.path.isfile(p):
    print('ERR the key file named by app_store_connect key_path does not exist'); sys.exit(0)
mode = stat.S_IMODE(os.stat(p).st_mode)
if mode != 0o600:
    print(f'ERR the key file has mode {mode:o}, not 600'); sys.exit(0)
print(p); print(a['key_id']); print(a['issuer_id'])
PY
) || refuse "the config reader failed (python exited non-zero); nothing signed"
case "$CREDS" in ERR*) refuse "${CREDS#ERR }";; esac
{ read -r KEY_PATH; read -r KEY_ID; read -r ISSUER; } <<<"$CREDS"
[ -n "$KEY_PATH" ] && [ -n "$KEY_ID" ] && [ -n "$ISSUER" ] || refuse "the config reader returned no key path, key id or issuer"
unset CREDS

# 3. The darwin assets, each matching its checksums line.
"$GH" release download "$TAG" --repo "$REPO" --dir "$STAGE/in" \
  --pattern 'initech_darwin_*.tar.gz' --pattern checksums.txt >/dev/null 2>&1 \
  || refuse "could not download the draft's darwin assets"
[ -f "$STAGE/in/checksums.txt" ] || refuse "the draft has no checksums.txt"
DARWIN=$(grep -oE 'initech_darwin_[a-z0-9]+\.tar\.gz$' "$STAGE/in/checksums.txt" | sort -u)
[ -n "$DARWIN" ] || refuse "checksums.txt names no darwin tarball"
for n in $DARWIN; do
  [ -f "$STAGE/in/$n" ] || refuse "$n is in checksums.txt and not in the draft"
  want=$(awk -v n="$n" '$2 == n {print $1}' "$STAGE/in/checksums.txt")
  got=$(shasum -a 256 "$STAGE/in/$n" | awk '{print $1}')
  [ "$want" = "$got" ] || refuse "$n does not match its checksums.txt line"
done

# 4. Sign, then read back.
"$SECURITY" find-identity -v -p codesigning 2>/dev/null | grep -q " $IDENTITY \"Developer ID Application: .*($TEAM)\"" \
  || refuse "the signing identity $IDENTITY of team $TEAM is not a valid identity in the keychain"
for n in $DARWIN; do
  d=$STAGE/bin/${n%.tar.gz}; mkdir -p "$d"
  tar xzf "$STAGE/in/$n" -C "$d" || refuse "could not unpack $n"
  [ -f "$d/initech" ] || refuse "$n holds no initech binary"
  "$CODESIGN" -f -s "$IDENTITY" -i "$IDENTIFIER" -o runtime --timestamp "$d/initech" </dev/null >"$STAGE/codesign.out" 2>&1 &
  cp_=$!
  for ((t=0; t<CODESIGN_LIMIT*5; t++)); do kill -0 "$cp_" 2>/dev/null || break; sleep 0.2; done
  if kill -0 "$cp_" 2>/dev/null; then
    for p in $(pgrep -P "$cp_") "$cp_"; do kill "$p" 2>/dev/null; done
    refuse "codesign did not finish within ${CODESIGN_LIMIT}s on $n (a keychain prompt may be waiting): stopped, no retry"
  fi
  wait "$cp_" || refuse "codesign failed on $n: $(tail -1 "$STAGE/codesign.out")"
  info=$("$CODESIGN" -dvv "$d/initech" 2>&1) || refuse "codesign could not read back $n"
  grep -qx "Identifier=$IDENTIFIER" <<<"$info" || refuse "$n read back $(grep '^Identifier=' <<<"$info"), not Identifier=$IDENTIFIER"
  grep -qx "TeamIdentifier=$TEAM" <<<"$info" || refuse "$n read back $(grep '^TeamIdentifier=' <<<"$info"), not TeamIdentifier=$TEAM"
  grep -q 'flags=0x[0-9a-f]*(.*runtime' <<<"$info" || refuse "$n has no hardened runtime flag"
  grep -q '^Timestamp=' <<<"$info" || refuse "$n has no secure timestamp"
  echo "signed $n: Identifier=$IDENTIFIER TeamIdentifier=$TEAM runtime, timestamped"
done

# 5. Notarise both binaries in one submission, bounded.
(cd "$STAGE/bin" && /usr/bin/ditto -c -k --keepParent . "$STAGE/notarize.zip") || refuse "could not zip the binaries"
"${NOTARYTOOL[@]}" submit "$STAGE/notarize.zip" --key "$KEY_PATH" --key-id "$KEY_ID" --issuer "$ISSUER" \
  --wait --timeout "${NOTARY_LIMIT}s" --output-format json </dev/null >"$STAGE/notary.json" 2>"$STAGE/notary.err" &
np=$!
for ((t=0; t<(NOTARY_LIMIT+30)*5; t++)); do kill -0 "$np" 2>/dev/null || break; sleep 0.2; done
if kill -0 "$np" 2>/dev/null; then
  for p in $(pgrep -P "$np") "$np"; do kill "$p" 2>/dev/null; done
  refuse "notarytool did not answer within the bound of ${NOTARY_LIMIT}s"
fi
wait "$np"
# The last line of its output that is a JSON object is the answer.
read -r SUB STATUS < <(python3 -c '
import json, sys
d = {}
for line in open(sys.argv[1], errors="replace"):
    try:
        v = json.loads(line)
    except ValueError:
        continue
    if isinstance(v, dict):
        d = v
print(str(d.get("id") or "-").replace(" ", "_"), d.get("status") or "no-status")' "$STAGE/notary.json")
if [ "$STATUS" != Accepted ]; then
  LOG=${TMPDIR:-/tmp}/notarytool-$SUB.json
  if [ "$SUB" != - ]; then
    "${NOTARYTOOL[@]}" log "$SUB" --key "$KEY_PATH" --key-id "$KEY_ID" --issuer "$ISSUER" </dev/null >"$LOG" 2>/dev/null
    echo "notarytool log saved to $LOG"
  fi
  refuse "notarytool answered $STATUS for submission $SUB, not Accepted"
fi
echo "notarised: submission $SUB Accepted"

# 6. Re-pack, new checksums, check every re-packed file, then upload.
cp "$STAGE/in/checksums.txt" "$STAGE/out/checksums.txt"
for n in $DARWIN; do
  d=$STAGE/bin/${n%.tar.gz}
  files=$(tar tzf "$STAGE/in/$n") || refuse "could not list $n"
  # macOS tar adds ._ files for extended attributes unless told not to; the
  # published tarball holds exactly the files CI packed.
  # shellcheck disable=SC2086
  COPYFILE_DISABLE=1 tar czf "$STAGE/out/$n" --no-mac-metadata --no-xattrs -C "$d" $files || refuse "could not re-pack $n"
  [ "$(tar tzf "$STAGE/out/$n" | sort)" = "$(sort <<<"$files")" ] || refuse "the re-packed $n holds other files than CI packed"
  sum=$(shasum -a 256 "$STAGE/out/$n" | awk '{print $1}')
  python3 - "$STAGE/out/checksums.txt" "$n" "$sum" <<'PY' || refuse "could not write the new checksums line for $n"
import sys
path, name, s = sys.argv[1:]
lines = open(path).read().splitlines()
hits = [i for i, l in enumerate(lines) if l.split()[-1:] == [name]]
if len(hits) != 1:
    sys.exit(1)
lines[hits[0]] = f'{s}  {name}'
open(path, 'w').write('\n'.join(lines) + '\n')
PY
done
[ -n "${REPACK_HOOK:-}" ] && eval "$REPACK_HOOK"
for n in $DARWIN; do
  want=$(awk -v n="$n" '$2 == n {print $1}' "$STAGE/out/checksums.txt")
  got=$(shasum -a 256 "$STAGE/out/$n" | awk '{print $1}')
  [ "$want" = "$got" ] || refuse "the re-packed $n does not match the new checksums.txt"
done
diff <(grep -v 'initech_darwin_' "$STAGE/in/checksums.txt") <(grep -v 'initech_darwin_' "$STAGE/out/checksums.txt") >/dev/null \
  || refuse "the new checksums.txt changed a line that is not darwin"

uploads=()
for n in $DARWIN; do uploads+=("$STAGE/out/$n"); done
"$GH" release upload "$TAG" --repo "$REPO" --clobber "${uploads[@]}" "$STAGE/out/checksums.txt" >/dev/null 2>&1 \
  || { echo "FAILED: the upload to the draft did not complete; the draft may hold a mix, and stays a draft: run this step again"; exit 1; }
for n in $DARWIN; do echo "replaced $n: sha256 $(awk -v n="$n" '$2 == n {print $1}' "$STAGE/out/checksums.txt")"; done
echo "draft $TAG holds signed, notarised darwin binaries; publish is the next step"
