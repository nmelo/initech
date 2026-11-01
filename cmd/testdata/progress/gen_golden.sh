#!/bin/sh
# Regenerates golden.txt: theguy's progress.py run on this fixture, with a
# fake bd and gh answering from these files and the clock fixed at
# 2026-10-31T22:00:00Z, prefix fx-, six hours. Not run by the tests; run by
# hand when the fixture or the script changes:
#   sh cmd/testdata/progress/gen_golden.sh ~/Desktop/Projects/theguy/skills/progress-tracking/scripts/progress.py
set -eu
script=${1:?path to progress.py}
here=$(cd "$(dirname "$0")" && pwd)
bin=$(mktemp -d)
trap 'rm -rf "${bin:?}"' EXIT
cat > "$bin/bd" <<BD
#!/bin/sh
case "\$1" in
list) cat "$here/list.json" ;;
show) python3 -I -c 'import json,sys; print(json.dumps([b for b in json.load(open(sys.argv[1])) if b["id"]==sys.argv[2]]))' "$here/list.json" "\$2" ;;
comments) cat "$here/comments/\$2.json" ;;
*) exit 1 ;;
esac
BD
printf '#!/bin/sh\ncat "%s/commits.txt"\n' "$here" > "$bin/gh"
chmod +x "$bin/bd" "$bin/gh"
PATH="$bin:$PATH" python3 -I - "$script" > "$here/golden.txt" <<'PY'
import datetime as real, importlib.util, sys, types
script = sys.argv[1]
class Fixed(real.datetime):
    @classmethod
    def now(cls, tz=None):
        t = real.datetime(2026, 10, 31, 22, 0, 0, tzinfo=real.timezone.utc)
        return t.astimezone(tz) if tz else t
shim = types.SimpleNamespace(datetime=Fixed, timezone=real.timezone, timedelta=real.timedelta)
spec = importlib.util.spec_from_file_location("progress", script)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
mod.dt = shim
sys.argv = ["progress.py", "initech", "fx-", "nmelo/initech", "6"]
mod.main()
PY
