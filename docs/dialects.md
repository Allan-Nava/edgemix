# The dialects it reads

| Dialect | Where it sits | Timing it trusts | What that timing means | Cache verdict |
|---|---|---|---|---|
| `haproxy` | origin side | `Tr` | the wait for the server's response — queueing on the tier behind | via a captured header (planned, EM-18) |
| `nginx` | origin side | `$request_time` | the **whole** exchange, including the client reading the body | `$upstream_cache_status` |
| `traefik` | origin side | `OriginDuration` | the wait on the service behind | `Cache-Status` |
| `cloudfront` | **above the cache** | `time-to-first-byte` | the wait measured at the edge — the origin on a miss, the cache on a hit | `x-edge-result-type` |
| `akamai` | **above the cache** | `turnAroundTimeMSec` | the origin wait, measured at the edge | `cacheStatus` |

These timings are not comparable. A slow phone inflates `$request_time` without
any tier being busy, while `Tr` and `OriginDuration` grow only when the thing
behind is slow to answer. Every report names the field it measured for exactly
this reason.

## Above the cache, or below it

The `Where it sits` column changes what every number in a report means, and it
is the one distinction worth getting right before reading any of them.

An **origin-side** log — HAProxy, nginx, Traefik — never sees a request the CDN
answered. Its numbers are origin load, and they say nothing about the audience:
a cache change upstream moves them without a single visitor behaving
differently.

A **CDN** log is the other way round. It records what the audience asked for,
and the tier behind was asked only for the share that missed. The report says
so, the tier diagram draws it, the `edge` finding gives the share, and an
emitted profile carries an `edge_note` — because a mix measured at the edge and
replayed against an origin replays every request the CDN was absorbing, at a
peak the origin never saw.

```
    origin-side log                       CDN log

  visitors                              visitors
     │                                     │
  [ CDN ]  ─── invisible ───            ┌──▼──────────┐
     │                                  │  the CDN    │ ◄── the log
  ┌──▼──────────┐                       └──┬──────────┘
  │  HAProxy    │ ◄── the log              │  misses only
  └──┬──────────┘                       ┌──▼──────────┐
     │                                  │  the origin │
  ┌──▼──────────┐                       └─────────────┘
  │  the app    │
  └─────────────┘
   origin load,                          audience-side,
   not audience                          origin saw a share

## HAProxy

`option httplog`, with or without a syslog prefix, captured headers, or
incomplete phases:

```
Aug 19 18:06:38 lb1 haproxy[2411]: 203.0.113.7:53412 [19/Aug/2026:18:06:38.123] fe_https~ be_app/app3 12/0/1/845/860 200 15321 - - ---- 1200/1200/8/2/0 0/0 {www.example.test|Mozilla/5.0} "GET /news/latest?page=2 HTTP/1.1"
                                   └ client        └ accept date              └ tiers    └ timers      └ st └ bytes  └ ─ └ term └ conns     └ queues └ captures     └ request
```

What the parser anchors on: the first bracketed group **that parses as a date**
(the first one on a syslog line is the pid), the five-timer field found by its
shape, and the **last** quoted field. Everything else is read at a fixed offset
from the timers, never from the start of the line.

Traps this handles, each of which has broken an `awk` one-liner:

- A syslog prefix shifts every column by four or five fields.
- A captured header block appears only on some frontends, shifting the request.
- `-1` timers mean the phase never completed. They are absent, not zero — read
  as zero they become the fastest requests in the log.
- `"<BADREQ>"` is a request the proxy itself could not read. It is still
  traffic, so it is counted rather than dropped.
- The accept date has **no zone offset**. `--tz` states the assumption.
- A captured `X-Forwarded-For` may be a dash. That is the header being absent,
  not a client identity.

The termination state is kept: `sD` is the proxy giving up on the backend
(a capacity symptom), `cD` is the client leaving, and reading a 5xx rate without
them attributes the application's own errors to saturation.

## nginx

The `combined` format, plus two optional extras recognised by shape:

```
203.0.113.7 - - [19/Aug/2026:18:06:38 +0000] "GET /a.js HTTP/1.1" 200 1234 "-" "UA" HIT 0.042
                                                                                    │    └ $request_time
                                                                                    └ $upstream_cache_status
```

Anything else appended is ignored rather than guessed at: a `log_format`
edgemix does not understand must not produce a number that looks measured.
`MISS` and `BYPASS` are kept apart — a cache that was asked and had nothing and
a rule that never asks are opposite problems.

## Traefik

The JSON access log (`accessLog.format: json`). `Duration` is the whole
exchange, `OriginDuration` the wait on the service; `DownstreamStatus` is what
the client got, which on a retry differs from what the origin answered.
Traefik's own application log lines share the file when `accessLog` is not
separated, and are skipped rather than counted as unreadable.

## CloudFront

The standard access log, as the console and S3 deliver it: two header lines and
then tab-separated rows.

```
#Version: 1.0
#Fields: date time x-edge-location sc-bytes c-ip cs-method cs(Host) cs-uri-stem sc-status … time-taken … time-to-first-byte …
2026-08-19→18:06:38→MXP64-C1→15321→203.0.113.7→GET→d111.cloudfront.net→/news/latest→200→…
```

(`→` marks a tab above; the real file is tab-separated, and an empty column is
written `-`.)

This is the one dialect that **names its own columns**, and the parser reads
them by name for the same reason the others anchor on shape: the field list is
not fixed. It has grown over the years, a distribution can be configured with
fewer fields, and two files written years apart and concatenated into one window
carry two different headers — which the parser re-reads, so the second file is
never read through the first file's mapping.

Traps this handles:

- **A file with no `#Fields:` line cannot be read, and is refused.** The columns
  are named nowhere else, and assuming the current default order would produce a
  plausible wrong report on any distribution configured differently. Keep the
  rotation intact, or concatenate the files with their headers.
- **`x-host-header` is the host the viewer asked for; `cs(Host)` is the
  distribution domain.** Reading the wrong one reports every request as going to
  `d111111abcdef8.cloudfront.net`, which then lands in an emitted profile's
  `allow_hosts` and aims a load test at the CDN. `cs(Host)` is used only when
  the other is absent.
- **`time-taken` is the whole exchange, `time-to-first-byte` is the wait.** The
  second is the one reported, and on a cache hit it is the edge answering rather
  than the origin — so the percentiles blend two systems, and both the finding
  and the field name say so.
- **A `-` is an absent value**, never a zero and never a literal dash. A dash in
  `time-to-first-byte` means no wait was recorded, so that request contributes
  nothing to the distribution rather than contributing the fastest number in it.
- **`sc-status 000`** is the viewer closing the connection before an answer. It
  is a request that happened, so it is counted, with a status of 0.
- The dates are two columns and always **UTC**. There is nothing to interpret,
  so `--tz` does not apply.

Real-time (Kinesis) delivery has a configurable field list and no header, so it
is not read: without the names there is nothing to anchor on.

## Akamai

DataStream 2, one JSON object per line:

```json
{"cliIP":"203.0.113.7","reqTimeSec":"1787162798.123","reqMethod":"GET","reqHost":"www.example.test",
 "reqPath":"/news/latest","statusCode":"200","cacheStatus":"1","turnAroundTimeMSec":"45",
 "transferTimeMSec":"12","totalBytes":"15321"}
```

- **Every value is a string**, numbers and the timestamp included. A parser that
  decodes `statusCode` as an int fails on the real thing; one that accepts only
  strings fails on a pipeline that re-typed the record on its way to S3. Both
  are read, and a value that is present but not a number makes the line
  unreadable rather than zero.
- **`reqTimeSec` is parsed as text**, not through a float: an epoch with
  milliseconds in it does not survive a `float64` round trip, and the timestamp
  is the one number in this format that has to be exact.
- **`turnAroundTimeMSec` is the origin wait**; `transferTimeMSec` is the viewer
  reading the body. There is no field for the whole exchange, and adding the two
  would produce a number that looks like `$request_time` without being one — so
  this dialect reports no total at all.
- **`cacheStatus` is `1` for a hit and `0` for a miss.** A stream configured
  without the field reports no verdict, which is not a miss.
- Both this and Traefik are JSON, so each parser refuses the other's lines
  outright. A detection tie would ask for `--dialect` on a log only one of them
  can actually read.

The older Log Delivery Service files are a different shape and are not claimed:
`--dialect akamai` reads nothing from one, which is the answer edgemix prefers
to a wrong number.

## Detection

`auto` runs all five parsers over a sample of up to 200 lines and keeps the one
that read the most. It is a vote and not a signature match because the signatures
overlap: an nginx line read as HAProxy does not fail, it parses into plausible
nonsense. A tie produces an error asking for `--dialect` — a coin toss over
which numbers to print is worse than a question.
