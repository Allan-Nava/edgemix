# The dialects it reads

| Dialect | Timing it trusts | What that timing means | Cache verdict |
|---|---|---|---|
| `haproxy` | `Tr` | the wait for the server's response — queueing on the tier behind | via a captured header (planned, EM-18) |
| `nginx` | `$request_time` | the **whole** exchange, including the client reading the body | `$upstream_cache_status` |
| `traefik` | `OriginDuration` | the wait on the service behind | `Cache-Status` |

These three are not comparable. A slow phone inflates `$request_time` without
any tier being busy, while `Tr` and `OriginDuration` grow only when the thing
behind is slow to answer. Every report names the field it measured for exactly
this reason.

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

## Detection

`auto` runs every parser over a sample of up to 200 lines and keeps the one that
read the most. It is a vote and not a signature match because the signatures
overlap: an nginx line read as HAProxy does not fail, it parses into plausible
nonsense. A tie produces an error asking for `--dialect` — a coin toss over
which numbers to print is worse than a question.
