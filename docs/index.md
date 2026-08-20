# edgemix

`edgemix` reads a production access log and reports what the traffic was made
of, when it peaked at the second, and where it waited — then writes a
[crowdsim](https://github.com/HiWay-Media/crowdsim) profile from the same
measurement.

- [Usage and flags](usage.md)
- [The dialects it reads](dialects.md)
- [The findings, and the thresholds behind them](findings.md)
- [A worked example, from 40 log lines to a profile](example.md)
- [From a log to a load test](profile.md)
- [The logo, and what it means](logo.md)

```
  visitors
     │
  [ CDN ]  ─── invisible to this log ───
     │
  ┌──▼──────────────────┐
  │  HAProxy (edge)     │  ◄── the log edgemix reads
  └──┬──────────────────┘
     │
  ┌──▼──────────────────┐
  │  the tier behind    │  the wait edgemix measures (Tr)
  └─────────────────────┘
```

Everything above the logging tier is invisible: a CDN hit never reaches the log,
so the numbers are origin-side load, not audience. Every page here keeps that
distinction, because it is the one that turns a report into a wrong conclusion
when it is dropped.
