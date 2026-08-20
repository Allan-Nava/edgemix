# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[0.1.0]: https://github.com/Allan-Nava/edgemix/releases/tag/v0.1.0
