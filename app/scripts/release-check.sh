#!/bin/bash
# release-check.sh proves the debug launch options are compiled out of the
# app's Release build: no file of the Release app holds any option's name, and
# every name is in the Debug app (its twin: a search that could not find a name
# where it is known to be says nothing about where it is not).
#
#   app/scripts/release-check.sh check <Release .app> <Debug .app>
#   app/scripts/release-check.sh build <derived data dir>
#
# The names are read from the app's source: every "-...debug-<name>" literal under
# app/InitechApp and app/InitechCore/Sources. A new option is checked the day
# it is written; no list is kept here. `build` builds both configurations for a
# device, unsigned, with no simulator, and then checks them.
#
# Exit 0: Release holds none, Debug holds all. Exit 1: a name is in Release, or
# missing from Debug. Exit 2: nothing to check with (no names in the source, an
# app that is missing or empty, a bad argument). One line on stderr each.
set -u
here=$(cd "$(dirname "$0")" && pwd); app=${APP_DIR:-$(cd "$here/.." && pwd)}
mode=${1:-}

names=$(grep -rhoE '"-[a-z-]*debug-[a-z]+"' "$app/InitechApp" "$app/InitechCore/Sources" 2>/dev/null | tr -d '"' | sort -u)
[ -n "$names" ] || { echo "release-check: no -initech-debug- option name found in the source under $app: nothing to check with. Refused." >&2; exit 2; }
# A name of 15 bytes or fewer is not stored as text in an optimised build (the compiler keeps a short
# string inside the instructions), so a search of the Release app could never find it: with
# "-debug-focus" compiled into Release on purpose, this check passed. Every name must be longer.
for n in $names; do
  [ ${#n} -gt 15 ] || { echo "release-check: the option name $n is ${#n} bytes: a name of 15 or fewer cannot be seen in a Release build, so its absence would prove nothing. Give it a longer name. Refused." >&2; exit 2; }
done

check() { # <Release .app> <Debug .app>
  local rel=$1 dbg=$2 bad=0 n
  for d in "$rel" "$dbg"; do
    [ -d "$d" ] && [ -n "$(find "$d" -type f -size +0 2>/dev/null | head -1)" ] \
      || { echo "release-check: $d is not an app with files in it. Refused." >&2; exit 2; }
  done
  for n in $names; do
    if grep -rqaF -- "$n" "$rel"; then
      echo "release-check: the Release app holds $n ($(grep -rlaF -- "$n" "$rel" | head -1 | sed "s#^$rel/##")): a debug launch option is compiled into the build that ships." >&2
      bad=1
    fi
    if ! grep -rqaF -- "$n" "$dbg"; then
      echo "release-check: the Debug app does not hold $n: the search cannot see this name, so its absence from Release proves nothing." >&2
      bad=1
    fi
  done
  [ "$bad" = 0 ] || exit 1
  echo "release-check: Release holds none of $(echo "$names" | wc -l | tr -d ' ') debug launch options; Debug holds them all: $(echo $names)"
}

case "$mode" in
  check)
    [ $# -eq 3 ] || { echo "release-check: check takes the Release app and the Debug app." >&2; exit 2; }
    check "$2" "$3" ;;
  build)
    dd=${2:?a derived data dir}
    for cfg in Release Debug; do
      ( cd "$app" && xcodebuild build -project InitechApp.xcodeproj -scheme InitechApp -configuration "$cfg" \
          -destination 'generic/platform=iOS' -derivedDataPath "$dd" CODE_SIGNING_ALLOWED=NO > "$dd-$cfg.log" 2>&1 ) \
        || { echo "release-check: the $cfg build failed; log $dd-$cfg.log" >&2; exit 2; }
    done
    check "$dd/Build/Products/Release-iphoneos/InitechApp.app" "$dd/Build/Products/Debug-iphoneos/InitechApp.app" ;;
  *) echo "release-check: check <Release .app> <Debug .app>, or build <derived data dir>" >&2; exit 2 ;;
esac
