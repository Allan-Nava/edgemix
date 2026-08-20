#!/bin/sh
# logo.sh — write the edgemix logo files from one geometry.
#
#   scripts/logo.sh write [dir]   regenerate the SVGs in docs/assets (default)
#   scripts/logo.sh check         fail if the committed files are stale
#   scripts/logo.sh geometry      print the numbers the mark is made of
#
# The logo is the argument the tool makes, drawn: seven bars are seven seconds
# of traffic, the green one is the peak second, and the dashed line is the mean
# — which sits far below the second that produced the timeouts. Sizing against
# that line is the mistake edgemix exists to stop, so the mark states it.
#
# The four files are generated rather than hand-kept, for the same reason
# ROADMAP.md is: they share a geometry, and a mark whose lockup drifted from it
# by two pixels is a mark nobody notices is wrong. `check` is a CI gate.
#
# The dashed line is not a drawn decision either: it is the arithmetic mean of
# the bar values, computed here. If a bar changes, the mean moves with it, and
# the mark cannot end up claiming an average it does not have.
#
# POSIX sh and awk only, like the rest of scripts/. No raster is committed: an
# SVG is text, it reviews in a diff, and generating a PNG would mean a
# dependency this repository does not have. docs/logo.md says how to make one
# if a platform demands it.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
outdir="${LOGO_DIR:-$root/docs/assets}"

# The mark, in request-per-second units: one number per logged second.
bars="15 21 17 42 23 13 19"
peak=4 # 1-based index of the busiest second

# The favicon keeps four of those seconds around the peak. A favicon is
# rendered at 16px, where seven bars and a 3px dash are mush: it is the same
# mark restated at that size, not the same mark shrunk.
fav_from=3
fav_to=6

slate="#7c8b9a"  # ordinary seconds — legible on white and on #0d1117 alike,
                 # which is why there is no light and no dark variant of a mark
emerald="#10b981" # the peak second
amber="#f59e0b"   # the mean

tmp=$(mktemp -d "${TMPDIR:-/tmp}/edgemix-logo.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

# geometry prints what the mark is made of, so the numbers on the docs page can
# be read out of the generator rather than counted off the drawing.
geometry() {
	echo "$bars" | awk -v peak="$peak" -v from="$fav_from" -v to="$fav_to" '{
		n = NF
		for (i = 1; i <= n; i++) { sum += $i; if ($i > max) max = $i }
		printf "bars      %d seconds: %s\n", n, $0
		printf "peak      bar %d, %d req/s (%.2f x the mean)\n", peak, $peak, $peak / (sum / n)
		printf "mean      %.2f req/s\n", sum / n
		printf "favicon   bars %d..%d: ", from, to
		for (i = from; i <= to; i++) printf "%s%s", $i, (i < to ? " " : "\n")
	}'
}

# bars_svg emits the <rect>s and the mean line for one drawing.
#   $1 width  $2 gap  $3 x0  $4 baseline  $5 max-height  $6 rx
#   $7 dash overhang (0 leaves the mean line out)  $8 peak index  $9..: values
#
# The peak index is a parameter and not the global, because `peak=2 bars_svg …`
# would have been the tidy way to write the favicon call and is unspecified for
# a shell function: dash keeps the assignment after the call returns, so the
# lockup drawn next would have highlighted the wrong second — on CI only, where
# sh is dash, and never on the machine that generated the committed files.
bars_svg() {
	w=$1 gap=$2 x0=$3 base=$4 maxh=$5 rx=$6 over=$7 at=$8
	shift 8
	echo "$@" | awk -v w="$w" -v gap="$gap" -v x0="$x0" -v base="$base" \
		-v maxh="$maxh" -v rx="$rx" -v over="$over" -v peak="$at" \
		-v slate="$slate" -v emerald="$emerald" -v amber="$amber" '
	function num(v) { s = sprintf("%.2f", v); sub(/\.?0+$/, "", s); return s }
	{
		n = NF
		for (i = 1; i <= n; i++) { sum += $i; if ($i > max) max = $i }
		scale = maxh / max
		# The mean goes down first, so the bars cross in front of it and it
		# reads as the gridline it is. Drawn on top it collides with the bar
		# whose height is nearest the mean, and the collision looks like a
		# smudge at every size below 64px.
		if (over > 0) {
			y = base - (sum / n) * scale
			right = x0 + n * (w + gap) - gap
			printf "  <!-- the mean: %.2f req/s, which is the number not to size against -->\n", sum / n
			printf "  <path d=\"M%s %s H%s\" stroke=\"%s\" stroke-width=\"1.8\" stroke-linecap=\"round\" stroke-dasharray=\"3 3.5\" fill=\"none\"/>\n", \
				num(x0 - over), num(y), num(right + over), amber
		}
		# Ordinary seconds as one filled group: fewer attributes, and a reader
		# of the file sees at a glance which bar is the exception.
		printf "  <g fill=\"%s\">\n", slate
		for (i = 1; i <= n; i++) {
			if (i == peak) continue
			h = $i * scale
			printf "    <rect x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" rx=\"%s\"/>\n", \
				num(x0 + (i - 1) * (w + gap)), num(base - h), num(w), num(h), num(rx)
		}
		print "  </g>"
		h = $peak * scale
		printf "  <!-- the peak second: %d req/s -->\n", $peak
		printf "  <rect x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" rx=\"%s\" fill=\"%s\"/>\n", \
			num(x0 + (peak - 1) * (w + gap)), num(base - h), num(w), num(h), num(rx), emerald
	}'
}

# peak_bar_of prints the value of the peak bar, for the descriptions.
peak_value=$(echo "$bars" | awk -v p="$peak" '{ print $p }')
mean_value=$(echo "$bars" | awk '{ for (i = 1; i <= NF; i++) s += $i; printf "%.0f", s / NF }')
seconds=$(echo "$bars" | awk '{ print NF }')
fav_bars=$(echo "$bars" | awk -v a="$fav_from" -v b="$fav_to" '{ for (i = a; i <= b; i++) printf "%s%s", $i, (i < b ? " " : "") }')
fav_peak=$((peak - fav_from + 1))

write_mark() {
	cat <<HEAD
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width="64" height="64"
     role="img" aria-labelledby="title desc">
  <title id="title">edgemix</title>
  <desc id="desc">$seconds bars, one logged second each. The green one is the peak
    second at $peak_value requests, and the dashed line is the mean at
    $mean_value — the number a system gets sized against, and the reason the
    peak is the one drawn tall.</desc>
  <!-- GENERATED by scripts/logo.sh — edit the geometry there, not this file. -->
HEAD
	bars_svg 6 2 5 55 42 2 2 "$peak" $bars
	echo "</svg>"
}

write_favicon() {
	cat <<HEAD
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16"
     role="img" aria-label="edgemix">
  <!-- GENERATED by scripts/logo.sh. The mark restated at 16px: $((fav_to - fav_from + 1)) of the
       $seconds seconds and no dashed line, because a 3px dash in a browser tab is
       mush. A favicon that is the logo shrunk is a favicon nobody recognises. -->
HEAD
	bars_svg 2.6 1 1.6 14.4 12 0.8 0 "$fav_peak" $fav_bars
	echo "</svg>"
}

# The wordmark is set in the system monospace: the face the report itself is
# read in, and no font file to fetch — a logo that pulls a webfont would be the
# only network call in the project. Because that face differs per machine,
# textLength pins the advance width, or a wider mono runs past the viewBox and
# is clipped.
write_lockup() {
	ink=$1
	cat <<HEAD
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 232 64" width="232" height="64"
     role="img" aria-labelledby="title">
  <title id="title">edgemix</title>
  <!-- GENERATED by scripts/logo.sh — edit the geometry there, not this file. -->
HEAD
	bars_svg 6 2 5 55 42 2 2 "$peak" $bars
	cat <<FOOT
  <text x="76" y="46" textLength="142" lengthAdjust="spacingAndGlyphs"
        font-family="ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace"
        font-size="33" font-weight="700" letter-spacing="-1" fill="$ink">edgemix</text>
</svg>
FOOT
}

write_all() {
	dir=$1
	mkdir -p "$dir"
	write_mark >"$dir/edgemix-mark.svg"
	write_favicon >"$dir/favicon.svg"
	write_lockup "#1f2328" >"$dir/edgemix-logo-light.svg"
	write_lockup "#e6edf3" >"$dir/edgemix-logo-dark.svg"
}

case "${1:-write}" in
write)
	write_all "${2:-$outdir}"
	echo "logo.sh: wrote 4 SVGs to ${2:-$outdir}"
	geometry | sed 's/^/  /'
	;;
check)
	write_all "$tmp/assets"
	bad=0
	for f in edgemix-mark.svg favicon.svg edgemix-logo-light.svg edgemix-logo-dark.svg; do
		if [ ! -f "$outdir/$f" ]; then
			echo "logo.sh: $f is missing from $outdir" >&2
			bad=1
		elif ! diff -u "$outdir/$f" "$tmp/assets/$f"; then
			echo "logo.sh: $f is stale" >&2
			bad=1
		fi
	done
	if [ "$bad" -ne 0 ]; then
		echo "" >&2
		echo "logo.sh: run scripts/logo.sh write and commit the result" >&2
		exit 1
	fi
	echo "logo.sh: the committed SVGs match the geometry"
	;;
geometry)
	geometry
	;;
*)
	echo "usage: scripts/logo.sh [write [dir]|check|geometry]" >&2
	exit 2
	;;
esac
