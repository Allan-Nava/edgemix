# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **A documentation site, published from `docs/`** (EM-23). The dialect table,
  the finding catalogue with the threshold behind each one, the usage reference
  and a worked example, rendered by `scripts/site.sh` — POSIX sh and awk, like
  the rest of the tooling. A site that needs Ruby, Node or a theme gem to build
  is a site that stops building on the day that toolchain moves; this one has
  no toolchain, and the same markdown reads correctly on GitHub and on the
  site because the renderer rewrites `.md` links to `.html` rather than asking
  the pages to choose. `scripts/site.sh check` fails on a dead internal link or
  a dead anchor, which is the failure a docs rename actually causes and the one
  nobody notices until a reader clicks; it runs in CI on every push and before
  every deploy.
- **A worked example, end to end** (EM-23): `docs/example.md`, from a 40-line
  hand-written HAProxy log that ships with it (`docs/example.log`) to the
  emitted profile, with every figure on the page produced by running the two
  commands over that file. Two of its lines carry a syslog prefix and change
  none of the numbers, one request times out at 7301ms and one path is refused
  entry to a pool because the only time it was asked for as a document it
  answered `504`. An example whose input you cannot read is a screenshot.
- **A logo** (EM-35), and a generator behind it. The mark is the argument the
  tool makes, drawn: seven bars are seven logged seconds, the green one is the
  peak second at 42 req/s, and the dashed line is the mean at 21 — drawn behind
  the bars because that is what a mean is, a gridline rather than something that
  happened. `scripts/logo.sh` writes the mark, the favicon and both lockups
  from one geometry, computes the dashed line from the bar values, and
  `scripts/logo.sh check` fails on a hand-edited or stale SVG the way
  `backlog.sh check` fails on a stale roadmap. The favicon is the mark
  *restated* at 16px — four seconds and no dash — because at that size a 3px
  dash is a smudge and seven bars are a grey block. The wordmark is set in the
  system monospace with `textLength` pinning its advance width: the face the
  report is read in, no font file to fetch, and no clipping on a machine whose
  mono is wider. No raster is committed — an SVG is text and reviews in a diff;
  `docs/logo.md` carries the command for a PNG if a platform insists.
- `docs/logo.md`: what the mark means, the four files, the geometry, the
  colours, the clear space and the minimum sizes, and why there is no dark
  variant of the mark (its three colours read on white and on `#0d1117` alike).
- `scripts/logo_test.sh`: 27 checks. A generated logo makes claims that can be
  false — that the four files share one geometry, that the dashed line really
  is the mean of the bars, that no SVG fetches or runs anything, and that the
  numbers on `docs/logo.md` are the ones the generator prints.
- Images in the docs renderer: `![alt](src)` was rendering as a link with a
  stray exclamation mark in front of it, which the logo page found immediately.
  `src` attributes are now link-checked alongside `href`, so a missing image
  fails the build like a missing page, and `docs/assets/` is copied into the
  built site.
- `scripts/site_test.sh`: 39 checks over the renderer, because a renderer does
  not crash — it publishes a page with `**bold**` printed literally in it, a
  `.md` link that 404s, or a table row split in three by a `|` inside a cell.
  It found an infinite loop on a paragraph beginning with a pipe before any
  page did, and a `{#custom-anchor}` on the logo page that neither this
  renderer nor GitHub supports.

### Changed

- The operating brief (`AGENTS.md`, `CLAUDE.md`) gains **document everything, in
  the same commit** (EM-36): a change is done when a reader can find out what it
  does without reading the code, every number in the docs comes from running the
  tool over `docs/example.log`, and anything generated says that it is generated
  and how to regenerate it.
- `BACKLOG.md` gains **M7 — Who was asking, and what it cost** (EM-28 … EM-34),
  targeted at v0.4.0: bots separated from visitors, a per-host split, the
  aggregate wait attributed per class and per path, concurrency at the peak
  second, the write path in the mix, a single-file HTML report, and a versioned
  JSON schema — plus EM-35 (the logo) and EM-36 (the documentation rule, as an
  ongoing item). `ROADMAP.md` regenerated.

## [0.1.0] — 2026-08-20

First release: read a production access log, say what the traffic was made of,
and write the load-test profile from the same measurement.

### Added

- **Three log dialects, anchored on shape rather than on columns** (EM-1, EM-2,
  EM-3). HAProxy `option httplog` (with or without a syslog prefix, captured
  headers or a `-1` timer), the nginx `combined` format (plus
  `$upstream_cache_status` and `$request_time` when they are appended), and the
  Traefik JSON access log. Each parser finds the bracketed date, the timers and
  the quoted request by what they look like, because a positional reader breaks
  silently the moment a prefix shifts the columns by one — and reports a
  plausible wrong number when it does. Every parser keeps the distinction
  between the wait on the tier behind (`Tr`, `OriginDuration`) and the whole
  exchange (`Tt`, `Duration`, `$request_time`), and the report always names
  which one it measured, because the three are not comparable.
- **Dialect detection as a vote** (EM-4). Every parser is run over a sample and
  the one that read the most lines wins; a tie refuses and asks for
  `--dialect`. The dialects resemble each other closely enough that a signature
  match is unsafe: an nginx line read as HAProxy does not fail, it parses into
  nonsense.
- **gzip, standard input, and several files as one window** (EM-5). Compression
  is detected by magic number rather than by file extension, because rotated
  logs get renamed by hand.
- **Arrival rate measured at the second** (EM-6): the busiest second and its
  timestamp, the mean, and percentiles over the seconds of the window — with the
  empty seconds counted without being stored, so a week-long log costs nothing
  beyond the seconds that carry traffic. A peak that is several times the p95
  second is reported as burstiness: it is the ratio that decides whether a
  system sized on the average survives.
- **Request classes with their own peaks** (EM-7). Framework navigation
  (`_rsc`), media, static assets, search, API calls and documents, each with its
  share, its own busiest second, its own wait distribution and its own path
  cardinality. Classes do not peak together, so a class peak is not the total
  peak times a share. The set is replaceable with `--classes`.
- **Wait tails against the reverse proxy's read timeout** (EM-8). The share of
  traffic past 1s, 3s and `--read-timeout` — the last of which is not a latency
  statistic but the share of visitors who got a 504.
- **5xx concentration and HAProxy termination states** (EM-9), which separate
  "the application answered with an error" from "the application never
  answered".
- **Cache verdicts, with unknown kept distinct from zero** (EM-10). A log
  carrying no cache field reports no ratio at all; `MISS` is kept apart from
  `BYPASS`, since a cache that is never asked and one that keeps missing are
  opposite problems.
- **Coverage and audience honesty** (EM-11). Unreadable lines, the
  distinct-path cap, and whole silent seconds inside a busy window are findings
  in their own right: a proxy told not to log everything turns every share into
  a share of the logged subset. Where the log carries no client identifier, the
  report says the log cannot count people instead of producing a number that
  reads as an audience.
- **Findings and three renderers** (EM-12): worst-first everywhere, a
  measurement on every finding so a machine consumer never parses prose, and
  terminal, markdown and JSON output. The markdown report draws which tier the
  log came from, because a report whose reader cannot tell that gets compared
  with the wrong one.
- **A crowdsim profile written from the measurement** (EM-13). Measured weights,
  per-class pools, the brake set on the slowest class rather than the first one,
  the safety ceiling at the peak production has already survived, and provenance
  in `_measured` keys so nobody replays a mix measured on a different day.
- **Pools that are safe to commit** (EM-14): only paths that actually rendered —
  a 404 is cheap to serve and a pool of them reports a capacity that does not
  exist — and any segment shaped like a token or a per-user id dropped and
  counted.
- **Exit 0 whenever the analysis ran.** Findings are output, not failure;
  `--exit-on warn|bad|error` is there for a pipeline that needs to gate.
- The backlog tooling (`scripts/backlog.sh`), with `ROADMAP.md` generated from
  `BACKLOG.md` and both linted in CI.

[Unreleased]: https://github.com/Allan-Nava/edgemix/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Allan-Nava/edgemix/releases/tag/v0.1.0
