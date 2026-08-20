# CLAUDE.md — edgemix

`edgemix` (`github.com/Allan-Nava/edgemix`) reads a production access log and
reports what the traffic was made of, when it peaked at the second, and where it
waited — then writes a [crowdsim](https://github.com/HiWay-Media/crowdsim)
profile from the same measurement. One static Go binary, zero dependencies, no
network calls at all: `internal/logfmt` parses the dialects, `internal/classify`
puts a request in a class, `internal/analyze` folds events into a report and its
findings, `internal/profile` transcribes a report into a load-test profile,
`internal/output` renders, `cmd/edgemix` is the CLI.

This file is the operating brief for agents working in the repo.
[AGENTS.md](AGENTS.md) holds the same rules for other tools — when they
disagree, AGENTS.md wins and this file gets fixed.

## Working rules (ALWAYS)

- **Every feature earns its place against one sentence**: *say what the traffic
  was, from the log, without inventing anything the log does not carry*. A check
  that probes a live system belongs in
  [checkfleet](https://github.com/Allan-Nava/checkfleet); a check that generates
  traffic belongs in crowdsim.
- **Zero dependencies, no network, no subprocesses.** `go.mod` has no `require`
  block and CI enforces it, along with a grep that fails the build on `net`,
  `net/http`, `net/url` or `os/exec`. edgemix reads files. That is what makes it
  safe to point at a copy of production logs, and it is a product property, not
  an aesthetic.
- **A missing field is never a zero.** A dialect with no timing produces no
  latency section; a log with no cache header reports *unknown*, not 0%; a `-1`
  HAProxy timer is an incomplete phase. The `Have*` flags and the
  `(value, false)` protocol exist for this — callers must consult the bool.
- **Anchor on shape, never on column position.** Every parser finds the
  bracketed date, the five-timer field and the quoted request by what they look
  like. A positional reader survives the test corpus and then misreads the first
  real log with a syslog prefix, reporting a plausible wrong number rather than
  failing.
- **Announce every cap.** Distinct-path limits, `--since/--until` exclusions,
  unreadable lines and silent seconds are reported. A silently truncated list
  reads as a complete one.
- **Exit 0 whenever the analysis ran.** Findings are output. Only `--exit-on`
  produces a non-zero exit; a usage error or a file that is not a log exits 2.
- **Worst findings first**, in every renderer, and every finding carries
  `Value`/`Unit` so a machine consumer never parses `Message`.
- **Test first, always.** The failing test lands before the implementation. Two
  of the parser's real bugs (a syslog prefix read as a date, a captured dash
  counted as an identity) were found this way within minutes of the tests
  existing.
- **Backlog first**: work exists in `BACKLOG.md` with an `EM-n` id, and
  `ROADMAP.md` is generated — run `scripts/backlog.sh roadmap` after editing the
  backlog or CI fails. Commits and CHANGELOG entries reference the id.
- **Align everything**: a new dialect, check or flag lands in the same commit as
  its README row, its `--help` text, its tests, the backlog tick and the
  CHANGELOG line.
- **The docs are a site**: `docs/` is published to
  [allan-nava.github.io/edgemix](https://allan-nava.github.io/edgemix/) by
  `scripts/site.sh` — POSIX sh and awk, because tooling with a toolchain rots.
  A new page goes into the `$pages` nav list in that script (an unlisted page
  fails the build rather than becoming unreachable), links stay written as
  `.md` and are rewritten to `.html` at render time, and
  `scripts/site.sh check` — a CI gate alongside `scripts/site_test.sh` — fails
  on a dead internal link or a dead anchor. Numbers on a docs page come from
  running the tool over `docs/example.log`, never from memory.
- **Releases**: every release is a tagged `vX.Y.Z` with a new `CHANGELOG.md`
  section (Keep a Changelog). `minor` for new dialects, checks or flags; `patch`
  for fixes. **Never `git push`** — that is the maintainer's call. No
  `Co-Authored-By` trailers.

## Pattern for adding a dialect or a check

1. **Backlog first**: an `EM-n` with a milestone, `prio`, `size` and `labels`.
   Regenerate the roadmap.
2. **Red first**: write the log line (or the synthetic event stream) with the
   property planted, assert against it, and watch it fail for the right reason.
   No captured production log ever enters this repository — lines are written by
   hand in the test, which is also how the trap gets documented.
3. **A parser states what it could not read.** New dialects return `ErrSkip` for
   a line that is not a request record and `ErrMalformed` for one that is but
   could not be read. Conflating the two either hides a coverage hole or invents
   one.
4. **A check emits a real `finding.Finding`**: `Target` names the class, path or
   the log itself; `Message` states the measurement; `Value`/`Unit` carry it;
   `Hint` says what it means operationally.
5. **Two tests minimum**: one that plants the condition and asserts it is found
   *and correctly attributed*, and one that asserts a healthy log stays quiet.
6. `go test -race ./...`, `gofmt`, `go vet`.
7. **Close the loop**: CHANGELOG referencing the `EM-n`, tick the backlog with
   `ver=X.Y.Z`, regenerate the roadmap, tag. No push.

## Known traps / technical rules

- **The first `[...]` on a syslog line is the pid**, not the date
  (`haproxy[2411]`). The HAProxy parser tries every bracketed group until one
  parses as a date; stopping at the first is how a reader skips every line of a
  syslog-shipped log.
- **HAProxy's accept date carries no zone offset.** There is nothing in the line
  to read it from, so `--tz` states what was assumed and the report prints it.
  nginx's `$time_local` does carry an offset and it is authoritative; Traefik's
  `StartUTC` is UTC.
- **`Tr`, `OriginDuration` and `$request_time` are not the same measurement.**
  The first two are the wait on the tier behind; the third includes the client
  reading the body, which a slow phone inflates. `LatencyField` travels with the
  numbers so two reports are never compared across dialects by accident.
- **A captured header slot may hold a dash.** HAProxy writes `-` for a header
  that was not there, and counting it as a client identity makes an unanswerable
  audience question look answerable.
- **Percentiles over seconds include the empty seconds**, and they are counted
  without being stored: the sorted observed counts are indexed with an offset
  for the silent ones. Storing them would make a week-long log allocate 600k
  ints for nothing.
- **A class peak is not the total peak times a share.** Classes do not peak
  together — a navigation burst lands on a different second from the document
  that triggered it — so each class keeps its own per-second series.
- **`sD` in the termination state is the proxy giving up on the backend**; `cD`
  is the client leaving. Only the first is a capacity symptom, and a 5xx rate
  read without them attributes the application's own errors to saturation.
- **Whole silent seconds inside a busy window mean the log is incomplete**
  (HAProxy's `dontlog-normal`, a sampled nginx log), not that traffic stopped.
  Every share is then a share of the logged subset, and the report has to say so
  — including inside an emitted profile.
- **An access log cannot count people.** Requests are not visitors, an address
  is not a person, and behind a CDN or a carrier it is neither one nor stable.
  The audience finding states the limit; it never estimates.
- **A pool of paths that did not render measures the error handler.** A 404 is
  cheap — often cheaper than the render it stands in for — so pools keep only
  paths whose responses were overwhelmingly 2xx, and `looksSecret` drops
  token-shaped segments because a profile is a file people paste into tickets.
- **Go's `flag` package stops at the first operand.** `permute` in
  `cmd/edgemix` reorders flags ahead of the file arguments, because
  `edgemix profile edge.log -o out.json` is the form every example uses and
  "no such file: -o" is a confusing way to learn otherwise.
- **Detection is a vote, not a signature match.** The dialects resemble each
  other; a wrong reader does not fail, it returns nonsense. A tie refuses.
