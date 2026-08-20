#!/bin/sh
# site_test.sh — tests for scripts/site.sh, the markdown renderer behind the
# documentation site.
#
# A renderer is the worst kind of thing to leave untested, because it does not
# crash: an unsupported construct comes out as literal `**bold**` in a published
# page, a `.md` link that was not rewritten 404s only when somebody clicks it,
# and a `|` inside a table cell splits one row into three that still look like
# a table. Every case below is one of those — a wrong page rather than a missing
# one.
#
# POSIX sh and awk only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/site.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/edgemix-site-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}

# render <markdown> writes the rendered HTML to $tmp/out.html
render() {
	printf '%s\n' "$1" >"$tmp/in.md"
	"$script" render "$tmp/in.md" >"$tmp/out.html"
}

# assert_render <name> <markdown> <needle>
assert_render() {
	checks=$((checks + 1))
	render "$2"
	if grep -qF "$3" "$tmp/out.html"; then
		echo "ok   $1"
	else
		fail "$1 — expected to find: $3"
		sed 's/^/       /' "$tmp/out.html" >&2
	fi
}

# assert_render_absent <name> <markdown> <needle>
assert_render_absent() {
	checks=$((checks + 1))
	render "$2"
	if grep -qF "$3" "$tmp/out.html"; then
		fail "$1 — did not expect to find: $3"
		sed 's/^/       /' "$tmp/out.html" >&2
	else
		echo "ok   $1"
	fi
}

nl='
'

echo "# blocks"

assert_render "a heading carries a GitHub-style anchor id" \
	'## The four refusals' \
	'<h2 id="the-four-refusals">'

assert_render "a heading in code still slugs to the bare word" \
	'## `analyze`' \
	'id="analyze"'

assert_render "the level-1 heading gets no anchor link" \
	'# Usage' \
	'<h1 id="usage">Usage</h1>'

assert_render "a paragraph is one <p>" \
	"first line${nl}second line" \
	'<p>first line second line</p>'

# A paragraph is joined before its inline markup is read: markdown lets a link
# or a bold run wrap across a line, and the docs do exactly that.
assert_render "markup wrapped across two lines still renders" \
	"see the **whole${nl}thing** here" \
	'<strong>whole thing</strong>'

assert_render "a fence becomes pre/code" \
	'```bash
edgemix analyze edge.log
```' \
	'<pre data-lang="bash"><code>edgemix analyze edge.log'

# An access log is full of characters that are markup in HTML. A fence that
# does not escape them publishes a page that silently loses half a log line.
assert_render "a fence escapes HTML and leaves markdown alone" \
	'```
a <b> & "c" **not bold** `not code`
```' \
	'a &lt;b&gt; &amp; &quot;c&quot; **not bold** `not code`'

assert_render "a bullet list nests" \
	"- outer${nl}  - inner${nl}- outer again" \
	'<li>outer<ul>'

assert_render "a wrapped list item is one item" \
	"- a claim that${nl}  continues here${nl}- another" \
	'<li>a claim that continues here'

assert_render "a blockquote is a blockquote" \
	'> generated, do not edit' \
	'<blockquote><p>generated, do not edit</p></blockquote>'

echo "# tables"

assert_render "a table gets a scroll container" \
	"| A | B |${nl}|---|---|${nl}| 1 | 2 |" \
	'<div class="scroll">'

assert_render "a table row becomes cells" \
	"| A | B |${nl}|---|---|${nl}| 1 | 2 |" \
	'<td>1</td>'

# The trap the roadmap generator hit first: a pipe is legal inside a cell when
# it is escaped, and reading it as a separator turns one row into three columns
# of a table that still looks fine.
assert_render "an escaped pipe stays inside its cell" \
	"| Flag | Values |${nl}|---|---|${nl}| \`--format\` | text \\| md \\| json |" \
	'<td>text | md | json</td>'

assert_render_absent "a table with an escaped pipe grows no extra cell" \
	"| Flag | Values |${nl}|---|---|${nl}| \`--format\` | text \\| md \\| json |" \
	'<td>md</td>'

# A line starting with a pipe is only a table when a separator follows it.
assert_render_absent "a lone pipe line is not a table" \
	'| this is prose that happens to start with a pipe' \
	'<table>'

echo "# inline"

assert_render "inline code is escaped and wrapped" \
	'the `<BADREQ>` request' \
	'<code>&lt;BADREQ&gt;</code>'

# Code spans are lifted out before any other inline rule runs, so what is
# inside one is text. `**` inside a code span was the first thing to break this.
assert_render "markup inside a code span is text" \
	'a `**literal**` span' \
	'<code>**literal**</code>'

assert_render "strong renders" \
	'**worst first**' \
	'<strong>worst first</strong>'

assert_render "emphasis renders" \
	'the *shape* of a line' \
	'<em>shape</em>'

# An asterisk on its own is a character in prose, not the start of emphasis.
# The docs are full of them: globs, footnote markers, arithmetic.
assert_render "a stray asterisk is left alone" \
	'peak is 4.3* the p95 second' \
	'4.3* the p95'

# Not supported on purpose: an underscore is a character in
# `$upstream_cache_status`, `log_format` and `be_app`, and treating it as
# emphasis mangles prose about a log format.
assert_render "an underscore is not emphasis" \
	'the _rsc parameter and the _measured block' \
	'the _rsc parameter and the _measured block'

echo "# links"

assert_render "a .md link is rewritten to .html" \
	'see [the dialects](dialects.md)' \
	'<a href="dialects.html">the dialects</a>'

assert_render "a fragment survives the rewrite" \
	'see [the class set](usage.md#classes)' \
	'<a href="usage.html#classes">'

assert_render "a bare fragment is untouched" \
	'see [the class set](#classes)' \
	'<a href="#classes">'

assert_render "an absolute link is untouched" \
	'see [crowdsim](https://github.com/HiWay-Media/crowdsim)' \
	'<a href="https://github.com/HiWay-Media/crowdsim">'

# A link to a file that is not a page — the example log — must not become
# .html, and a link inside a code span is not a link at all.
assert_render "a link to a non-markdown file keeps its extension" \
	'see [the log](example.log)' \
	'<a href="example.log">'

assert_render "a link inside a code span is text" \
	'write `[text](target.md)` to link' \
	'<code>[text](target.md)</code>'

echo "# the site"

# Every page in docs/ is reachable from the nav, and every nav entry is a page
# that exists. Both directions matter: an unreachable page is one nobody reads,
# and a nav entry pointing at nothing is a 404 on every page of the site.
checks=$((checks + 1))
if "$script" pages | while IFS='|' read -r slug label; do
	[ -n "$slug" ] || continue
	[ -f "$root/docs/$slug.md" ] || exit 1
	[ -n "$label" ] || exit 1
done; then
	echo "ok   every nav entry is a page in docs/ with a label"
else
	fail "the nav names a page that does not exist in docs/"
fi

checks=$((checks + 1))
missing=""
for f in "$root"/docs/*.md; do
	slug=$(basename "$f" .md)
	"$script" pages | grep -q "^$slug|" || missing="$missing $slug"
done
if [ -z "$missing" ]; then
	echo "ok   every page in docs/ is in the nav"
else
	fail "docs/ has pages the nav does not carry:$missing"
fi

# An unlisted page and a nav entry with no page both have to fail the build.
# The first is a page nobody can reach, the second a 404 in the nav of every
# page on the site — and neither shows up as an error anywhere else.
checks=$((checks + 1))
mkdir -p "$tmp/nav"
cp "$root"/docs/*.md "$tmp/nav/"
printf '# Orphan\n' >"$tmp/nav/orphan.md"
if DOCS_DIR="$tmp/nav" "$script" check >"$tmp/nav.out" 2>&1; then
	fail "check passed over a docs/ directory with a page the nav does not carry"
else
	if grep -q 'orphan.md is not in the nav' "$tmp/nav.out"; then
		echo "ok   a page missing from the nav fails the build, named"
	else
		fail "the unlisted page was not named"
		sed 's/^/       /' "$tmp/nav.out" >&2
	fi
fi

checks=$((checks + 1))
rm -f "$tmp/nav/orphan.md" "$tmp/nav/usage.md"
if DOCS_DIR="$tmp/nav" "$script" check >"$tmp/nav2.out" 2>&1; then
	fail "check passed over a nav entry whose page does not exist"
else
	if grep -q 'nav carries usage' "$tmp/nav2.out"; then
		echo "ok   a nav entry with no page fails the build, named"
	else
		fail "the missing page was not named"
		sed 's/^/       /' "$tmp/nav2.out" >&2
	fi
fi

# The real site builds, and every internal link and anchor in it resolves.
checks=$((checks + 1))
if "$script" check >"$tmp/check.out" 2>&1; then
	echo "ok   the site builds and its internal links resolve"
else
	fail "site.sh check failed"
	sed 's/^/       /' "$tmp/check.out" >&2
fi

# ...and it says so rather than shipping a dead link. A fixture with a link to
# a page that does not exist has to fail the check, or the gate is decoration.
checks=$((checks + 1))
mkdir -p "$tmp/docs"
cp "$root/docs/index.md" "$tmp/docs/index.md"
printf '\nsee [nowhere](nowhere.md) and [no anchor](index.md#not-a-heading)\n' >>"$tmp/docs/index.md"
for slug in usage dialects findings example profile; do
	printf '# %s\n' "$slug" >"$tmp/docs/$slug.md"
done
if DOCS_DIR="$tmp/docs" "$script" check >"$tmp/dead.out" 2>&1; then
	fail "check passed over a site with a dead link and a dead anchor"
	sed 's/^/       /' "$tmp/dead.out" >&2
else
	if grep -q 'dead link' "$tmp/dead.out" && grep -q 'dead anchor' "$tmp/dead.out"; then
		echo "ok   a dead internal link and a dead anchor both fail the check, named"
	else
		fail "check failed but did not name both the dead link and the dead anchor"
		sed 's/^/       /' "$tmp/dead.out" >&2
	fi
fi

echo ""
if [ "$failures" -eq 0 ]; then
	echo "site_test.sh: $checks checks, all passed"
else
	echo "site_test.sh: $checks checks, $failures failed" >&2
	exit 1
fi
