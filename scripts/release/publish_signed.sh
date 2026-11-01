#!/bin/bash
# Publishes a DRAFT release whose darwin binaries sign_notarize.sh has signed,
# then pushes the tap formula with the published checksums, in that order: no
# unsigned darwin binary is ever public, and brew never points at one.
#
#   publish_signed.sh <tag>                 check, publish, push the formula
#   publish_signed.sh <tag> --formula-only  the release is already published (a
#                                           push that failed): check, push only
#
# Checks, each refusing with its reason (exit 1) and leaving the draft a draft:
#   the release is a draft (or published, with --formula-only)
#   each darwin tarball matches checksums.txt, and its binary passes
#   codesign --verify --strict --deep and reads back the fixed identifier and team
#   the release workflow run for the tag succeeded and left its formula artifact
#   that formula is for this version, names each darwin tarball once, and its
#   linux checksums equal checksums.txt
set -u
IDENTIFIER=com.neblihq.initech
TEAM=K7Z8YKVA8G
REPO=${REPO:-nmelo/initech}
TAP_URL=${TAP_URL:-https://github.com/nmelo/homebrew-tap.git}
GH=${GH:-gh}
CODESIGN=${CODESIGN:-codesign}

TAG=${1:-}
[ -n "$TAG" ] || { echo "usage: publish_signed.sh <tag> [--formula-only]"; exit 2; }
VER=${TAG#v}
ONLY=0; [ "${2:-}" = --formula-only ] && ONLY=1

hold() { echo "HELD: $*; the release stays as it was and the tap is untouched"; exit 1; }

STAGE=$(mktemp -d "${TMPDIR:-/tmp}/publish-signed.XXXX") || exit 1
trap 'rm -rf "${STAGE:?}"' EXIT
mkdir -p "$STAGE/in" "$STAGE/bin" "$STAGE/formula"

draft=$("$GH" release view "$TAG" --repo "$REPO" --json isDraft -q .isDraft 2>/dev/null)
if [ "$ONLY" = 0 ]; then
  [ "$draft" = true ] || hold "release $TAG is already published or missing (isDraft: '${draft}'); use --formula-only to finish its formula"
else
  [ "$draft" = false ] || hold "release $TAG is not published (isDraft: '${draft}'); --formula-only finishes a published release only"
fi

# The darwin binaries: published as they will be, and signed.
"$GH" release download "$TAG" --repo "$REPO" --dir "$STAGE/in" \
  --pattern 'initech_*.tar.gz' --pattern checksums.txt >/dev/null 2>&1 || hold "could not download the release assets"
[ -f "$STAGE/in/checksums.txt" ] || hold "the release has no checksums.txt"
DARWIN=$(grep -oE 'initech_darwin_[a-z0-9]+\.tar\.gz$' "$STAGE/in/checksums.txt" | sort -u)
[ -n "$DARWIN" ] || hold "checksums.txt names no darwin tarball"
for n in $DARWIN; do
  [ -f "$STAGE/in/$n" ] || hold "$n is in checksums.txt and not in the release"
  want=$(awk -v n="$n" '$2 == n {print $1}' "$STAGE/in/checksums.txt")
  got=$(shasum -a 256 "$STAGE/in/$n" | awk '{print $1}')
  [ "$want" = "$got" ] || hold "$n does not match its checksums.txt line"
  d=$STAGE/bin/${n%.tar.gz}; mkdir -p "$d"
  tar xzf "$STAGE/in/$n" -C "$d" || hold "could not unpack $n"
  b=$d/initech
  [ -f "$b" ] || hold "$n holds no initech binary"
  "$CODESIGN" -dvv "$b" >"$STAGE/dvv" 2>&1 || hold "$n: the binary is not signed"
  "$CODESIGN" --verify --strict --deep "$b" >"$STAGE/verify" 2>&1 || hold "$n: the signature does not verify: $(tail -1 "$STAGE/verify")"
  grep -qx "Identifier=$IDENTIFIER" "$STAGE/dvv" || hold "$n reads back $(grep '^Identifier=' "$STAGE/dvv"), not Identifier=$IDENTIFIER"
  grep -qx "TeamIdentifier=$TEAM" "$STAGE/dvv" || hold "$n reads back $(grep '^TeamIdentifier=' "$STAGE/dvv"), not TeamIdentifier=$TEAM"
  echo "verified $n: signed, Identifier=$IDENTIFIER TeamIdentifier=$TEAM"
done

# The formula CI built, rewritten to the published checksums and checked.
read -r RID CONC < <("$GH" run list --repo "$REPO" --workflow release.yml --branch "$TAG" -L 1 \
  --json databaseId,conclusion -q '.[] | "\(.databaseId) \(.conclusion)"' 2>/dev/null)
[ -n "${RID:-}" ] && [ "${CONC:-}" = success ] || hold "no successful release run for $TAG (run '${RID:-}' conclusion '${CONC:-}')"
"$GH" run download "$RID" --repo "$REPO" -n homebrew-formula -D "$STAGE/formula" >/dev/null 2>&1 \
  || hold "release run $RID left no homebrew-formula artifact"
F=$(find "$STAGE/formula" -name initech.rb -type f | head -1)
[ -n "$F" ] || hold "the homebrew-formula artifact holds no initech.rb"
python3 - "$F" "$STAGE/in/checksums.txt" "$TAG" "$VER" <<'PY' || hold "the formula does not fit this release (reason above)"
import re, sys
path, sums_path, tag, ver = sys.argv[1:]
text = open(path).read()
sums = {}
for line in open(sums_path):
    parts = line.split()
    if len(parts) == 2:
        sums[parts[1]] = parts[0]
if not re.search(r'^\s*version "' + re.escape(ver) + r'"\s*$', text, re.M):
    print(f'formula: no version "{ver}" line'); sys.exit(1)
lines = text.split('\n')
for name, want in sums.items():
    if not name.endswith('.tar.gz') or '_windows_' in name:
        continue
    url = f'/releases/download/{tag}/{name}"'
    hits = [i for i, l in enumerate(lines) if l.strip().startswith('url "') and l.rstrip().endswith(url)]
    if len(hits) != 1:
        print(f'formula: {name} is named {len(hits)} times, not once'); sys.exit(1)
    i = hits[0] + 1
    m = re.match(r'^(\s*)sha256 "([0-9a-f]{64})"\s*$', lines[i]) if i < len(lines) else None
    if not m:
        print(f'formula: no sha256 line after the url of {name}'); sys.exit(1)
    if '_darwin_' in name:
        lines[i] = f'{m[1]}sha256 "{want}"'
    elif m[2] != want:
        print(f'formula: {name} checksum {m[2][:12]} is not checksums.txt {want[:12]}'); sys.exit(1)
open(path, 'w').write('\n'.join(lines))
PY

# Publish, then the formula.
if [ "$ONLY" = 0 ]; then
  "$GH" release edit "$TAG" --repo "$REPO" --draft=false --latest >/dev/null 2>&1 \
    || hold "the publish call failed; the release is a draft still"
  [ "$("$GH" release view "$TAG" --repo "$REPO" --json isDraft -q .isDraft 2>/dev/null)" = false ] \
    || hold "the release still reads as a draft after the publish call"
  echo "published $TAG"
fi
git clone -q --depth 1 "$TAP_URL" "$STAGE/tap" 2>/dev/null \
  || { echo "FAILED: could not clone the tap; $TAG is published, its formula is not: run publish_signed.sh $TAG --formula-only"; exit 1; }
cp "$F" "$STAGE/tap/Formula/initech.rb"
if git -C "$STAGE/tap" diff --quiet; then
  echo "the tap formula already describes $TAG; nothing pushed"
  exit 0
fi
git -C "$STAGE/tap" add Formula/initech.rb
git -C "$STAGE/tap" commit -q -m "Brew formula update for initech version $TAG (signed darwin binaries)" \
  && git -C "$STAGE/tap" push -q origin HEAD 2>/dev/null \
  || { echo "FAILED: the formula push did not complete; $TAG is published, its formula is not: run publish_signed.sh $TAG --formula-only"; exit 1; }
for n in $DARWIN; do echo "formula $n: sha256 $(awk -v n="$n" '$2 == n {print $1}' "$STAGE/in/checksums.txt")"; done
echo "tap formula pushed for $TAG"
