#!/bin/bash
# testflight.sh builds the app at the current checkout and uploads it to App
# Store Connect for TestFlight, headless, as one heavy step under heavy.sh:
#
#   ~/Desktop/Projects/initech/super/tools/heavy.sh app/scripts/testflight.sh <out dir> [--check]
#
# Keys come from initech.yaml app_store_connect, by path only, and are never
# printed: the archive signs with the CI key (key_id, key_path); only the
# export and the upload use the release key (release_key_id,
# release_key_path), an Admin key, because only an Admin key may use the
# cloud-managed distribution certificate. altool finds the release key at its
# standard path, ~/.appstoreconnect/private_keys/AuthKey_<id>.p8.
#
# --check runs the checks only and builds nothing. A check that fails refuses
# with its reason and exit 3: a key file missing, a key file readable by
# anyone else (not mode 600), the release key not where altool looks.
# Each stage stops the script on failure with its exit code and the log path;
# nothing is retried.
set -u
out=${1:?out dir}; check=${2:-}
yaml=${INITECH_YAML:-$HOME/Desktop/Projects/initech/initech.yaml}
app=$(cd "$(dirname "$0")/.." && pwd)

eval "$(python3 - "$yaml" <<'PY'
import yaml, shlex, sys, os
a = (yaml.safe_load(open(sys.argv[1])) or {}).get('app_store_connect') or {}
v = {k: str(a.get(k) or '') for k in ('key_id', 'key_path', 'release_key_id', 'release_key_path', 'issuer_id', 'team_id')}
for k in ('key_path', 'release_key_path'):
    v[k] = os.path.expanduser(v[k])
print(' '.join('%s=%s' % (k.upper(), shlex.quote(x)) for k, x in v.items()))
PY
)" || { echo "testflight: could not read app_store_connect from $yaml" >&2; exit 3; }

refuse() { echo "testflight: refused: $*" >&2; exit 3; }
for k in KEY_ID ISSUER_ID RELEASE_KEY_ID TEAM_ID; do [ -n "${!k}" ] || refuse "app_store_connect.$(echo $k | tr A-Z a-z) is empty"; done
for p in "$KEY_PATH" "$RELEASE_KEY_PATH"; do
  [ -f "$p" ] || refuse "key file missing: $(basename "$p")"
  [ "$(stat -f %Lp "$p")" = 600 ] || refuse "key file $(basename "$p") is not mode 600"
done
[ "$RELEASE_KEY_PATH" = "$HOME/.appstoreconnect/private_keys/AuthKey_$RELEASE_KEY_ID.p8" ] \
  || refuse "the release key is not at ~/.appstoreconnect/private_keys/AuthKey_<release_key_id>.p8, where altool looks"
echo "testflight: checks passed at $(git -C "$app" rev-parse --short HEAD); load now: $(sysctl -n vm.loadavg)"
[ "$check" = "--check" ] && exit 0

mkdir -p "$out" || exit 2
archive="$out/InitechApp.xcarchive"; export_dir="$out/export"
cat > "$out/ExportOptions.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
	<key>method</key><string>app-store-connect</string>
	<key>destination</key><string>export</string>
	<key>teamID</key><string>$TEAM_ID</string>
	<key>signingStyle</key><string>automatic</string>
	<key>manageAppVersionAndBuildNumber</key><false/>
	<key>uploadSymbols</key><true/>
</dict></plist>
EOF

cd "$app" || exit 2
echo "testflight: 1/3 archive (CI key)"
perl -e 'alarm 1080; exec @ARGV' xcodebuild archive -project InitechApp.xcodeproj -scheme InitechApp \
  -configuration Release -destination 'generic/platform=iOS' -archivePath "$archive" \
  -allowProvisioningUpdates -authenticationKeyPath "$KEY_PATH" -authenticationKeyID "$KEY_ID" \
  -authenticationKeyIssuerID "$ISSUER_ID" > "$out/archive.log" 2>&1 \
  || { echo "testflight: archive failed, see $out/archive.log" >&2; exit 4; }

echo "testflight: 2/3 export for App Store Connect (release key)"
perl -e 'alarm 600; exec @ARGV' xcodebuild -exportArchive -archivePath "$archive" -exportPath "$export_dir" \
  -exportOptionsPlist "$out/ExportOptions.plist" -allowProvisioningUpdates \
  -authenticationKeyPath "$RELEASE_KEY_PATH" -authenticationKeyID "$RELEASE_KEY_ID" \
  -authenticationKeyIssuerID "$ISSUER_ID" > "$out/export.log" 2>&1 \
  || { echo "testflight: export failed, see $out/export.log" >&2; exit 5; }

echo "testflight: 3/3 upload (release key)"
perl -e 'alarm 900; exec @ARGV' xcrun altool --upload-app -f "$export_dir/InitechApp.ipa" -t ios \
  --apiKey "$RELEASE_KEY_ID" --apiIssuer "$ISSUER_ID" --output-format json > "$out/upload.log" 2>&1 \
  || { echo "testflight: upload refused, see $out/upload.log" >&2; grep -oE '"description" : "[^"]{0,200}' "$out/upload.log" | sort -u >&2; exit 6; }
build=$(plutil -extract ApplicationProperties.CFBundleVersion raw "$archive/Info.plist")
version=$(plutil -extract ApplicationProperties.CFBundleShortVersionString raw "$archive/Info.plist")
echo "testflight: uploaded build $build (version $version); App Store Connect processes it next"
