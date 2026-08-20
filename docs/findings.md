# The findings, and the thresholds behind them

A finding is one statement about one measurement. It carries the number it is
made of (`value` + `unit`) so a machine consumer never parses prose, and a
`hint` that says what it means operationally.

Statuses, worst first: **ERROR** (the analysis could not run, or ran over an
incomplete record — it sorts above BAD because a hole in the coverage
invalidates everything below it), **BAD**, **WARN**, **OK**. A finding that
reports a healthy measurement is still emitted: *"nothing waited past the read
timeout"* is the sentence you want before a launch, and a report that only
speaks when something is wrong cannot be used as a baseline.

Every threshold below lives in `internal/analyze/findings.go` as a constant with
its reasoning, not in a config file — a threshold is a claim about what a number
means, and a claim belongs where it can be argued with rather than where it can
be tuned until the report is green.

## coverage

| Target | Level | When | Why it matters |
|---|---|---|---|
| `log` | ERROR | no request line could be read at all | wrong dialect, a custom `log_format`, or not an access log: nothing below is a measurement |
| `log` | ERROR / WARN | ≥ 2% / ≥ 0.2% of request lines unreadable | the shares are computed over the lines that did read, which is not the whole traffic |
| `paths` | WARN | the distinct-path cap was reached | top-path lists and any emitted pool are incomplete |
| `window` | WARN | ≥ 20% of seconds carry no logged request, at a mean of ≥ 1 req/s | at that rate traffic does not stop for whole seconds: the proxy is not logging everything (`dontlog-normal`, sampling), so every share is of the logged subset |

## rate

| Target | Level | When |
|---|---|---|
| `peak second` | OK | always — the busiest second, its timestamp, the mean and the p95 second |
| `burstiness` | WARN | peak ≥ 3× the p95 second |

The percentiles are over **seconds**, not requests: p95 is the rate the busiest
5% of seconds exceeded. Peak ÷ p95 is how spiky the arrival is, and it is what
decides whether a system sized on the average survives the seconds that matter.

## wait

| Target | Level | When |
|---|---|---|
| `all classes` | OK | always — p50/p95/p99/max of the dialect's timing field, or a statement that the log carries none |
| `read timeout` | BAD | > 0.1% of requests waited longer than `--read-timeout` |
| `read timeout` | WARN | at least one did |
| `read timeout` | OK | none did — the margin is intact at this load |
| `1s tail` | WARN | ≥ 5% waited longer than a second |
| *(class name)* | OK | the slowest class at p95: the one to brake a load test on |

The read-timeout share is not a latency statistic. Past that line the proxy
answers 504 whatever the application does next, so the number is the share of
visitors who saw an error page.

## errors

| Target | Level | When |
|---|---|---|
| `5xx` | BAD / WARN | ≥ 2% / ≥ 0.5% of responses, with the top path named |
| `server timeout` | WARN | ≥ 0.5% of requests ended `sD` (HAProxy) |

A 5xx concentrated on one path is usually the application answering, not the
tier saturating. `sD` is what separates them: the proxy gave up waiting.

## mix

Always OK: the largest class, its share, its count and its **own** peak second.
Classes do not peak together, and the numerous class is usually the cheap one —
which is why a load test firing a flat URL list reproduces none of this.

## edge

Only for a **CDN** dialect (`cloudfront`, `akamai`), because only there is the
log written above the cache. See [the dialects](dialects.md#above-the-cache-or-below-it).

| Target | Level | When | Why it matters |
|---|---|---|---|
| `origin share` | OK | the log carries a cache verdict | the share that missed is the only bridge between this report and the capacity of the tier behind: the mix and the peak are what the audience asked for, and the origin was asked for that share of them |
| `origin share` | WARN | the log carries no cache verdict | the traffic is measurable and the origin's share of it is not, so every capacity number in the report is audience-side only |

This is the finding that stops the most expensive misreading of a traffic
report: an origin sized on audience-side numbers is sized for the traffic a CDN
was absorbing. The same reversal runs through the report — the tier diagram
draws the CDN on top, the `wait` hint says the percentiles blend an edge answer
with an origin one, and an emitted profile carries an `edge_note` saying that
`safe_peak_rps` is a level the *edge* survived.

## cache

| Target | Level | When |
|---|---|---|
| the cache field | OK | the share of judged responses served without the origin |
| *(class name)* | WARN | a class with ≥ 1000 judged responses hit under 10% of the time |
| `log` | OK | the log carries no cache verdict — the ratio is **unknown**, which is not zero |

The target of that first finding is the field that was actually read —
`$upstream_cache_status`, `Cache-Status`, `x-edge-result-type` or `cacheStatus` —
because a hit ratio without the name of the layer that judged it is not
comparable with anything.

A hit is any verdict that answered without asking the origin, which is what the
capacity question is: `HIT`, `STALE`, `UPDATING` and `REVALIDATED`, plus
CloudFront's `RefreshHit` (revalidated, but served from the edge) and
`OriginShieldHit` (answered by the shield, so the origin never saw it).
`LimitExceeded` and `CapacityExceeded` are **not** hits: the CDN refused, and
counting a throttled visitor as a cached one would flatter both numbers.

A class that cold is usually a cache that is never asked rather than one that
keeps missing: a `no-store` from the application, a `Vary` the origin does not
send on that response type, or a CDN behavior with a zero minimum TTL that
leaves the decision to the origin.

## audience

Always OK, and always a statement of a limit rather than a number. Where nearly
every line carries a client identifier, distinct addresses *could* be counted —
but an address is not a person, and behind a CDN or a mobile carrier it is
neither one visitor nor a stable one. Where they do not, the report says the log
cannot count people. Requests are never presented as an audience.
