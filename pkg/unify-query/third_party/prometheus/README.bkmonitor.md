# bkmonitor Prometheus v0.42 compatibility fork

This subtree is pinned to upstream Prometheus `v0.42.0`, commit
`225c61122d88b01d1f0eaaee0e05b6f3e0567ac0`, and keeps its original module
path and licenses. UQ uses it through the local `go.mod` replacement so the
production build does not depend on an edited module cache.

Only packages reachable from UQ plus the upstream `promql` test suite are
retained; unrelated commands, web assets, documentation, discovery providers,
and package-local fixtures are pruned from the vendored snapshot.

The local patch is intentionally limited to `promql/engine.go`:

- backport lazy point-slice allocation and unused-capacity reuse from upstream
  pull requests `#12734` and `#13448` while retaining the v0.42 `Point` API;
- prevent dense range-query slices from entering the generic point pool;
- check cancellation during long per-series range evaluation;
- enforce the existing `MaxSamples` boundary before allocating the next point.

No PromQL result, timestamp, window, histogram, stale-sample, or vector-matching
semantics are intentionally changed. UQ compatibility and allocation tests are
the release gate for this subtree.
