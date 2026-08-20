# From a log to a load test

```
  edge log ──► edgemix profile ──► profile.json ──► crowdsim load
   measured        transcribed         validated        replayed
```

A load test built from a guess measures a load that does not exist. The mix it
needs — which classes, in what proportion, against which paths, braking on which
one — is already in the edge log. `edgemix profile` transcribes it and refuses to
invent the parts the log cannot supply.

```bash
edgemix profile --base-url https://www.example.test --name example \
  --read-timeout 7s edge.log -o profile.json
crowdsim validate profile.json
crowdsim load --profile profile.json --peak 150
```

## What is measured, and what you have to say

| In the profile | Where it comes from |
|---|---|
| `classes[].weight` | the measured share of requests in that class |
| `classes[].kind` | `rsc` for framework navigation, so the generator sends the right headers |
| `pools` | the busiest paths of each class **that actually rendered** |
| `slo.brake_class` | the class with the highest p95 wait: the one that falls over first |
| `safety.safe_peak_rps` | the busiest second actually measured — a level production has already survived |
| `safety.allow_hosts` | the hostnames the log names, and nothing else |
| `_measured` | source, dialect, window, peak, timing field, and what the log cannot say |
| `slo.guillotine_ms` | **your** `--read-timeout`: given to edgemix, not measured |
| `slo.max_p95_ms`, `max_failed_rate` | your brakes (`--max-p95`, `--max-failed`) |
| `targets.list[].base_url` | `--base-url`, required |

## The four refusals

- **A path that did not render does not enter a pool.** A 404 is cheap to serve
  — often cheaper than the render it stands in for — so a pool of them reports a
  capacity that does not exist. `--min-ok` is the threshold (0.95 by default).
- **A path that looks like a secret is dropped and counted.** A long
  word-separator-free segment with a healthy share of digits is an id or a
  token; `/news/48211` and `/news/champions-league-final` are pages and are
  kept. A profile is a file people paste into tickets.
- **The allowlist is never invented.** If the log does not record the hostname,
  `allow_hosts` is emitted empty with a warning, and crowdsim will refuse to run
  until it is filled. A load test aimed at the wrong hostname is
  indistinguishable from an attack.
- **A class with no replayable path is dropped loudly**, with its share stated,
  rather than folded into another pool — which would measure the wrong request
  type under the right label.

## Warnings you should expect to act on

- *"class X has no path that rendered often enough to replay"* — the mix is
  missing that share. Usually an API class whose paths all answered 4xx/5xx in
  the window.
- *"this log does not record the hostname"* — pass `--allow-hosts`.
- *"the p95 brake was at or above the read timeout"* — it was lowered, because
  inverted it would abort a run only after real visitors were already getting
  504s.
- *"N of M seconds carry no logged request"* — the source log is probably not a
  complete record, so the weights are of the logged subset. The note is written
  into the profile too, not only to the terminal.

## Re-measure after a deploy

Static pools carry build hashes, and a route can move. Two runs are only
comparable at an identical pool, so a profile is a snapshot of a window — which
is why `_measured` states which window, and why it belongs in the same review as
the change it is meant to test.
