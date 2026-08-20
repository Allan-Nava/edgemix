# A worked example

Every number on this page comes from
[`docs/example.log`](https://github.com/Allan-Nava/edgemix/blob/main/docs/example.log),
a 40-line HAProxy log written by hand for this page. It is short enough to read
in full and to check every figure against — which is the point: a worked example
whose input you cannot see is a screenshot.

```bash
edgemix analyze docs/example.log
edgemix profile --base-url https://www.example.test --name example docs/example.log
```

## What is in the log

Six seconds of a server-rendered site behind HAProxy, with five things planted
in it:

```
203.0.113.3:40013 [19/Aug/2026:18:00:00.104] fe_https~ be_app/app2 0/0/0/18/19 200 14802 - - ---- 24/24/1/1/0 0/0 {www.example.test} "GET / HTTP/1.1"
203.0.113.4:40026 [19/Aug/2026:18:00:00.221] fe_https~ be_app/app3 0/0/0/3/4 200 48211 - - ---- 24/24/2/2/0 0/0 {www.example.test} "GET /_next/static/chunks/main-8f2a.js HTTP/1.1"
203.0.113.7:40065 [19/Aug/2026:18:00:00.910] fe_https~ be_app/app3 0/0/0/41/43 200 9021 - - ---- 24/24/0/2/0 0/0 {www.example.test} "GET /news/latest?_rsc=1k2j3 HTTP/1.1"
Aug 19 18:00:02 lb1 haproxy[2411]: 203.0.113.2:40364 [19/Aug/2026:18:00:02.779] fe_https~ be_app/app2 0/0/0/7301/7310 504 217 - - sD-- 24/24/3/1/0 0/0 {www.example.test} "GET /video/highlights HTTP/1.1"
203.0.113.3:40377 [19/Aug/2026:18:00:02.822] fe_https~ be_app/app3 0/0/0/513/520 500 512 - - ---- 24/24/4/2/0 0/0 {www.example.test} "GET /api/live/scores HTTP/1.1"
```

| Planted | Where | What it should produce |
|---|---|---|
| a numerous cheap class | 21 of 40 requests are `.js`, `.css`, `.svg`, `.webp` | `static` is the largest class and the fastest one |
| a slow expensive class | four documents wait between 1.8s and 7.3s | `doc` is the class to brake a load test on, at 22.5% of the mix |
| one request past the read timeout | the `504` line, `Tr` = 7301ms, termination `sD` | one BAD finding, and `sD` reported separately from the 5xx rate |
| an application error | `/api/live/scores` answering `500` in 513ms | a 5xx finding attributed to the path, and an `api` class with no replayable path |
| two syslog-prefixed lines | `Aug 19 18:00:02 lb1 haproxy[2411]:` | nothing at all: the numbers are identical with and without the prefix |

The last row is the whole reason the parsers anchor on shape. The first
bracketed group on those two lines is `[2411]`, the pid — a reader that takes
the first `[...]` as the date skips them, and a log shipped through syslog is
*every* line.

## `edgemix analyze`

```
$ edgemix analyze docs/example.log
edgemix: reading docs/example.log as haproxy
log  docs/example.log  2026-08-19 18:00:00 → 2026-08-19 18:00:05 (UTC)
fmt  haproxy, 40 requests read, 0 skipped, 0 unreadable

🔴 BAD   errors    5xx            5.0% of responses were 5xx (2), most on /api/live/scores (1)
         ↳ a 5xx concentrated on one path is usually the application answering, not the tier saturating — check the termination state before reading it as capacity
🔴 BAD   wait      read timeout   2.5% of requests waited longer than the 7s read timeout (1 requests)
         ↳ past the read timeout the proxy answers 504 whatever the app does next — these are visitors who saw an error page, and the margin is gone
🟡 WARN  errors    server timeout 2.5% of requests ended in an `sD` termination — the server did not answer in time (1)
         ↳ `sD` is the proxy giving up on the backend, which is a capacity symptom; a `cD` at the same rate would have been visitors leaving
🟡 WARN  wait      1s tail        12.5% of requests waited longer than a second (5 requests)
         ↳ a queue this deep at a second means the tier behind has no spare worker at peak, not that a query is slow
🟢 OK    audience  log            every line carries a client identifier (100.0%)
         ↳ distinct addresses can be counted from this log, but an address is not a person: behind a CDN or a mobile carrier it is neither one visitor nor a stable one
🟢 OK    cache     log            this log carries no cache verdict, so the hit ratio is unknown
         ↳ unknown is not zero: add $upstream_cache_status to nginx's log-format, or capture the layer's cache header in HAProxy, before concluding anything about caching
🟢 OK    mix       static         largest class is static at 52.5% of requests (21), peaking at 9 req/s of its own
         ↳ a load test firing a flat URL list reproduces none of this: the class peaks do not coincide, and the cheap class is usually the numerous one
🟢 OK    rate      peak second    busiest second was 18 req/s at 18:00:02 (mean 6.7, p95 18)
         ↳ size against this second, not the mean: it is the one that produced the timeouts
🟢 OK    wait      all classes    Tr (wait for the server's response): p50 3ms, p95 1889ms, p99 7301ms, max 7301ms
         ↳ this is time spent waiting for the tier behind, not time spent computing: it grows with queueing, so it is the earliest sign of a saturated tier
🟢 OK    wait      doc            slowest class at p95 is doc (7301ms)
         ↳ this is the class to brake a load test on: the mix falls over here first

── arrival ─────────────────────────────────────────────────────────
peak 18 req/s at 2026-08-19 18:00:02 · p99 18 · p95 18 · p50 5 · mean 6.7 over 6s

── the mix ─────────────────────────────────────────────────────────
class         requests   share   peak/s       p95       p99   paths
rsc_nav              7   17.5%        4      48ms      48ms       3
static              21   52.5%        9       3ms       4ms       5
search               2    5.0%        1    1204ms    1204ms       1
api                  1    2.5%        1     513ms     513ms       1
doc                  9   22.5%        4    7301ms    7301ms       3

── waiting ─────────────────────────────────────────────────────────
Tr (wait for the server's response)
  over  1000ms         5   12.50%
  over  3000ms         1    2.50%
  over  7000ms         1    2.50%

── answers ─────────────────────────────────────────────────────────
2xx 38 (95.0%) · 5xx 2 (5.0%)
  5xx      1  /api/live/scores
  5xx      1  /video/highlights

── busiest paths ───────────────────────────────────────────────────
         7   17.5%  /_next/static/chunks/main-8f2a.js
         6   15.0%  /news/champions-league-final
         4   10.0%  /
         4   10.0%  /_next/static/css/app-1c9d.css
         4   10.0%  /logo.svg
         3    7.5%  /_next/static/chunks/framework-3b7c.js
         3    7.5%  /icons/badge.webp
         3    7.5%  /news/latest
         3    7.5%  /video/highlights
         2    5.0%  /search

10 findings: 6 OK, 2 WARN, 2 BAD, 0 ERROR
```

## How to read it

**The mean is 6.7 req/s and the peak is 18.** Sizing against 6.7 would be
sizing against a second that never happened, which is the same mistake as
dividing an hourly total by 3600. Every finding about waiting belongs to
`18:00:02`, the second that carried 18 of the 40 requests.

**The largest class is the cheapest one.** `static` is 52.5% of the traffic at a
p95 of 3ms; `doc` is 22.5% at a p95 of 7301ms. A load test that fires the ten
busiest paths in measured proportion spends half its budget on files that cost
nothing to serve, and never queues the tier that actually broke. That is why
[the profile](profile.md) keeps a pool per class and brakes on one of them.

**The peaks do not coincide.** `static` peaks at 9 req/s of its own and
`rsc_nav` at 4, inside a second that carried 18 requests in total. Multiplying
the total peak by a class share invents a number for every class.

**`sD` is the finding to act on, not the 5xx rate.** Two responses were 5xx, and
they are two different incidents: `/api/live/scores` returned `500` in 513ms —
the application answering — while `/video/highlights` never answered at all and
the proxy gave up at 7301ms and wrote `504`. A 5xx rate that lumps them together
reports an application bug as saturation, or the reverse.

**One request past the read timeout is one visitor who saw an error page.** 2.5%
here is a tiny log, but the finding is BAD at anything over 0.1% for a reason:
past that line the answer is a `504` whatever the application does next, so the
number is not a latency statistic.

**p95 over six seconds is the peak.** `p99 18 · p95 18` is not burstiness
information — with six seconds of window the busiest second *is* the 95th
percentile. Burstiness (peak ÷ p95) only becomes a claim over a window long
enough to have quiet seconds in it: a few minutes at minimum, an hour if the
question is capacity. The report prints the window on the first line so this is
visible rather than assumed.

**The cache ratio is unknown, and unknown is not zero.** This HAProxy log
captures `Host` and nothing else. Reporting a 0% hit ratio from it would be an
invented number; the finding says the log carries no verdict and what to add to
it.

## `edgemix profile`

The same measurement, transcribed into a [crowdsim](https://github.com/HiWay-Media/crowdsim)
profile:

```
$ edgemix profile --base-url https://www.example.test --name example docs/example.log -o profile.json
edgemix: reading docs/example.log as haproxy
edgemix: warning: class api (2.5% of requests) has no path that rendered often enough to replay, so it is not in the profile: the mix is missing that share
edgemix: 4 classes, 4 pools, safe peak 18 req/s — validate it with `crowdsim validate`
```

Two refusals are visible in that output and in the file it wrote:

```jsonc
"classes": [
  { "name": "static",  "weight": 52.5, "pool": "static",
    "_note": "21 requests measured, 52.5% of the mix, own peak 9 req/s, 5 distinct paths seen, p95 3ms on Tr (wait for the server's response)" },
  { "name": "doc",     "weight": 22.5, "pool": "doc",
    "_note": "9 requests measured, 22.5% of the mix, own peak 4 req/s, 3 distinct paths seen, p95 7301ms on Tr (wait for the server's response)" }
],
"pools": {
  "doc": ["/", "/news/champions-league-final"]
},
"slo": {
  "max_p95_ms": 5000, "guillotine_ms": 7000, "brake_class": "doc",
  "_note": "guillotine_ms is the reverse proxy's read timeout as given to edgemix (7s), not something measured; brake_class is the slowest class at p95 in the measured window"
},
"safety": {
  "safe_peak_rps": 18,
  "allow_hosts": ["www.example.test"]
}
```

- **`api` is dropped loudly.** Its only path answered `500`, so there is nothing
  in the window that shows what a working `/api/live/scores` costs. Folding its
  2.5% into another class would have measured the wrong request type under the
  right label, so the share is stated as missing instead.
- **`/video/highlights` is not in the `doc` pool**, although the log saw it three
  times, because the one time it was requested as a document it timed out. A
  pool of paths that did not render measures the error handler — and a `504` is
  cheaper to serve than the render it stands in for.

Everything else in the file is either measured (`weight`, `pool`,
`safe_peak_rps`, `allow_hosts`, `brake_class`) or something you supplied
(`base_url`, `guillotine_ms` from `--read-timeout`, `max_p95_ms` from
`--max-p95`). The `_measured` block carries the window, the dialect and the
timing field, so a profile that outlives the deploy it was measured on says so.

## What this window cannot tell you

- **How many people.** 40 requests from 7 addresses is not 7 visitors. The
  audience finding states the limit and never estimates.
- **What the CDN absorbed.** This is the HAProxy log, so it is origin-side load.
  A hit at the edge never reached it.
- **Whether 18 req/s is your peak.** It is the peak of six seconds. Run it over
  a rotated day, or a `--since`/`--until` window around the incident, and the
  same command answers a question worth asking.
