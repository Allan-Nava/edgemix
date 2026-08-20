<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/edgemix-logo-dark.svg">
    <img alt="edgemix" src="docs/assets/edgemix-logo-light.svg" width="290">
  </picture>
</p>

<p align="center"><strong>Your edge log already knows what your traffic is made of. <em>edgemix</em> reads it out — and writes the load test.</strong></p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/github/go-mod/go-version/Allan-Nava/edgemix?color=10b981">
  <img alt="Zero dependencies" src="https://img.shields.io/badge/dependencies-0-10b981">
  <a href="LICENSE"><img alt="License: PolyForm Noncommercial 1.0.0" src="https://img.shields.io/badge/license-PolyForm%20Noncommercial%201.0.0-f59e0b"></a>
  <a href="https://allan-nava.github.io/edgemix/"><img alt="Documentation" src="https://img.shields.io/badge/docs-allan--nava.github.io%2Fedgemix-3b82f6"></a>
</p>

<p align="center">📖 <strong>Documentation: <a href="https://allan-nava.github.io/edgemix/">allan-nava.github.io/edgemix</a></strong> — <a href="docs/usage.md">usage</a> · <a href="docs/dialects.md">dialects</a> · <a href="docs/findings.md">findings</a> · <a href="docs/install.md">install</a> · <a href="docs/example.md">a worked example</a> · <a href="docs/profile.md">log → load test</a> · <a href="docs/logo.md">the logo</a></p>

---

**edgemix reads a production access log and says what the traffic was made of, when it actually peaked, and where it waited** — then writes a [crowdsim](https://github.com/HiWay-Media/crowdsim) profile from the same measurement, so a load test replays the mix that happened instead of one somebody estimated.

One static Go binary, no dependencies, nothing installed on the server: point it at a file.

```
$ edgemix analyze /var/log/haproxy-edge.log

log  /var/log/haproxy-edge.log  2026-08-19 18:00:00 → 2026-08-19 18:01:59 (UTC)
fmt  haproxy, 2781 requests read, 0 skipped, 0 unreadable

🔴 BAD   wait      read timeout   0.54% of requests waited longer than the 7s read timeout (15 requests)
         ↳ past the read timeout the proxy answers 504 whatever the app does next — these are visitors who saw an error page, and the margin is gone
🟡 WARN  errors    5xx            0.76% of responses were 5xx (21), most on /live (15)
         ↳ a 5xx concentrated on one path is usually the application answering, not the tier saturating — check the termination state before reading it as capacity
🟡 WARN  errors    server timeout 0.54% of requests ended in an `sD` termination — the server did not answer in time (15)
         ↳ `sD` is the proxy giving up on the backend, which is a capacity symptom; a `cD` at the same rate would have been visitors leaving
🟡 WARN  rate      burstiness     peak is 4.3× the p95 second (159 vs 37 req/s)
         ↳ the arrival is spiky, so a system sized on the average is undersized for the seconds that matter — replay the peak, do not average it
🟢 OK    mix       static         largest class is static at 49.6% of requests (1380), peaking at 78 req/s of its own
🟢 OK    rate      peak second    busiest second was 159 req/s at 18:00:55 (mean 23.2, p95 37)
🟢 OK    wait      doc            slowest class at p95 is doc (3549ms)

── the mix ─────────────────────────────────────────────────────────
class         requests   share   peak/s       p95       p99   paths
rsc_nav            920   33.1%       52      38ms      40ms       8
static            1380   49.6%       78      24ms      25ms       4
api                  6    0.2%        1     868ms     868ms       1
doc                475   17.1%       29    3549ms    7001ms       8

── waiting ─────────────────────────────────────────────────────────
Tr (wait for the server's response)
  over  1000ms       130    4.67%
  over  3000ms        39    1.40%
  over  7000ms        15    0.54%
```

## Why this exists

Every capacity conversation starts with a number from a dashboard: *400k requests an hour*. Divided by 3600 that is 111 req/s, which sounds survivable — and it is the number that made the post-mortem wrong, because the second that produced the timeouts carried 159. The distribution is in the log; the dashboard averaged it away.

Four things a log answers that an aggregate cannot:

| Question | What edgemix reads | Why the aggregate lies |
|---|---|---|
| How hard did it actually get? | the **busiest second**, and the percentiles over seconds | traffic is bursty: a peak three to ten times the mean is normal, and only the peak matters for sizing |
| What is the load *made of*? | request **classes** with their own shares and their own peak seconds | 400k static assets off a CDN and 400k renders on six single-threaded processes are different systems |
| Which tier will fail first? | the **wait** on the tier behind (HAProxy `Tr`, Traefik `OriginDuration`), per class | the slowest class is rarely the biggest one, and a mean hides it |
| How much margin is left? | the share of traffic past the **read timeout** | that share is not a latency statistic, it is the visitors who got a 504 |

And one it refuses to answer: **how many people**. An access log counts requests. Where the log carries no client identifier, edgemix says so instead of producing a number that looks like an audience.

## The pipeline this closes

```
  /var/log/haproxy-edge.log ──┐
  Traefik JSON access log ────┤
  nginx combined + cache ─────┼──► edgemix analyze ──► report (text · markdown · JSON)
  CloudFront standard log ────┤         │                 findings, worst first
  Akamai DataStream 2 ────────┘         │
                                        │
                                        └──► edgemix profile ──► profile.json ──► crowdsim
                                                  measured weights, pools,        replays the
                                                  brake class, safe peak          mix that happened
```

`crowdsim` profiles were written by hand, or recorded from one browser session. The mix in them was a guess. This is the other half: the mix comes out of the log, with the peak that actually happened as the safety ceiling — a level production has already survived.

```
$ edgemix profile --base-url https://www.example.test --name example edge.log -o profile.json
edgemix: warning: class api (0.2% of requests) has no path that rendered often enough to replay, so it is not in the profile: the mix is missing that share
edgemix: 3 classes, 3 pools, safe peak 159 req/s — validate it with `crowdsim validate`
```

The emitted profile carries its own provenance, so nobody replays a mix measured on a different day against a system that has since changed:

```jsonc
"_measured": {
  "source": "edge.log", "dialect": "haproxy",
  "window_start": "2026-08-19T18:00:00Z", "window_end": "2026-08-19T18:01:59Z",
  "requests": 2781, "peak_per_sec": 159, "peak_at": "2026-08-19T18:00:55Z",
  "latency_field": "Tr (wait for the server's response)", "latency_p95_ms": 614,
  "audience_note": "the source log identifies clients, but this profile is a request mix and says nothing about how many people produced it"
}
```

## The logs it reads

| Dialect | Recognised by | Timing it trusts | Cache verdict |
|---|---|---|---|
| `haproxy` | the bracketed accept date, the five-timer field, the quoted request | `Tr` — the wait for the server's response | via a captured header (planned) |
| `nginx` | `combined`, with or without extras | `$request_time` — the whole exchange, client read included | `$upstream_cache_status` |
| `traefik` | the JSON access log | `OriginDuration` — the wait on the service | `Cache-Status` |
| `cloudfront` | the `#Fields:` header, then tab-separated rows | `time-to-first-byte` — the wait measured at the edge | `x-edge-result-type` |
| `akamai` | DataStream 2 JSON, every value a string | `turnAroundTimeMSec` — the origin wait | `cacheStatus` |

Detection is a **vote**: every parser is run over a sample and the one that read the most lines wins. The dialects resemble each other closely enough that a signature match is not safe — an nginx line read as HAProxy does not fail, it parses into plausible nonsense — and a tie refuses rather than guessing. Pass `--dialect` to decide yourself.

The parsers anchor on the *shape* of a line, never on column positions, which is what makes a syslog prefix, a captured header block or a renamed frontend harmless. Every `awk` one-liner this tool replaces was rewritten at least once because of exactly that. CloudFront is the exception that proves it: it names its own columns in a `#Fields:` line, so they are read by name — and a file whose header was stripped is refused rather than read through an assumed order.

**A CDN log means the opposite of a proxy log**, and edgemix says which one it read. An HAProxy or nginx log never sees what the CDN answered, so its numbers are origin load and say nothing about the audience. A CloudFront or DataStream 2 log is the other way round: it records what the audience asked for, and the origin behind was asked only for the share that missed. The report gives that share, the tier diagram draws it, and an emitted profile carries the note — because a mix measured at the edge and replayed against an origin replays every request the CDN was absorbing, at a peak the origin never saw.

## What it will not do

- **No number the log did not carry.** A log with no timing field produces no latency section, not a fast one. A log with no cache header reports "unknown", never 0%. A `-1` timer is an incomplete phase, not a zero.
- **No silent cap.** Unreadable lines, dropped paths and whole silent seconds inside a busy window are findings, because a proxy told not to log everything (HAProxy's `dontlog-normal`, a sampled nginx log) turns every share into a share of the logged subset.
- **No invented allowlist.** If the log does not name the host, the emitted profile's `allow_hosts` is empty and says so. A load test aimed at the wrong hostname is indistinguishable from an attack.
- **No secret in a pool.** A path segment shaped like a token or a per-user id is dropped and counted. A profile is a file people paste into tickets.
- **No network.** edgemix reads files and nothing else: it never contacts the systems it reports on, which is why it is safe to run against a log copied out of production.
- **No exit code for findings.** A check that ran is a success. Use `--exit-on warn|bad|error` when a pipeline needs to gate.

## Install

```bash
v=0.2.0   # a release the pipeline built — the install page says which ones those are

# Homebrew — the formula is generated by the release run, from the checksums it produced
brew install "https://github.com/Allan-Nava/edgemix/releases/download/v$v/edgemix.rb"

# Docker — 2.4 MB on scratch: no shell, no CA bundle, no resolver, nothing it does not need
docker run --rm -v /var/log:/logs:ro ghcr.io/allan-nava/edgemix:v$v analyze /logs/edge.log

# A release archive, verified — six platforms, each with a sha256 and a sigstore attestation
gh attestation verify edgemix_${v}_linux_amd64.tar.gz --repo Allan-Nava/edgemix

# From source, following main (this one reports its version as `dev`)
go install github.com/Allan-Nava/edgemix/cmd/edgemix@latest
```

Full instructions, and what each way does and does not give you, are in
**[the install page](docs/install.md)** — including why `brew upgrade` will not
find the next release yet, and why edgemix cannot go into `homebrew-core`.

## Usage

```bash
# the whole log
edgemix analyze /var/log/haproxy-edge.log

# one hour of a rotated, gzipped log — compression is detected, not assumed from the name
edgemix analyze --since '2026-08-19 18:00:00' --until '2026-08-19 19:00:00' edge.log.1.gz

# a report to commit next to an incident
edgemix analyze --format md edge.log > docs/incidents/2026-08-19-peak.md

# from a pipe, with the dialect stated
ssh lb1 'tail -n 200000 /var/log/haproxy.log' | edgemix analyze --dialect haproxy -

# gate a pipeline on the read-timeout tail
edgemix analyze --exit-on bad edge.log

# a load-test profile from the same window
edgemix profile --base-url https://www.example.test --read-timeout 7s edge.log -o profile.json
```

Flags worth knowing:

| Flag | What it changes |
|---|---|
| `--dialect` | skip detection: `haproxy`, `nginx`, `traefik`, `cloudfront`, `akamai` |
| `--tz` | the zone of a log whose dates carry no offset (HAProxy writes local time with nothing to read it from — the report states which zone it assumed) |
| `--read-timeout` | your reverse proxy's read timeout, the threshold the one BAD finding is made of (default 7s) |
| `--tails` | extra wait thresholds to report, e.g. `500ms,1s,3s` |
| `--xff-capture` | which slot of HAProxy's captured request headers holds `X-Forwarded-For` |
| `--classes` | a JSON file replacing the built-in class rules |
| `--exit-on` | exit 1 when a finding reaches `warn`, `bad` or `error` |
| `--pool-size`, `--min-ok` | how many paths per class enter a profile pool, and how reliably they must have rendered |

## Request classes

A class is a kind of request that costs the same thing to serve. The built-in set covers a server-rendered site: `rsc_nav` (framework navigation — anything with an `_rsc` parameter), `media`, `static`, `search`, `api`, and `doc` as the fallback. Two of those exist because of measurements that were misread without them:

- **Framework navigation is not a page view.** It is often the largest class by count while being the cheapest per request, and averaged into documents it makes the document tier look fast.
- **Search is kept apart.** It is usually the most expensive request in the mix, and averaged in it hides the thing that falls over first.

Replace the set with `--classes rules.json` — first match wins, so order is data:

```json
{
  "fallback": "page",
  "rules": [
    { "name": "nav", "kind": "rsc", "query_params": ["_rsc"] },
    { "name": "asset", "path_suffixes": [".js", ".css", ".webp"] },
    { "name": "api", "path_prefixes": ["/api/"] }
  ]
}
```

## Where it sits

| Tool | Question it answers |
|---|---|
| [checkfleet](https://github.com/Allan-Nava/checkfleet) | is the infrastructure healthy *right now*? |
| [segcheck](https://github.com/Allan-Nava/segcheck) | do the media segments match what the manifest claims? |
| [crowdsim](https://github.com/HiWay-Media/crowdsim) | what happens when the load arrives? |
| **edgemix** | **what was the load, actually?** |

edgemix is the measurement the other three cannot make: it looks backwards, at traffic that already happened, and turns it into evidence — and into the profile crowdsim needs to look forwards.

## Contributing

`BACKLOG.md` is the single source of truth for planned work (`ROADMAP.md` is generated from it — run `scripts/backlog.sh roadmap`). Tests come before implementation; `go test -race ./...` and `gofmt` are CI gates, as is having no dependencies at all.

The [logo](docs/logo.md) is generated too: `scripts/logo.sh` writes the four SVGs from one geometry, and the dashed line in the mark is the arithmetic mean of its bars rather than a line somebody placed — a mark that lied about its own numbers would be a poor sign for a measurement tool. `scripts/logo.sh check` is a CI gate.

The documentation is the site: [`docs/`](docs/) is rendered to [allan-nava.github.io/edgemix](https://allan-nava.github.io/edgemix/) by `scripts/site.sh` — POSIX sh and awk, no Jekyll and nothing to install. A new page goes into the nav list at the top of that script, and `scripts/site.sh check` (a CI gate, with `scripts/site_test.sh`) fails on a dead internal link or a dead anchor rather than leaving it for a reader to find.

## License

[PolyForm Noncommercial 1.0.0](LICENSE) — free for noncommercial use. For a commercial licence, get in touch.
