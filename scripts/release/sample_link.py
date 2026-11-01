#!/usr/bin/env python3
"""Sample the initech link during an install: every interval, read the link and
run what it names. Stops when the stop file appears, then reports.

  sample_link.py <prefix> <out> <stop-file> <old-version> <new-version> [interval-s]

Each sample in <out> is one line: seconds, link target or ENOENT, version printed
or the failure. Exit 0 only if at least one sample was taken and every sample names
the old or the new Cellar binary and that binary ran and said old or new."""
import os, re, subprocess, sys, time


def sample(link):
    """One reading: (target, printed). Target is ENOENT when the link is gone."""
    try:
        target = os.readlink(link)
    except OSError:
        return 'ENOENT', ''
    try:
        out = subprocess.run([link, 'version'], capture_output=True, text=True, timeout=5,
                             stdin=subprocess.DEVNULL)
        return target, out.stdout.strip() if out.returncode == 0 else f'rc={out.returncode}'
    except (OSError, subprocess.SubprocessError) as e:
        return target, f'did not run: {type(e).__name__}'


def bad(target, printed, versions):
    """Why a reading is not one of the two binaries, or None when it is."""
    if target == 'ENOENT':
        return 'link missing'
    m = re.fullmatch(r'\.\./Cellar/initech/([^/]+)/bin/initech', target)
    if not m or m[1] not in versions:
        return f'link names {target}'
    # The link can move between the read and the run, so either version may answer.
    if printed not in {f'initech {v}' for v in versions}:
        return f'binary said {printed!r}'
    return None


def main(argv):
    prefix, out_path, stop, old, new, *rest = argv
    interval = float(rest[0]) if rest else 0.05
    link = os.path.join(prefix, 'bin/initech')
    versions, n, errors, seen = {old, new}, 0, [], {}
    start = time.time()
    with open(out_path, 'w') as out:
        while not os.path.exists(stop):
            target, printed = sample(link)
            n += 1
            seen[target] = seen.get(target, 0) + 1
            why = bad(target, printed, versions)
            if why:
                errors.append(why)
            out.write(f'{time.time() - start:.3f} {target} {printed}\n')
            time.sleep(interval)
    print(f'samples {n}, bad {len(errors)}; ' + ', '.join(f'{k}: {v}' for k, v in sorted(seen.items())))
    for e in errors[:5]:
        print('  ' + e)
    return 0 if n and not errors else 1


if __name__ == '__main__':
    if len(sys.argv) < 6:
        sys.exit(__doc__)
    sys.exit(main(sys.argv[1:]))
