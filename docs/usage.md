# Usage

```
edgemix analyze <log...>    read the log: the mix, the peak second, the waiting
edgemix profile <log...>    write a crowdsim profile from the same measurement
edgemix version
```

A log is a file, a gzipped file, or `-` for standard input. Several files are
read as **one window**, in the order given — which is how a rotated log and its
successor are analysed together.

## Reading flags (both commands)

| Flag | Default | What it changes |
|---|---|---|
| `--dialect` | `auto` | `haproxy`, `nginx`, `traefik`, `cloudfront`, `akamai`. `auto` runs every parser over a sample and keeps the one that read the most lines; a tie refuses rather than guessing. |
| `--tz` | `UTC` | The zone of a log whose dates carry no offset. HAProxy writes local time with nothing to read the zone from, so this is an assumption — and the report prints which one it made. It does not apply to nginx (its `$time_local` carries an offset), Traefik (`StartUTC`), CloudFront (UTC by definition) or Akamai (an epoch). |
| `--xff-capture` | `0` | The 1-based slot of `X-Forwarded-For` among HAProxy's captured request headers (`{a|b|c}`). Only its presence is recorded; the value is never kept. |
| `--classes` | built-in | A JSON file replacing the class rules. See [the class set](#classes). |
| `--since`, `--until` | — | Narrow the window. `2026-08-19 18:00:00` is read in `--tz`; an RFC3339 instant is also accepted. Excluded requests are counted and reported on stderr. |
| `--tails` | `1s,3s,7s` | Wait thresholds to report the share of traffic beyond. `--read-timeout` is always added. |
| `--read-timeout` | `7s` | Your reverse proxy's read timeout. Traffic slower than this became a 504 for a real visitor, which is why it is the threshold the only BAD finding is made of. |
| `--top` | `10` | Paths listed per class. |
| `--max-paths` | `200000` | Cap on distinct paths kept per class. Whatever is dropped is counted and reported — an unbounded cardinality (a scanner, ids in the path) would otherwise be the tool's own out-of-memory. |

## `analyze`

| Flag | Default | What it changes |
|---|---|---|
| `--format` | `text` | `text`, `md`, `json`. |
| `--out`, `-o` | stdout | Write to a file. |
| `--exit-on` | — | Exit 1 when a finding reaches `warn`, `bad` or `error`. Without it the command exits 0: a check that ran is a success. |

```bash
edgemix analyze /var/log/haproxy-edge.log
edgemix analyze E1ABCDEF.2026-08-19-18.*.gz        # a day of CloudFront, headers and all
edgemix analyze --format md edge.log > docs/incidents/2026-08-19-peak.md
edgemix analyze --format json edge.log | jq '.rate, .classes[].name'
ssh lb1 'tail -n 200000 /var/log/haproxy.log' | edgemix analyze --dialect haproxy -
edgemix analyze --exit-on bad edge.log.1.gz
```

## `profile`

| Flag | Default | What it changes |
|---|---|---|
| `--base-url` | **required** | Where a run would send the load. Not guessed from the log: defaulting it to the measured hostname would aim a load test at production by accident. |
| `--name`, `--description` | from the log | Profile identity. |
| `--target` | `edge` | Name the base URL becomes in `targets.list`. |
| `--host-header`, `--bypass` | — | Address a tier by IP while keeping `Host` correct, or skip a CDN keeping SNI and `Host` correct. |
| `--pool-size` | `40` | Paths per class in the pool. |
| `--min-ok` | `0.95` | Minimum share of 2xx for a path to enter a pool. |
| `--max-p95` | `5s` | The brake. Lowered automatically if it is not below the read timeout. |
| `--max-failed` | `0.05` | Failed-rate brake. |
| `--safe-peak` | measured peak | The ceiling above which a run needs an explicit override. |
| `--allow-hosts` | measured hosts | Hostname globs a run may target. |
| `--out`, `-o` | stdout | Where to write the profile. |

```bash
edgemix profile --base-url https://www.example.test --name example edge.log -o profile.json
crowdsim validate profile.json
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | the analysis ran (findings are output, not failure) |
| 1 | `--exit-on` was set and a finding reached that level |
| 2 | edgemix could not run: a usage error, an unreadable file, or a file no dialect could read |

## Classes

First match wins, so the order of the rules is data rather than a detail.

```json
{
  "fallback": "page",
  "rules": [
    { "name": "nav",    "kind": "rsc", "query_params": ["_rsc"] },
    { "name": "asset",  "path_suffixes": [".js", ".css", ".webp"] },
    { "name": "search", "path_contains": ["/search"] },
    { "name": "api",    "path_prefixes": ["/api/"], "methods": ["GET", "POST"] }
  ]
}
```

`kind: "rsc"` is carried into an emitted profile, where it makes the generator
send framework-navigation headers instead of a plain request. A rule with no
condition, a duplicate class name or a missing fallback is refused: each of
those would silently redistribute the shares.
