#!/bin/sh
# site.sh — render docs/ into the static site published on GitHub Pages.
#
#   scripts/site.sh build [dir]   render docs/ into dir (default _site)
#   scripts/site.sh check         build into a temp dir, then verify every
#                                 internal link and anchor resolves
#   scripts/site.sh render FILE   render one markdown file to stdout (tests)
#   scripts/site.sh pages         list the pages the nav carries
#
# POSIX sh and awk only. This repository has no dependencies and neither does
# its tooling: a documentation site that needs Ruby, Node or a theme gem to
# build is a site that stops building, and the pages here are markdown with
# headings, tables, fences and lists in them — a subset small enough to render
# honestly and to test.
#
# The supported subset is deliberate and stated, because silently ignoring a
# construct is how a page ships with `**bold**` printed literally in it:
#
#   headings (`#`..`######`, with GitHub-style anchor ids), fenced code blocks
#   (escaped, never highlighted), tables (with `\|` inside a cell), bullet
#   lists with one level of nesting and wrapped continuation lines,
#   blockquotes, horizontal rules, paragraphs, and inline `code`, **strong**,
#   *emphasis* and [links](target.md) — the last rewritten from `.md` to
#   `.html` so the same file reads correctly on GitHub and on the site.
#
# Not supported, on purpose: `_emphasis_` (a `$upstream_cache_status` in prose
# would become italics), images, footnotes, HTML blocks, nested ordered lists,
# and cell alignment. `check` fails on a broken internal link, which is the
# failure this script is most likely to cause.
#
# DOCS_DIR overrides the source directory so the tests can render a fixture
# without touching this repository's own docs.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
docsdir="${DOCS_DIR:-$root/docs}"

# The nav, in reading order: `<slug>|<label>`. A page in docs/ that is missing
# from this list is an error rather than a page nobody can reach.
pages='index|Overview
usage|Usage
dialects|Dialects
findings|Findings
example|Worked example
profile|Log to load test'

repo_url="https://github.com/Allan-Nava/edgemix"
site_name="edgemix"
site_tagline="Read the edge log. Say what the traffic was. Write the load test."

tmp=$(mktemp -d "${TMPDIR:-/tmp}/edgemix-site.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

# ---------------------------------------------------------------------------
# The markdown renderer.
#
# Two passes: every line is read into an array first, because a table is only a
# table when the line after it is a separator, and a lookahead is easier to
# reason about than a state machine that has to remember it might be in one.
# ---------------------------------------------------------------------------
render_md() {
	awk '
	function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }

	# HTML-escape by concatenation rather than gsub: the meaning of a backslash
	# in a gsub replacement is not portable, and & is exactly the character
	# that would need one. A multibyte character passes through byte by byte
	# and comes out intact either way.
	function esc(s,   out, i, c) {
		out = ""
		for (i = 1; i <= length(s); i++) {
			c = substr(s, i, 1)
			if (c == "&") out = out "&amp;"
			else if (c == "<") out = out "&lt;"
			else if (c == ">") out = out "&gt;"
			else if (c == "\"") out = out "&quot;"
			else out = out c
		}
		return out
	}

	# GitHub-style anchor: lowercase, runs of separators to one dash, anything
	# else dropped. Computed from the raw heading text so `## `analyze`` and
	# `## analyze` land on the same id, which is what a #anchor in another page
	# was written against.
	function slug(s,   out, i, c) {
		s = tolower(s)
		out = ""
		for (i = 1; i <= length(s); i++) {
			c = substr(s, i, 1)
			if (c ~ /[a-z0-9]/) out = out c
			else if (c == " " || c == "-" || c == "_")
				{ if (out != "" && substr(out, length(out), 1) != "-") out = out "-" }
		}
		sub(/-+$/, "", out)
		return out
	}

	# A link target: .md becomes .html, anything with a scheme or starting at a
	# fragment is left alone. The fragment is split off first so
	# `usage.md#classes` is rewritten and `#classes` is not touched.
	function href(u,   base, frag, h) {
		h = index(u, "#")
		if (h) { base = substr(u, 1, h - 1); frag = substr(u, h) } else { base = u; frag = "" }
		if (base ~ /^[a-zA-Z][a-zA-Z0-9+.-]*:/ || base ~ /^\/\//) return u
		if (base ~ /\.md$/) sub(/\.md$/, ".html", base)
		return base frag
	}

	# Inline markup. Code spans are lifted out first and put back last, so a
	# `**` or a `[` inside one is text and not markup.
	function inline(s,   code, n, p, q, rest, k, out) {
		s = esc(s)

		n = 0
		while ((p = index(s, "`")) > 0) {
			rest = substr(s, p + 1)
			q = index(rest, "`")
			if (!q) break
			n++
			code[n] = substr(rest, 1, q - 1)
			s = substr(s, 1, p - 1) "\001" n "\002" substr(rest, q + 1)
		}

		# Links. The prefix already scanned is kept aside, so a `](` that does
		# not resolve costs one link rather than every link after it.
		out = ""
		while ((p = index(s, "](")) > 0) {
			k = 0
			for (q = p; q >= 1; q--) if (substr(s, q, 1) == "[") { k = q; break }
			rest = substr(s, p + 2)
			q = index(rest, ")")
			if (!k || !q) { out = out substr(s, 1, p + 1); s = substr(s, p + 2); continue }
			out = out substr(s, 1, k - 1) \
				"<a href=\"" href(substr(rest, 1, q - 1)) "\">" substr(s, k + 1, p - k - 1) "</a>"
			s = substr(rest, q + 1)
		}
		s = out s

		while ((p = index(s, "**")) > 0) {
			rest = substr(s, p + 2)
			q = index(rest, "**")
			if (!q) break
			s = substr(s, 1, p - 1) "<strong>" substr(rest, 1, q - 1) "</strong>" substr(rest, q + 2)
		}

		# Single-star emphasis, and only where it cannot be a stray asterisk:
		# no space after the opening star, none before the closing one.
		out = ""
		while ((p = index(s, "*")) > 0) {
			rest = substr(s, p + 1)
			q = index(rest, "*")
			if (!q || substr(rest, 1, 1) == " " || substr(rest, q - 1, 1) == " " || q == 1) {
				out = out substr(s, 1, p); s = rest; continue
			}
			out = out substr(s, 1, p - 1) "<em>" substr(rest, 1, q - 1) "</em>"
			s = substr(rest, q + 1)
		}
		s = out s

		for (k = 1; k <= n; k++) {
			p = index(s, "\001" k "\002")
			if (p) s = substr(s, 1, p - 1) "<code>" code[k] "</code>" substr(s, p + length("\001" k "\002"))
		}
		return s
	}

	function is_table_sep(s) { return s ~ /^\|[ :|-]+\|[ \t]*$/ }
	function starts_block(s) {
		return s ~ /^```/ || s ~ /^#{1,6} / || s ~ /^\|/ || s ~ /^[-*] / || \
		       s ~ /^> / || s ~ /^(---+|\*\*\*+)[ \t]*$/ || s ~ /^[ \t]*$/
	}
	function indent_of(s,   i, c, n) {
		n = 0
		for (i = 1; i <= length(s); i++) {
			c = substr(s, i, 1)
			if (c == " ") n++
			else if (c == "\t") n += 8
			else break
		}
		return n
	}

	# Split a table row on its unescaped pipes. A pipe is legal inside a cell
	# when it is written \| — `--profile a|b` in a flag table would otherwise
	# turn one row into three columns.
	function cells(row, out,   s, i, c, cur, n) {
		s = trim(row)
		sub(/^\|/, "", s)
		sub(/\|$/, "", s)
		n = 0; cur = ""
		for (i = 1; i <= length(s); i++) {
			c = substr(s, i, 1)
			if (c == "\\" && substr(s, i + 1, 1) == "|") { cur = cur "|"; i++; continue }
			if (c == "|") { out[++n] = trim(cur); cur = ""; continue }
			cur = cur c
		}
		out[++n] = trim(cur)
		return n
	}

	function do_fence(i,   lang, body, l) {
		lang = trim(substr(lines[i], 4))
		body = ""
		i++
		while (i <= N && lines[i] !~ /^```/) { body = body esc(lines[i]) "\n"; i++ }
		printf "<pre%s><code>%s</code></pre>\n", \
			(lang == "" ? "" : " data-lang=\"" esc(lang) "\""), body
		return i + 1   # step past the closing fence
	}

	function do_heading(i,   l, level, text, id) {
		l = lines[i]
		level = 0
		while (substr(l, level + 1, 1) == "#") level++
		text = trim(substr(l, level + 1))
		id = slug(text)
		if (level == 1)
			printf "<h1 id=\"%s\">%s</h1>\n", id, inline(text)
		else
			printf "<h%d id=\"%s\">%s<a class=\"anchor\" href=\"#%s\" aria-label=\"link to this section\">#</a></h%d>\n", \
				level, id, inline(text), id, level
		return i + 1
	}

	function do_table(i,   nh, head, nb, body, j, k) {
		nh = cells(lines[i], head)
		print "<div class=\"scroll\">"
		print "<table>"
		print "<thead><tr>"
		for (k = 1; k <= nh; k++) printf "<th>%s</th>\n", inline(head[k])
		print "</tr></thead>"
		print "<tbody>"
		i += 2
		while (i <= N && lines[i] ~ /^\|/) {
			nb = cells(lines[i], body)
			print "<tr>"
			for (k = 1; k <= nb; k++) printf "<td>%s</td>\n", inline(body[k])
			# A short row is padded rather than dropped: a missing cell is a
			# typo in the source, not a reason to lose the row.
			for (k = nb + 1; k <= nh; k++) print "<td></td>"
			print "</tr>"
			i++
		}
		print "</tbody></table>"
		print "</div>"
		return i
	}

	function do_list(i,   m, items, inds, l, depth, stack, k) {
		m = 0
		while (i <= N) {
			l = lines[i]
			if (l ~ /^[ \t]*$/) break
			if (l ~ /^[ \t]*[-*] /) {
				m++
				inds[m] = indent_of(l)
				items[m] = trim(substr(trim(l), 3))
			} else if (m > 0 && l ~ /^[ \t]+[^ \t]/) {
				items[m] = items[m] " " trim(l)     # a wrapped item
			} else break
			i++
		}
		depth = 0
		for (k = 1; k <= m; k++) {
			if (depth == 0) { print "<ul>"; stack[++depth] = inds[k] }
			else if (inds[k] > stack[depth]) { print "<ul>"; stack[++depth] = inds[k] }
			else {
				while (depth > 1 && inds[k] < stack[depth]) { print "</li></ul>"; depth-- }
				print "</li>"
			}
			printf "<li>%s", inline(items[k])
		}
		while (depth > 1) { print "</li></ul>"; depth-- }
		if (m) print "</li></ul>"
		return i
	}

	function do_quote(i,   text) {
		text = ""
		while (i <= N && lines[i] ~ /^> ?/) {
			text = text (text == "" ? "" : " ") trim(substr(lines[i], 2))
			i++
		}
		printf "<blockquote><p>%s</p></blockquote>\n", inline(text)
		return i
	}

	# A paragraph is joined before its inline markup is read, so a **bold** or a
	# [link](x) wrapped across two source lines still renders as one.
	#
	# The first line is taken unconditionally, and that is not a detail: a line
	# starting with `|` that no separator follows is prose, it reaches here, and
	# a loop that consumed only lines starts_block() rejects would return the
	# same index it was given and hang. A renderer that hangs on a paragraph
	# beginning with a pipe is one the test suite found before a docs page did.
	function do_para(i,   text) {
		text = trim(lines[i])
		i++
		while (i <= N && !starts_block(lines[i])) {
			text = text (text == "" ? "" : " ") trim(lines[i])
			i++
		}
		if (text != "") printf "<p>%s</p>\n", inline(text)
		return i
	}

	{ lines[++N] = $0 }

	END {
		i = 1
		while (i <= N) {
			l = lines[i]
			if (l ~ /^```/)                                 i = do_fence(i)
			else if (l ~ /^#{1,6} /)                        i = do_heading(i)
			else if (l ~ /^\|/ && is_table_sep(lines[i + 1])) i = do_table(i)
			else if (l ~ /^[ \t]*$/)                        i++
			else if (l ~ /^(---+|\*\*\*+)[ \t]*$/)          { print "<hr>"; i++ }
			else if (l ~ /^[-*] /)                          i = do_list(i)
			else if (l ~ /^> /)                             i = do_quote(i)
			else                                            i = do_para(i)
		}
	}
	' "$1"
}

# h1_of prints the first level-1 heading of a markdown file, ignoring anything
# inside a fence — a `# comment` line in a bash example is not a title.
h1_of() {
	awk '
	/^```/ { fence = !fence; next }
	fence  { next }
	/^# /  { sub(/^# /, ""); gsub(/`/, ""); print; exit }
	' "$1"
}

nav_html() {
	cur=$1
	printf '%s\n' "$pages" | while IFS='|' read -r slug label; do
		[ -n "$slug" ] || continue
		if [ "$slug" = "$cur" ]; then
			printf '<a href="%s.html" aria-current="page">%s</a>' "$slug" "$label"
		else
			printf '<a href="%s.html">%s</a>' "$slug" "$label"
		fi
	done
}

stylesheet() {
	cat <<'CSS'
/* Generated site for edgemix. One stylesheet, no fonts fetched, no scripts:
   the tool makes no network calls and neither does its documentation. */
:root {
  --bg: #ffffff;
  --bg-soft: #f6f8fa;
  --bg-code: #f6f8fa;
  --fg: #1f2328;
  --fg-soft: #59636e;
  --line: #d1d9e0;
  --accent: #0d8a5f;
  --accent-soft: #e7f6ef;
  --mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0d1117;
    --bg-soft: #151b23;
    --bg-code: #151b23;
    --fg: #e6edf3;
    --fg-soft: #9198a1;
    --line: #2f3742;
    --accent: #3fb950;
    --accent-soft: #16251c;
  }
}
* { box-sizing: border-box; }
html { -webkit-text-size-adjust: 100%; }
body {
  margin: 0;
  background: var(--bg);
  color: var(--fg);
  font: 16px/1.65 -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
}
.wrap { max-width: 52rem; margin: 0 auto; padding: 0 1.25rem; }
header.site {
  border-bottom: 1px solid var(--line);
  background: var(--bg-soft);
}
header.site .wrap { padding-top: 1.5rem; padding-bottom: 0; }
.brand { display: flex; align-items: baseline; gap: .6rem; flex-wrap: wrap; }
.brand a { color: var(--fg); text-decoration: none; font-weight: 700; font-size: 1.35rem; font-family: var(--mono); }
.brand .tagline { color: var(--fg-soft); font-size: .9rem; }
nav.site { display: flex; gap: .25rem; flex-wrap: wrap; margin: 1rem -.6rem -1px; }
nav.site a {
  padding: .5rem .6rem;
  color: var(--fg-soft);
  text-decoration: none;
  font-size: .95rem;
  border-bottom: 2px solid transparent;
}
nav.site a:hover { color: var(--fg); }
nav.site a[aria-current="page"] { color: var(--fg); font-weight: 600; border-bottom-color: var(--accent); }
main { padding: 2rem 0 3rem; }
h1, h2, h3, h4 { line-height: 1.25; margin: 2rem 0 .75rem; }
h1 { font-size: 1.9rem; margin-top: .5rem; }
h2 { font-size: 1.35rem; padding-bottom: .3rem; border-bottom: 1px solid var(--line); }
h3 { font-size: 1.1rem; }
h2 + p, h3 + p { margin-top: .6rem; }
a { color: var(--accent); }
a.anchor {
  margin-left: .4rem; opacity: 0; text-decoration: none; font-weight: 400; color: var(--fg-soft);
}
h2:hover a.anchor, h3:hover a.anchor, h4:hover a.anchor { opacity: 1; }
p, ul, ol, blockquote { margin: .85rem 0; }
li { margin: .35rem 0; }
li > ul { margin: .35rem 0; }
code {
  font-family: var(--mono);
  font-size: .875em;
  background: var(--bg-code);
  border: 1px solid var(--line);
  border-radius: 4px;
  padding: .1em .3em;
}
pre {
  background: var(--bg-code);
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: .9rem 1rem;
  overflow-x: auto;          /* an access-log line is long; the page must not scroll */
  font-size: .82rem;
  line-height: 1.5;
}
pre code { background: none; border: 0; padding: 0; font-size: inherit; }
blockquote {
  margin-left: 0;
  padding: .1rem 1rem;
  border-left: 3px solid var(--line);
  color: var(--fg-soft);
}
.scroll { overflow-x: auto; margin: 1rem 0; }
table { border-collapse: collapse; width: 100%; font-size: .92rem; }
th, td { border: 1px solid var(--line); padding: .45rem .7rem; text-align: left; vertical-align: top; }
thead th { background: var(--bg-soft); }
tbody tr:nth-child(even) { background: var(--bg-soft); }
hr { border: 0; border-top: 1px solid var(--line); margin: 2rem 0; }
footer.site {
  border-top: 1px solid var(--line);
  color: var(--fg-soft);
  font-size: .85rem;
  padding: 1.5rem 0 3rem;
}
footer.site p { margin: .3rem 0; }
CSS
}

page_html() {
	src=$1
	slug=$2
	title=$(h1_of "$src")
	[ -n "$title" ] || title=$slug
	# The front page is titled `edgemix`, and "edgemix — edgemix" in a tab is
	# noise rather than context.
	if [ "$title" = "$site_name" ]; then head_title=$site_name; else head_title="$title — $site_name"; fi
	cat <<HEAD
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>$head_title</title>
<meta name="description" content="$site_tagline">
<meta property="og:title" content="$head_title">
<meta property="og:description" content="$site_tagline">
<meta property="og:type" content="article">
<link rel="stylesheet" href="style.css">
<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'%3E%3Ctext y='13' font-size='13'%3E%F0%9F%93%8A%3C/text%3E%3C/svg%3E">
</head>
<body>
<header class="site"><div class="wrap">
<div class="brand"><a href="index.html">$site_name</a> <span class="tagline">$site_tagline</span></div>
<nav class="site">$(nav_html "$slug")</nav>
</div></header>
<main><div class="wrap">
HEAD
	render_md "$src"
	cat <<FOOT
</div></main>
<footer class="site"><div class="wrap">
<p>Generated from <a href="$repo_url/tree/main/docs">docs/</a> by <code>scripts/site.sh</code> — edit the markdown, never the HTML.</p>
<p><a href="$repo_url">$repo_url</a> · PolyForm Noncommercial 1.0.0 · no dependencies, no network calls, not even here</p>
</div></footer>
</body>
</html>
FOOT
}

# lint_pages fails when docs/ and the nav disagree in either direction: a page
# nothing links to is as much a bug as a nav entry pointing at nothing.
lint_pages() {
	bad=0
	for f in "$docsdir"/*.md; do
		slug=$(basename "$f" .md)
		case "$slug" in _*) continue ;; esac
		if ! printf '%s\n' "$pages" | grep -q "^$slug|"; then
			echo "site.sh: docs/$slug.md is not in the nav — add it to \$pages in scripts/site.sh" >&2
			bad=1
		fi
	done
	printf '%s\n' "$pages" | while IFS='|' read -r slug label; do
		[ -n "$slug" ] || continue
		[ -f "$docsdir/$slug.md" ] || {
			echo "site.sh: the nav carries $slug but docs/$slug.md does not exist" >&2
			exit 1
		}
	done || bad=1
	[ "$bad" -eq 0 ] || return 1
}

build() {
	out=$1
	lint_pages
	mkdir -p "$out"
	stylesheet >"$out/style.css"
	# The site is deployed as an artifact, so Jekyll never runs over it. The
	# marker is here for the day somebody points the branch-based Pages build
	# at this directory, where a file named _something would be dropped.
	: >"$out/.nojekyll"
	count=0
	printf '%s\n' "$pages" | while IFS='|' read -r slug label; do
		[ -n "$slug" ] || continue
		page_html "$docsdir/$slug.md" "$slug" >"$out/$slug.html"
		echo "  $slug.html"
	done
	count=$(printf '%s\n' "$pages" | grep -c '|')
	echo "site.sh: wrote $count pages and style.css to $out"
}

# check_links resolves every href in the built site. A relative target must
# exist as a file, and a fragment must exist as an id in the page it points at
# — the two mistakes a markdown-to-HTML rename makes, and both of them are
# invisible until somebody clicks.
#
# Every problem is reported, not just the first: a check that stops at one dead
# link has to be re-run once per dead link, and the second one always exists.
check_links() {
	out=$1
	: >"$tmp/dead.txt"
	for f in "$out"/*.html; do
		page=$(basename "$f")
		grep -o 'href="[^"]*"' "$f" | sed 's/^href="//; s/"$//' | sort -u |
			while read -r url; do
				case "$url" in
				http://* | https://* | mailto:* | data:* | //*) continue ;;
				esac
				target=${url%%#*}
				frag=${url#*#}
				[ "$frag" = "$url" ] && frag=""
				file=${target:-$page}
				if [ ! -f "$out/$file" ]; then
					echo "$page: dead link -> $url (no $file)" >>"$tmp/dead.txt"
					continue
				fi
				if [ -n "$frag" ] && ! grep -q "id=\"$frag\"" "$out/$file"; then
					echo "$page: dead anchor -> $url (no id=\"$frag\" in $file)" >>"$tmp/dead.txt"
				fi
			done
	done
	if [ -s "$tmp/dead.txt" ]; then
		cat "$tmp/dead.txt" >&2
		echo "" >&2
		echo "site.sh: the site has $(grep -c '' "$tmp/dead.txt") broken internal link(s)" >&2
		return 1
	fi
	echo "site.sh: every internal link and anchor resolves"
}

case "${1:-build}" in
build)
	build "${2:-$root/_site}"
	;;
check)
	build "$tmp/site" >/dev/null
	check_links "$tmp/site"
	;;
render)
	[ $# -ge 2 ] || {
		echo "site.sh: render needs a markdown file" >&2
		exit 2
	}
	render_md "$2"
	;;
pages)
	printf '%s\n' "$pages"
	;;
*)
	echo "usage: scripts/site.sh [build [dir]|check|render FILE|pages]" >&2
	exit 2
	;;
esac
