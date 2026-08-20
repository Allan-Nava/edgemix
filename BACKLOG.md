# Backlog — edgemix

Single source of truth for what is planned. Items keep a stable `EM-n` id so
commits, the CHANGELOG and issues can reference them. New ideas go here rather
than into scattered TODO comments.

[ROADMAP.md](ROADMAP.md) is a **generated** view of this file, grouped by
milestone. Do not edit it by hand — run `scripts/backlog.sh roadmap` after
touching this file, or CI will fail.

## How to write an item

```
## M3 — Title of the milestone <!-- ms: target=v0.2.0 phase=now -->

- [ ] **EM-15 — Short name**: what it is, why it earns its place, what it
  needs to touch. <!-- em: prio=high size=L labels=parser,check -->
```

- The **id never changes**. Adding an item means taking the next free number,
  never reusing a retired one. Moving an item to a different milestone is fine;
  renumbering it is not.
- `- [ ]` is open, `- [x]` is shipped, and a shipped item carries the release it
  went out in: `ver=0.1.0`.
- Metadata lives in a trailing `<!-- em: ... -->` comment. Keys: `prio`
  (`high|med|low`), `size` (`S|M|L|XL`), `labels` (comma-separated, from the
  vocabulary below), `ver` (shipped items only).
- Milestone metadata is a trailing `<!-- ms: ... -->` on the heading. Keys:
  `target` (the release it aims at, or `ongoing`) and `phase`
  (`shipped|now|next|later|ongoing`).
- Labels: `parser`, `check`, `output`, `cli`, `delivery`, `integration`,
  `tests`, `docs`, `release`, `project`.

`scripts/backlog.sh lint` enforces all of the above; `scripts/backlog.sh next`
prints what to pick up.

## M1 — Read the log <!-- ms: target=v0.1.0 phase=shipped -->

- [x] **EM-1 — HAProxy `option httplog` parser**: anchored on the bracketed
  accept date, the five-timer field and the last quoted request, so a syslog
  prefix, a captured header block or a renamed frontend cannot shift the
  columns. Reads Tw, Tr and Tt, the termination state, and captured Host and
  X-Forwarded-For. <!-- em: prio=high size=L labels=parser ver=0.1.0 -->
- [x] **EM-2 — nginx `combined` parser**: plus `$upstream_cache_status` and
  `$request_time` when they are appended, recognised by shape rather than by
  position. <!-- em: prio=high size=M labels=parser ver=0.1.0 -->
- [x] **EM-3 — Traefik JSON access log parser**: `OriginDuration` separately
  from `Duration`, since only the first one is the wait on the service behind.
  <!-- em: prio=high size=M labels=parser ver=0.1.0 -->
- [x] **EM-4 — Dialect detection as a vote**: every parser is run over a sample
  and the one that read the most lines wins; a tie refuses rather than guessing,
  because an nginx line read as HAProxy parses into plausible nonsense.
  <!-- em: prio=high size=M labels=parser,cli ver=0.1.0 -->
- [x] **EM-5 — gzip, stdin and several files as one window**: compression
  detected by magic number, not by extension, because rotated logs get renamed.
  <!-- em: prio=med size=S labels=cli ver=0.1.0 -->

## M2 — Measure what the log can answer <!-- ms: target=v0.1.0 phase=shipped -->

- [x] **EM-6 — Arrival rate at the second**: peak, its timestamp, and
  percentiles over the seconds of the window, with the empty seconds counted
  and not stored. An hourly mean is the number that made every "we were nowhere
  near capacity" post-mortem wrong. <!-- em: prio=high size=M labels=check ver=0.1.0 -->
- [x] **EM-7 — Request classes with their own peaks**: documents, framework
  navigation, static assets, media, API and search, each with its share, its own
  busiest second and its own wait distribution. <!-- em: prio=high size=M labels=check ver=0.1.0 -->
- [x] **EM-8 — Wait tails against the read timeout**: the share of traffic past
  1s, 3s and the proxy's own read timeout, which is the share of visitors who
  got a 504 rather than a slow page. <!-- em: prio=high size=M labels=check ver=0.1.0 -->
- [x] **EM-9 — 5xx and termination states**: the top paths answering 5xx, and
  HAProxy's `sD` share, which separates "the application returned an error" from
  "the application never returned". <!-- em: prio=high size=S labels=check ver=0.1.0 -->
- [x] **EM-10 — Cache verdicts, with unknown kept distinct from zero**: a log
  carrying no cache field reports no ratio at all, and MISS is kept apart from
  BYPASS. <!-- em: prio=med size=S labels=check ver=0.1.0 -->
- [x] **EM-11 — Coverage and audience honesty**: unreadable lines, the
  distinct-path cap, whole silent seconds inside a busy window (a proxy told not
  to log everything), and whether the log carries a client identifier at all.
  <!-- em: prio=high size=M labels=check ver=0.1.0 -->
- [x] **EM-12 — Findings and three renderers**: worst-first ordering, a number
  on every finding, and terminal, markdown and JSON output — the markdown one
  drawing which tier the log came from. <!-- em: prio=high size=M labels=output ver=0.1.0 -->

## M3 — Feed the load test <!-- ms: target=v0.1.0 phase=shipped -->

- [x] **EM-13 — crowdsim profile emitter**: measured weights, per-class pools,
  the brake on the slowest class, the safety ceiling at the peak production has
  already survived, and provenance in `_measured` keys.
  <!-- em: prio=high size=L labels=integration ver=0.1.0 -->
- [x] **EM-14 — Pools that are safe to commit**: only paths that actually
  rendered, and any segment shaped like a token or a per-user id dropped and
  counted. <!-- em: prio=high size=M labels=integration ver=0.1.0 -->

## M4 — The layers this cannot see yet <!-- ms: target=v0.2.0 phase=now -->

- [ ] **EM-15 — CDN log dialects**: CloudFront and Akamai delivery logs. Today
  everything above the edge is invisible, which is exactly where the difference
  between origin load and audience lives — and the reason a report has to keep
  saying "origin-side load, not audience".
  <!-- em: prio=high size=L labels=parser -->
- [ ] **EM-16 — `edgemix compare` for two windows**: before and after a
  deploy, a cache change or an incident, with the denominator stated on every
  delta. A share of a total that itself moved is the most reliable way to
  misread two reports side by side. <!-- em: prio=high size=M labels=check,cli -->
- [ ] **EM-17 — Per-second series export**: TSV of the rate, the wait
  percentiles and the class mix per second, for plotting a peak rather than
  describing it. <!-- em: prio=med size=S labels=output -->
- [ ] **EM-18 — Cache-Status (RFC 9211) and captured cache headers**: read a
  Souin or CDN verdict from a Traefik field or an HAProxy capture, so the hit
  ratio stops being unknown on the stacks that do have one.
  <!-- em: prio=med size=M labels=parser,check -->
- [ ] **EM-19 — Declared sampling**: when a proxy is known to log a fraction of
  requests, accept the rate and scale the absolute numbers, with the assumption
  printed next to every scaled figure. <!-- em: prio=med size=M labels=check -->
- [ ] **EM-20 — Profile linter**: check an emitted (or hand-edited) profile for
  paths carrying a query string, a token-shaped segment or a hostname outside
  the allowlist, so a profile can be reviewed before it is run.
  <!-- em: prio=med size=S labels=integration,cli -->

## M5 — Delivery and depth <!-- ms: target=v0.3.0 phase=later -->

- [ ] **EM-21 — Prometheus textfile output**: the peak, the tails and the 5xx
  share as metrics, for a cron that keeps a rolling baseline.
  <!-- em: prio=med size=S labels=output,integration -->
- [ ] **EM-22 — Docker image and a release pipeline**: a static binary, a
  scratch image and signed archives per platform.
  <!-- em: prio=med size=M labels=delivery,release -->
- [ ] **EM-23 — Documentation site**: the dialect table, the finding catalogue
  and a worked example, published from `docs/`.
  <!-- em: prio=med size=M labels=docs -->
- [ ] **EM-24 — Fuzz the parsers**: a log is bytes from the outside — a line cut
  by a rotation, a captured header with a quote in it, a JSON object with a
  number where a string belongs. A panic there is a crash in someone else's CI.
  <!-- em: prio=med size=M labels=tests,parser -->
- [ ] **EM-25 — Apache and AWS ALB dialects**: the two most common logs that are
  neither of the three read today. <!-- em: prio=low size=M labels=parser -->
- [ ] **EM-26 — Infer a visitor journey**: group a client's requests inside a
  short window into document plus fan-out, to write crowdsim's `journey.json`
  from measured traffic instead of a browser recording. Only possible where the
  log carries an identity, and must refuse where it does not.
  <!-- em: prio=low size=L labels=integration,check -->

## M6 — Ongoing <!-- ms: target=ongoing phase=ongoing -->

- [ ] **EM-27 — Keep the finding catalogue and its thresholds documented**:
  every threshold in `internal/analyze/findings.go` is a claim about what a
  number means operationally, and each one belongs in the docs next to the
  measurement it judges. <!-- em: prio=med size=S labels=docs,check -->
