#!/bin/sh
# logo_test.sh — tests for scripts/logo.sh, the logo generator.
#
# A logo is not usually something a test suite has an opinion about. This one
# is generated, which changes the question from "is it pretty" to claims that
# can be false: that the four files share one geometry, that the dashed line is
# the mean of the bars rather than a line somebody placed, that nothing in an
# SVG reaches out to the network, and that the numbers on docs/logo.md are the
# numbers the generator actually produces.
#
# The last one is the rule the repository already has — a change lands with its
# documentation in the same commit — mechanised for the one artefact where
# nobody would notice the drift.
#
# POSIX sh and awk only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/logo.sh"
assets="$root/docs/assets"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/edgemix-logo-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}

pass() { echo "ok   $1"; }

# assert_contains <name> <needle> <file>
assert_contains() {
	checks=$((checks + 1))
	if grep -qF "$2" "$3"; then pass "$1"; else
		fail "$1 — expected to find: $2"
	fi
}

# assert_absent <name> <needle> <file>
assert_absent() {
	checks=$((checks + 1))
	if grep -qF "$2" "$3"; then
		fail "$1 — did not expect to find: $2"
	else pass "$1"; fi
}

echo "# the committed files"

checks=$((checks + 1))
if "$script" check >"$tmp/check.out" 2>&1; then
	pass "the committed SVGs match the geometry"
else
	fail "logo.sh check failed — run scripts/logo.sh write"
	sed 's/^/       /' "$tmp/check.out" >&2
fi

# A stale file has to be named, not just reported. Four files share a geometry;
# "something is stale" would send the reader to diff them by hand.
checks=$((checks + 1))
mkdir -p "$tmp/assets"
cp "$assets"/*.svg "$tmp/assets/"
printf '<!-- edited by hand -->\n' >>"$tmp/assets/edgemix-mark.svg"
if LOGO_DIR="$tmp/assets" "$script" check >"$tmp/stale.out" 2>&1; then
	fail "check passed over a hand-edited mark"
else
	if grep -q 'edgemix-mark.svg is stale' "$tmp/stale.out"; then
		pass "a hand-edited file fails the check, named"
	else
		fail "the stale file was not named"
		sed 's/^/       /' "$tmp/stale.out" >&2
	fi
fi

checks=$((checks + 1))
rm -f "$tmp/assets/favicon.svg"
if LOGO_DIR="$tmp/assets" "$script" check >"$tmp/missing.out" 2>&1; then
	fail "check passed with the favicon missing"
else
	if grep -q 'favicon.svg is missing' "$tmp/missing.out"; then
		pass "a missing file fails the check, named"
	else
		fail "the missing file was not named"
	fi
fi

echo "# what is in the drawings"

for f in edgemix-mark.svg favicon.svg edgemix-logo-light.svg edgemix-logo-dark.svg; do
	checks=$((checks + 1))
	if [ -s "$assets/$f" ] && grep -q '</svg>' "$assets/$f"; then
		pass "$f is a closed SVG"
	else
		fail "$f is empty or unterminated"
	fi

	# No dependency, no network — the rule the CI grep enforces for the Go
	# code, applied to the drawings. An SVG can fetch: <image href>, a
	# @import, a <script>, a url() in a style. None of that is here, and the
	# logo is the one asset a page loads before anything else.
	checks=$((checks + 1))
	if grep -qE '<script|<image|xlink:href|@import|url\(|<foreignObject' "$assets/$f"; then
		fail "$f reaches outside itself"
		grep -nE '<script|<image|xlink:href|@import|url\(|<foreignObject' "$assets/$f" | sed 's/^/       /' >&2
	else
		pass "$f fetches nothing and runs nothing"
	fi
done

# Exactly one bar is the peak, in every drawing that has one.
for f in edgemix-mark.svg favicon.svg edgemix-logo-light.svg edgemix-logo-dark.svg; do
	checks=$((checks + 1))
	n=$(grep -c '#10b981' "$assets/$f" || true)
	if [ "$n" -eq 1 ]; then
		pass "$f marks exactly one second as the peak"
	else
		fail "$f has $n peak-coloured elements, expected 1"
	fi
done

assert_contains "the mark carries the mean as a dashed line" \
	'stroke-dasharray' "$assets/edgemix-mark.svg"

# At 16px a dash is a smudge, so the favicon is the mark restated rather than
# the mark scaled. If this ever starts passing, the favicon became the logo
# shrunk.
assert_absent "the favicon leaves the dashed mean out" \
	'stroke-dasharray' "$assets/favicon.svg"

assert_contains "the wordmark pins its advance width" \
	'textLength' "$assets/edgemix-logo-light.svg"

assert_contains "the wordmark needs no font file" \
	'ui-monospace' "$assets/edgemix-logo-light.svg"

# The two lockups are one drawing in two ink colours. If they differ anywhere
# else, one of them was edited by hand and the mark halves have drifted.
checks=$((checks + 1))
diff "$assets/edgemix-logo-light.svg" "$assets/edgemix-logo-dark.svg" >"$tmp/lockup.diff" 2>&1 || true
if [ "$(grep -c '^[<>]' "$tmp/lockup.diff")" -eq 2 ] &&
	grep -q '^[<>].*fill="#1f2328">edgemix' "$tmp/lockup.diff" &&
	grep -q '^[<>].*fill="#e6edf3">edgemix' "$tmp/lockup.diff"; then
	pass "the light and dark lockups differ only in the wordmark colour"
else
	fail "the two lockups differ in more than the wordmark colour"
	sed 's/^/       /' "$tmp/lockup.diff" >&2
fi

echo "# the mean is arithmetic, not artistic"

# The claim the mark makes is that the dashed line is the mean of the bars. The
# bar heights are in the file; so is the line. Recompute one from the other.
checks=$((checks + 1))
if awk '
	/<rect / {
		# The peak rect carries its own fill; the slate ones inherit the group.
		if (match($0, /height="[0-9.]+"/)) {
			h = substr($0, RSTART + 8, RLENGTH - 9) + 0
			sum += h; n++
			if (h > max) max = h
		}
	}
	/<path / && match($0, /M[0-9.]+ [0-9.]+ H/) {
		split(substr($0, RSTART + 1, RLENGTH - 3), p, " ")
		liney = p[2] + 0
	}
	END {
		if (n != 7) { print "expected 7 bars, found " n > "/dev/stderr"; exit 1 }
		# baseline 55, and the drawing scales the tallest bar to its box, so
		# the mean in pixels is the mean of the heights.
		want = 55 - sum / n
		if (want - liney > 0.02 || liney - want > 0.02) {
			printf "the dashed line is at %.2f, the mean of the bars is at %.2f\n", liney, want > "/dev/stderr"
			exit 1
		}
	}
' "$assets/edgemix-mark.svg" 2>"$tmp/mean.out"; then
	pass "the dashed line sits at the arithmetic mean of the seven bars"
else
	fail "the dashed line is not the mean of the bars"
	sed 's/^/       /' "$tmp/mean.out" >&2
fi

echo "# the documentation says what the generator does"

# docs/logo.md quotes the geometry. The rule in AGENTS.md is that a change
# lands with its documentation; for a generated drawing nobody would notice
# the drift, so it is asserted instead.
"$script" geometry >"$tmp/geometry.txt"
bars=$(sed -n 's/^bars *[0-9]* seconds: //p' "$tmp/geometry.txt")
mean=$(sed -n 's/^mean *//p' "$tmp/geometry.txt" | sed 's/ req\/s//')
assert_contains "docs/logo.md quotes the bar values the generator uses" \
	"$bars" "$root/docs/logo.md"
assert_contains "docs/logo.md quotes the mean the generator computes" \
	"$mean" "$root/docs/logo.md"

for f in edgemix-mark.svg edgemix-logo-light.svg edgemix-logo-dark.svg favicon.svg; do
	assert_contains "docs/logo.md lists $f" "$f" "$root/docs/logo.md"
done

echo ""
if [ "$failures" -eq 0 ]; then
	echo "logo_test.sh: $checks checks, all passed"
else
	echo "logo_test.sh: $checks checks, $failures failed" >&2
	exit 1
fi
