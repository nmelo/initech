#!/bin/sh
# private-scan: refuse names that belong only to a private rig in this
# public repo.
#
#   scripts/private-scan.sh --diff   < unified diff   check the added lines
#   scripts/private-scan.sh --tree                    check every tracked file
#
# The list is never in the repo: one Perl-compatible regex per line, matched
# without regard to case, from $INITECH_PRIVATE_TERMS or else
# ~/.config/initech/private-terms (mode 0600). Blank lines and lines starting
# with # are skipped. It names what only a private rig has (its bead prefixes,
# workspace paths, hosts, repo, people's and agent names), never a bare word
# the product uses for itself.
#
# A hit prints "path:line: pattern N" and never the matched text, so a run
# can never copy a term into a terminal log or a CI page. Any hit exits 1.
# A missing list, a list with no pattern, or a pattern that is not a valid
# regex also exits 1, with one line: a scan with nothing to look for
# would pass everything.
#
# scripts/hooks/reference-transaction runs --diff on every branch update, so
# a commit cannot skip it with --no-verify; make private-scan runs --tree for
# the whole-tree report.
set -u

list="${INITECH_PRIVATE_TERMS:-${HOME:-}/.config/initech/private-terms}"
where="the list of private terms ($list, one Perl-compatible regex per line, mode 0600)"

fail() {
	echo "private-scan: $*" >&2
	exit 1
}

mode="${1:-}"
case "$mode" in
--diff | --tree) ;;
*) fail "usage: private-scan.sh --diff (unified diff on stdin) | --tree" ;;
esac

[ -f "$list" ] && [ -r "$list" ] || fail "$where is missing: nothing can be checked, so nothing passes"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/private-scan.XXXXXX") || fail "cannot make a scratch folder"
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

# The patterns, numbered by their line in the list.
awk '!/^[[:space:]]*(#|$)/ { print NR "\t" $0 }' "$list" >"$tmp/patterns"
[ -s "$tmp/patterns" ] || fail "$where holds no pattern: nothing can be checked, so nothing passes"

# ONE DIALECT, PERL'S, IN BOTH MODES. git grep -E and grep -E differ here:
# git's ERE has no \b, so a word-bounded pattern compiled and silently
# matched nothing in --tree while --diff found it. Perl runs --diff and
# git grep -P runs --tree, and both read \b, \d and (?i) alike.
command -v perl >/dev/null 2>&1 || fail "needs perl, which reads the patterns"
# Every pattern must compile. Never print it.
while IFS="$(printf '\t')" read -r n pat; do
	perl -e 'eval { qr/$ARGV[0]/ }; exit($@ ? 2 : 0)' "$pat" 2>/dev/null ||
		fail "pattern $n in $where is not a valid Perl-compatible regex"
done <"$tmp/patterns"

hits="$tmp/hits"
: >"$hits"

if [ "$mode" = "--diff" ]; then
	# Added lines only: "path<TAB>line" in where, the text in text, row for row.
	awk -v W="$tmp/where" -v T="$tmp/text" '
		/^\+\+\+ / { path = substr($0, 5); sub(/^b\//, "", path); next }
		/^@@ /     { split($3, a, ","); ln = substr(a[1], 2) + 0; next }
		/^\+/      { print path "\t" ln > W; print substr($0, 2) > T; ln++; next }
		/^-/       { next }
		/^ /       { ln++ }
	' || fail "cannot read the diff"
	[ -f "$tmp/text" ] || exit 0
	perl -e '
		my ($pf, $wf, $tf) = @ARGV;
		open my $P, "<", $pf or exit 2; open my $W, "<", $wf or exit 2; open my $T, "<", $tf or exit 2;
		my @w = <$W>; my @t = <$T>; chomp(@w, @t);
		while (my $l = <$P>) {
			chomp $l; my ($n, $pat) = split /\t/, $l, 2; my $re = qr/$pat/i;
			for my $i (0 .. $#t) {
				next unless $t[$i] =~ $re;
				my ($path, $line) = split /\t/, $w[$i], 2;
				print "$path:$line: pattern $n\n";
			}
		}
	' "$tmp/patterns" "$tmp/where" "$tmp/text" >>"$hits" || fail "cannot check the diff"
else
	git rev-parse --is-inside-work-tree >/dev/null 2>&1 || fail "--tree runs inside a git work tree"
	# git grep exits 0 on a hit and 1 on none; anything else (a git built
	# without PCRE, a bad tree) is a scan that did not run, never a clean one.
	while IFS="$(printf '\t')" read -r n pat; do
		git grep -I -n -z -i -P -e "$pat" >"$tmp/grep" 2>/dev/null
		rc=$?
		[ "$rc" -le 1 ] || fail "git grep could not run pattern $n (exit $rc): nothing was checked"
		tr '\000' '\t' <"$tmp/grep" | awk -F'\t' -v n="$n" '{ print $1 ":" $2 ": pattern " n }' >>"$hits"
	done <"$tmp/patterns"
fi

if [ -s "$hits" ]; then
	sort -u "$hits" >&2
	fail "$(sort -u "$hits" | wc -l | tr -d ' ') line(s) match $where; the matched text is not shown. Rewrite them to placeholders"
fi
exit 0
