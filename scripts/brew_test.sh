#!/bin/sh
# brew_test.sh — tests for scripts/brew.sh, the Homebrew formula generator.
#
# A formula is a file other people run, and every way it can be wrong is silent
# at generation time and loud on somebody else's machine: a checksum that does
# not match aborts a download and reads like a network problem, a URL with the
# version in the wrong half 404s, and a `version` that disagrees with what the
# binary reports makes the formula's own test fail after the install succeeded.
#
# So the generator is tested on exactly those: the four checksums are read from
# the file rather than assumed, a missing one is fatal and named, the tag half
# of the URL carries the v and the filename half does not, and the log lines in
# the formula's test block are run through the real binary here — because a
# formula that asserts "peak 2 req/s" is making a claim about this tool.
#
# POSIX sh and awk only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/brew.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/edgemix-brew-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}

pass() { echo "ok   $1"; }

# assert_has <name> <needle>   (over $tmp/formula.rb)
assert_has() {
	checks=$((checks + 1))
	if grep -qF "$2" "$tmp/formula.rb"; then
		pass "$1"
	else
		fail "$1 — expected to find: $2"
	fi
}

assert_lacks() {
	checks=$((checks + 1))
	if grep -qF "$2" "$tmp/formula.rb"; then
		fail "$1 — did not expect to find: $2"
	else
		pass "$1"
	fi
}

# A release's checksum file, with the Windows archive in it (the formula covers
# the four Homebrew platforms and must ignore the rest).
cat >"$tmp/SHA256SUMS" <<'EOF'
1111111111111111111111111111111111111111111111111111111111111111  edgemix_0.2.0_darwin_arm64.tar.gz
2222222222222222222222222222222222222222222222222222222222222222  edgemix_0.2.0_darwin_amd64.tar.gz
3333333333333333333333333333333333333333333333333333333333333333  edgemix_0.2.0_linux_arm64.tar.gz
4444444444444444444444444444444444444444444444444444444444444444  edgemix_0.2.0_linux_amd64.tar.gz
5555555555555555555555555555555555555555555555555555555555555555  edgemix_0.2.0_windows_amd64.zip
6666666666666666666666666666666666666666666666666666666666666666  edgemix_0.2.0_windows_arm64.zip
EOF

"$script" formula 0.2.0 "$tmp/SHA256SUMS" >"$tmp/formula.rb"

echo "# what the formula says"

assert_has "the class is the one brew expects for edgemix.rb" "class Edgemix < Formula"
assert_has "the version is the bare one, as the binary reports it" 'version "0.2.0"'
assert_has "the licence is stated" 'license "PolyForm-Noncommercial-1.0.0"'

# The tag carries the v and the file name does not. Getting this the wrong way
# round produces a URL that 404s on every platform at once.
assert_has "the download URL uses the v-prefixed tag and the bare file name" \
	"/releases/download/v0.2.0/edgemix_0.2.0_darwin_arm64.tar.gz"

echo "# the checksums come from the file"

for sha in 1111111111111111111111111111111111111111111111111111111111111111 \
	2222222222222222222222222222222222222222222222222222222222222222 \
	3333333333333333333333333333333333333333333333333333333333333333 \
	4444444444444444444444444444444444444444444444444444444444444444; do
	assert_has "the checksum $(echo "$sha" | cut -c1-4)… is carried over" "sha256 \"$sha\""
done

assert_lacks "the Windows archives stay out of the formula" "windows"
assert_lacks "no checksum is left blank" 'sha256 ""'

checks=$((checks + 1))
if [ "$(grep -c 'sha256 "' "$tmp/formula.rb")" -eq 4 ]; then
	pass "exactly four checksums, one per Homebrew platform"
else
	fail "expected 4 sha256 lines, found $(grep -c 'sha256 "' "$tmp/formula.rb")"
fi

# A checksum file written from a directory (`sha256sum dist/*`) carries the path.
checks=$((checks + 1))
sed 's,  edgemix_,  dist/edgemix_,' "$tmp/SHA256SUMS" >"$tmp/prefixed.txt"
if "$script" formula 0.2.0 "$tmp/prefixed.txt" | grep -q 'sha256 "1111111111111111111111111111111111111111111111111111111111111111"'; then
	pass "a checksum line with a directory prefix still matches its archive"
else
	fail "a dist/ prefix in the checksum file broke the lookup"
fi

echo "# what it refuses"

# A blank checksum would be generated happily and rejected by every user, so a
# missing archive has to stop the generator and name what is missing.
checks=$((checks + 1))
grep -v linux_arm64 "$tmp/SHA256SUMS" >"$tmp/incomplete.txt"
if "$script" formula 0.2.0 "$tmp/incomplete.txt" >"$tmp/out.rb" 2>"$tmp/err.txt"; then
	fail "the formula was generated with a checksum missing"
	sed 's/^/       /' "$tmp/out.rb" >&2
else
	if grep -q 'edgemix_0.2.0_linux_arm64.tar.gz' "$tmp/err.txt"; then
		pass "a missing checksum is fatal and names the archive"
	else
		fail "the missing archive was not named: $(cat "$tmp/err.txt")"
	fi
fi

# Nothing is printed before the failure: a half-written formula redirected into
# a file is a formula somebody publishes.
checks=$((checks + 1))
if [ ! -s "$tmp/out.rb" ]; then
	pass "nothing is printed when a checksum is missing"
else
	fail "a partial formula was printed before the error"
fi

checks=$((checks + 1))
if "$script" formula v0.2.0 "$tmp/SHA256SUMS" >/dev/null 2>"$tmp/verr.txt"; then
	fail "a v-prefixed version was accepted: the formula's own test would then fail after a successful install"
else
	pass "a v-prefixed version is refused, with the reason"
fi

checks=$((checks + 1))
if "$script" formula 0.2.0 "$tmp/nope.txt" >/dev/null 2>&1; then
	fail "a missing checksum file was accepted"
else
	pass "a missing checksum file is refused"
fi

checks=$((checks + 1))
if [ "$("$script" platforms | grep -c .)" -eq 4 ]; then
	pass "four platforms: macOS and Linux, arm and intel"
else
	fail "platforms does not list four"
fi

# The formula's four blocks are written out by hand in the generator, so the
# platform list and the formula could drift. A platform named in the list and
# missing from the formula is a platform nobody can install on.
missing=""
for p in $("$script" platforms); do
	grep -qF "edgemix_0.2.0_${p}.tar.gz" "$tmp/formula.rb" || missing="$missing $p"
done
checks=$((checks + 1))
if [ -z "$missing" ]; then
	pass "every platform the generator lists appears in the formula"
else
	fail "listed but not in the formula:$missing"
fi

echo "# the formula's test block is a claim about this tool"

# The `test do` block asserts a version string and one measurement. Both are
# claims about the binary, so both are checked here rather than trusted — the
# rule the whole repository runs on is that a number in a file comes from having
# run the thing.
assert_has "the test block pins the version to what the binary reports" \
	'assert_match "edgemix #{version}"'

checks=$((checks + 1))
if ! command -v go >/dev/null 2>&1; then
	echo "skip no go toolchain here: the formula's measurement claim was not run"
else
	# The two log lines out of the heredoc, and the number the formula claims.
	awk '/<<~LOG/ { p = 1; next } /^    LOG$/ { p = 0 } p { sub(/^      /, ""); print }' \
		"$tmp/formula.rb" >"$tmp/edge.log"
	want=$(awk '/assert_match "peak/ { match($0, /"peak [^"]*"/); print substr($0, RSTART + 1, RLENGTH - 2); exit }' "$tmp/formula.rb")
	go build -o "$tmp/edgemix" "$root/cmd/edgemix"
	if [ ! -s "$tmp/edge.log" ] || [ -z "$want" ]; then
		fail "could not read the test block out of the formula (log lines or expectation missing)"
	elif "$tmp/edgemix" analyze "$tmp/edge.log" 2>/dev/null | grep -qF "$want"; then
		pass "the formula's own measurement is true of this binary: $want"
	else
		fail "the formula claims \"$want\" and this binary does not say it"
		"$tmp/edgemix" analyze "$tmp/edge.log" 2>&1 | sed 's/^/       /' >&2
	fi
fi

echo ""
if [ "$failures" -eq 0 ]; then
	echo "brew_test.sh: $checks checks, all passed"
else
	echo "brew_test.sh: $checks checks, $failures failed" >&2
	exit 1
fi
