# Shared Topology Fetch, Output and Admission

This extends the shared topology API without changing the default snapshot
response. It is not evidence that a particular production capacity or latency
target has been met. Ordinary query routes retain their existing renderer.

## Execution

```mermaid
flowchart TD
    A[HTTP admission and memory reservation] --> B[Validate request and grid]
    B --> C[Resolve schema and optional candidate plan]
    C --> D[Prepare each logical query]
    D --> E{Same complete direct-VM input as previous relation?}
    E -->|yes| F[Reuse one request-local read-only Matrix]
    E -->|no| G[Fetch with backend response budget]
    F --> H[Validate grid and count logical work]
    G --> H
    H --> I[Apply relation direction to shared graph]
    I --> J[Propagate per-time reachability]
    J --> K{Requested response format}
    K -->|default| L[Materialize sorted independent snapshots]
    K -->|compact-v1| M[Export owned object versions and masks]
    L --> N[Encode item once and check batch budget]
    M --> N
    N --> O[Encode final envelope and write response]
    O --> P[Release admission and reservations]
```

### Matrix Reuse

`cmdb.v1beta3.shared_topology_reuse_matrix` defaults to `true`. A single slot
is owned by the loader, never shared between requests. Preparation runs on
every logical query. Reuse requires matching serialized QueryTs, expression,
resolved VM expansion, user/tenant/scope, query parameters, storage types,
time range, step, lookback, exact/left-open grid and backend byte budget.
Ephemeral metadata hash IDs are excluded. Only direct-VM relation queries
are eligible; aggregated/non-direct routes fall back to fetching.

Empty and partial results can be reused. Errors cannot. Both applications
still validate the grid and contribute to partial status and logical counters.
Reusing the same live Matrix does not allocate another resident copy. The previous slot is dropped before a different fetch.
Apply never mutates the Matrix. There is no retry or cross-request cache.

`matrix-query-count` remains the logical loader count. New trace attributes
`matrix-physical-query-count`, `matrix-reused-query-count` and `matrix-reused`
separate actual loader fetch attempts from reuse. Physical load metrics no
longer charge reused data; `cmdb_timegraph_matrix_reuses_total` counts hits.
A loader fetch attempt is not necessarily one downstream HTTP request for
non-direct routes. Backend scanning cost remains unknown unless reported by
that backend; Matrix point/label counts are not a scan-cost measurement.

### Independent Relation Fetches

Independent relation groups use up to four workers and share the process-wide
four-fetch limit. A worker retains at most one unapplied Matrix and waits for
its application before fetching the next shard. Graph writes remain serial.

Static bidirectional groups whose endpoint projections contain no conflicting
non-primary attributes may deliver through one shared channel. This allows a
ready group's next shard to proceed while an earlier group is still fetching.
Groups with attribute-sensitive projections, target details, or a collection
callback retain the original group order. The existing memory accounting,
cancellation, backend limits, complete time grid and full result contract apply
to both paths. Request-local numeric IDs may be allocated in a different order;
cross-request comparisons must use full resource identities and relation keys.

For eligible groups, a response-size rejection can also return independent
cardinality branches to the same four-worker queue. A single large relation
can therefore use idle workers after smaller relations finish. The queue holds
query plans, not fetched Matrices; it does not create a new worker pool for
each relation or recursive split. Cancellation stops queued and running work
before the graph is cleaned up.

One single-point partition probe can produce up to four branches by splitting
the heaviest observed bucket again in memory. Each split adds complementary
positive/negative filters, preserving the original OR conditions, aggregation
dimensions and complete time grid. Missing, unseen and later labels still
belong to exactly one branch, even when the probe was partial. Probe values
choose a query plan and are never used as an exhaustive identity list. Probe
Matrices are discarded rather than cached in queued plans. Further oversized
branches retain the existing dimension and exact-time fallback.

Time shards remain serial within their worker to reuse an observed successful
width. Backend scanning, discovery and transfer costs remain; neither the
four-worker scheduler nor the controlled-delay benchmark establishes a
production latency target. No input limit, cross-request cache or new runtime
configuration is introduced.

### Planning Before a Large Read

With candidate planning enabled, a broad, projection-safe, static bidirectional
YOLO query with an exact grid of at least 32 points can probe before its first
data read. This applies only to the existing ready-worker path, without root
filters, identity batches, attribute-sensitive ordering, target details or a
collector. Ordinary mode and short grids keep their existing fetch path.

The probe aggregates one retained identity dimension at the first evaluation
point. Its physical-series weights, multiplied by the original grid length,
estimate branch work. Above a soft target of 1,048,576 points, it plans two to
four complementary branches. Reuse the sample up to the existing four-branch
ceiling, stopping each bucket when its own estimate fits the target. Do not
preselect `ceil(total/target)` branches: binary splits can leave a 2:1:1 shape
with an overweight bucket even when the same sample can split it again. Two
sufficient buckets still stop at two. These numbers are planning heuristics: they do
not cap input, change the grid, guarantee a backend response size, or measure
heap usage. The backend's byte and JSON-object guards remain authoritative.

For a preflight estimate above the target, stop splitting a bucket once its own
estimated work fits the target, even if fewer than the planned maximum number
of branches remain. In a skewed sample, one heavy identity may need another
dimension; splitting its light siblings again cannot reduce that heavy branch.
Keep the light siblings together with their complementary filter, including
unknown and late labels. This stopping rule does not apply to the prepared
fallback for a low estimate or to a confirmed response-size rejection: those
still use the full existing fanout, since the estimate may understate real size.

```mermaid
flowchart TD
    A[Eligible broad long-grid query] --> B[Single-point dimension probe]
    B --> C{Estimated work above soft target?}
    C -->|yes and splittable| D[Queue complementary branches]
    D --> E{Known heavy child with at least two dimensions?}
    E -->|yes| J[Joint single-point probe of two dimensions]
    J --> K{Complete usable sample?}
    K -->|yes| L[Sum marginals and plan both levels locally]
    L --> D
    K -->|no| B
    E -->|only one dimension| B
    E -->|light or no dimensions| F[Fetch full original grid]
    C -->|no or unavailable| F
    F --> G{Response-size rejection?}
    G -->|yes| H[Reuse prepared split or existing partition/time fallback]
    G -->|no| I[Validate and apply complete Matrix]
```

A known-heavy child with at least two remaining dimensions can request a joint
single-point count grouped by the next two dimensions. The root probe remains
single-dimensional, so small broad queries retain their previous probe and data
call counts. Sum joint tuple counts to obtain each dimension's marginal weights;
using the maximum tuple count would underestimate a label shared by many tuples.
Split sample rows using only each newly created matcher, since inherited filters
may reference dimensions that the joint sample no longer contains. For each
heavy first-level bucket, reuse its sample to plan the second dimension locally.
Light buckets keep that second dimension for a later size-error fallback.

This produces at most sixteen query plans from one two-dimensional sample,
using the same four workers and global fetch limit. Each plan owns its remaining
dimensions; no Matrix, sample bucket or histogram enters the work queue. A failed,
partial, empty or malformed joint sample is discarded before the original
single-dimension planner runs. This fallback can cost one extra optional probe.
A valid low estimate still prepares the full first-dimension fallback and does
not consume an untried second dimension. Unseen and late labels remain covered
by complementary full-grid data reads, including when no sampled dimension can
split a heavy bucket.

Joint grouping can return more rows than a single-dimension probe, and its
response remains subject to the existing backend byte and object guards. Fewer
calls are a structural benefit, not proof of reduced physical scans or end-to-end
latency. Acceptance must compare probe rows/bytes/time, data reads, size failures,
peak memory and complete output, as well as small queries. Trace attributes
`shard-partition-joint-attempted`, `shard-partition-joint-used` and
`shard-partition-probes-reused` distinguish the attempted plan from local reuse.

A small estimate still fetches the entire original query. Only its prepared
query plans are retained until that read completes, so an underestimated read
can reuse the split without probing the same dimension twice. Probe Matrices
never enter the graph or remain in queued jobs. Empty, partial, failed or stale
probes cannot define the result's identities or quality; unseen and later
identities remain in a complementary branch. Cancellation stops planning and
running children through the existing request context.

A small eligible long-grid query pays for an extra single-point probe. Large
queries can avoid discarded root responses and intermediate size rejections,
but the tradeoff must be checked against real backend latency and cardinality.
No cross-request hints, raised backend limits or extra fetch concurrency are
introduced. `shard-partition-preflight`, `shard-estimated-points` and the existing
partition-count attributes expose planning decisions for the next acceptance
run; estimates are not actual scan or returned-point counts.

### Candidate Planning

`cmdb.v1beta3.shared_topology_plan_candidates` defaults to `true`.
The planner first computes an H-hop type reachability upper bound and retains
relations between those types plus incoming root observations. `target_types`
only filters output, never the candidate frontier.

When the source has every configured primary key and all values are nonempty,
shared topology reads an instance frontier instead of entire business-wide
relation matrices. Incomplete source identities retain the existing type plan
and full candidate loading. Disabling this option retains the full-scan path
for identity-normalized comparison.

The instance path validates all planned physical routes before it can return
an empty result, observes the root at both relation endpoints, and expands
outgoing planned relations for each discovered complete identity over the
entire request grid. Dynamic relations keep their original orientation and
`from_`/`to_` prefixes even when anchored at the target. H-boundary identities
receive one outgoing closure read so induced edges, back edges, and self edges
are retained. H+1 nodes discovered during closure do not extend the frontier.

Identity union across frames is only a fetching upper bound. Existing edge
bitmaps and per-frame BFS determine the final nodes and induced edges; edges
from different frames cannot be combined into a path. Source attribute info
continues to restrict eligible roots per frame without restricting neighbors.
Request identity alone never creates an observed root.

Selectors batch at most 64 complete primary-key tuples. Each tuple is one AND
group, including all endpoint nonempty guards; groups are joined by OR. No
per-field IN product is used. Oversized multi-identity responses split identity
batches first, keeping the same grid. Both the backend size error and a local
HTTP byte-cap rejection may trigger this split. Each physical fetch receives
an independent cap flag, shared by its aggregation children so rejection
cannot be downgraded to partial. A single identity exceeding the normal-mode
cap remains an explicit error; YOLO retains exact-grid time splitting.

All batches and shards share the loader's deadline, logical Matrix-point counters,
live graph/output reservations, admission lease, and partial timestamps. Missing
required routes, cancellation, and exhausted budgets remain errors. An empty
partial frontier returns partial empty frames, never an unrestricted retry.
Skipped unrelated backend queries no longer contribute their partial signal.
Candidate changes can renumber request-local IDs, so comparisons must use
resource type and complete primary-key identity, plus relation identity and
frame quality, rather than numeric IDs.

`TestTopologyFrontierMatchesFullScanWithCompositeKeysAndChanges` checks both
formats against the full-scan path with changing frames, composite cross-key
counterexamples, all directions, cycles, and boundary induced edges. Independent
per-frame BFS tests also cover the public query. Selector and shard tests cover
parsed OR groups, the real cap flag (including aggregation swallowing the
error), partial propagation, cumulative accounting, hard single-identity
rejection, and cancellation. These do not establish live latency or capacity.

## Response Formats

Default or `response_format: "snapshots"` on a query keeps the existing wire
shape, empty arrays, numeric IDs, ordering, messages and budgets. API conversion
transfers already-owned attribute maps; graph cleanup cannot mutate them. The
handler retains each checked item as RawMessage instead of retaining all its
objects and encoding them again. The final envelope still incurs validation,
copying and allocation: this is not streaming or zero-copy output.

`response_format: "compact-v1"` is an explicit per-query opt-in. It returns
`code`, time/grid fields, `response_format`, `compact`, and optional `message`.
It does **not** return `snapshots`. Failed items have `compact: null`; never
treat that as an empty successful graph. Unknown formats are rejected.

The `compact` object contains:

- `version: 1`, `timestamps`: the entire evaluation grid, including empty frames.
- `nodes`: full `{id, resource_type, dimensions, mask}` attribute versions.
- `edges`: full `{source, target, relation_type, metric_name, category, direction, mask}` versions.
- `partial`: ordered `{index, reason}` entries, including an empty reason when the frame is partial.
- `node_occurrences`, `edge_occurrences`: counts after expanding all frames,
  not dictionary sizes.

IDs are decimal strings, including values above JavaScript's exact Number
range. Masks are arrays of 16-character lowercase hexadecimal uint64 words.
Word 0 contains frames 0..63, with frame 0 in its least significant bit.
Trailing zero words can be omitted. For example, frame 64 alone is represented
by `["0000000000000000", "0000000000000001"]`. YOLO supports larger grids;
there is no new single-word or 60-point restriction in this protocol.

Node masks intersect reachability and the existing disjoint attribute-version
masks. Uncovered bits use the same stable-identity fallback as snapshots. Edge
masks intersect edge activity and both endpoint reachabilities. Target-type
filtering happens after propagation. Dictionaries own their maps and mask
strings before graph cleanup. Nodes are emitted in numeric ID order; versions
of one node have disjoint masks. Edges preserve the snapshot comparator order.
Filtering either dictionary for one frame therefore preserves snapshot order.

[The dependency-free JavaScript reader](examples/topology-compact.mjs) retains
IDs as strings and materializes only the requested frame. It validates masks,
overlapping node versions and dangling edges. Each returned frame owns its
dimension objects. The Node tests also compare server-produced fixtures.

```javascript
import { createTopologyReader } from "./topology-compact.mjs";
const reader = createTopologyReader(successfulItem.compact);
const frame = reader.frame(64);
```

Normal mode reserves the complete graph and traversal workspace before target
filtering, then reserves the DTO containers actually emitted. HTTP checks actual
encoded batch bytes. Repeated observations and conservative JSON upper bounds
are not independent rejection limits. Version-heavy inputs may compress poorly.

## Resource Protection

The following settings are under `cmdb.v1beta3`:

| Setting | Default | Meaning |
| --- | ---: | --- |
| `max_shared_topology_concurrent_requests` | 8 | Execution slots; FIFO queue holds at most the same number of waiting requests |
| `max_shared_topology_reserved_bytes` | 4294967296 | Process-wide estimated in-flight reservation cap |
| `shared_topology_request_memory_bytes` | Deprecated | Read for configuration compatibility; empty leases no longer reserve a fixed floor |
| `shared_topology_memory_headroom_bytes` | 536870912 | Additional margin below observed available memory |
| `yolo_mode` | false | Sustained bypass of TimeGraph quantity/data/admission budgets, configured hop ceilings and internal query deadlines; requested scope, validation and cancellation remain |

Nonpositive values use the finite defaults, not unlimited mode. These starting
values are conservative configuration defaults, **not a recommended capacity
for every Pod**. Calibrate them for the actual CPU/memory limits, ordinary-query
baseline, labels, versions, GC behavior and downstream service capacity.

Reservations follow simultaneously live stages. Request reading reserves 6x the
body cap; backend receiving/decoding reserves 8x the response cap, then switches
to Matrix slice capacity and retained labels. Shared graph accounting follows
unique nodes, full relation identity, indexes and attribute versions, including
old/new storage during replacement; it updates in 256 KiB blocks. Traversal
workspaces are reserved separately. DTO and encoder growth use 4 MiB blocks.
Prior batch items' encoded buffers remain charged while the next graph executes.
These are calibration estimates, not exact allocation or RSS guarantees.

`materialized` confirms only lower bounds of initialized, request-owned storage
that remains live. Borrowed strings, map capacity allowances and unused capacity
stay pending. `pending = reserved - materialized`; headroom checks subtract
pending reservations from current available memory after the configured margin.
Zeroing a stage also clears its credit, including after cancellation. A failed
growth does not wait while holding a graph.

Normal requests wait for execution slots in a bounded FIFO queue, sharing the
original request deadline. A queued HTTP request drains only its bounded body,
allowing HTTP/1 peer closure detection, and counts that body in the total ledger.
The queue is checked before reading additional bodies. Queue-full, cancellation,
deadline and body failures release queued state without starting model work.

Admission and reservation growth additionally inspect host MemAvailable and
all visible cgroup v1/v2 memory limit/usage pairs, including visible ancestors.
Unlimited sentinels and read failures do not become infinite reservations: the
configured cap still applies. Namespace-hidden parents and shared external
activity cannot be certified by these reads. Memory paths are discovered once
per process; restart after moving a process to another cgroup. Only confirmed owned storage already reflected in current usage receives
materialized credit; unknown ownership remains conservative. A check is not an
atomic promise about future RSS.

The lease spans actual response writing, including proxy envelopes and write
failures. Nested model calls share it; release is idempotent. Proxy cleanup clears retained response data before releasing the lease. Active
requests, waiters, reserved bytes and materialized bytes have separate gauges.
Rejections expose stable `max_topology_admission_waiters`, `max_topology_reserved_bytes` or
`max_topology_memory_headroom` reasons and count/limit attributes. Response
encoding and network writing have separate `http-response-encode` and
`http-response-write` spans; neither is a CPU-time measurement.

## Verification and Rollout

Tests cover independent/combined fetch reuse and planning, logical counters without duplicate resident
charging on reuse, immutable Matrix application, owned results after cleanup,
randomized historical attributes/directions/partial at 1..257 points, exact
uint64 client IDs, induced edges, target post-filtering, cancellation, resource
rejection/recovery and direct/proxied writer failure cleanup. Go tests execute
the client fixture tests when Node is available; otherwise run them explicitly:

```sh
node --test docs/examples/topology-compact.test.mjs
GOTOOLCHAIN=go1.24.4 go test -race -tags=jsonsonic ./cmdb/v1beta3 ./service/http/api ./service/http/proxy ./metric
GOTOOLCHAIN=go1.24.4 go test -tags=jsonsonic ./cmdb/v1beta3 -run '^$' -bench BenchmarkTopologyOutputFormats -benchmem
```

The opt-in local pipeline probe supports `TG_PIPELINE_MODE=shared|compact`,
`TG_EDGES`, `TG_POINTS`, `TG_CONCURRENCY` and `TG_PIPELINE_YOLO=true`. It records
fixture/response hashes, stages, allocations, live/retained heap and GC. Use
independent processes and external RSS/cgroup sampling. There is no fixed 4-way
test ceiling, but increased load still needs measured headroom and a stop rule.
Synthetic tests are not real 10x business load or production tail-latency proof.

Validate a newly built image in an authorized test window before rollout.
Keep transport/HTTP2 failures separate from these optimization claims. Bounded
parallel fetch and frontier pushdown remain conditional follow-up experiments
only when post-reuse backend measurements justify them; this change does not
increase physical query concurrency or modify ordinary-query routing.

## Attribute Selection and Representation Budgets

`source_expand_info` is consumed by both topology endpoints. It selects roots
from the resource-info query at each evaluation frame, intersected with observed
relation endpoints. It does not filter neighboring entities or invent info-only
roots. Conflicting identity filters fail; primary keys in the expanded matcher
complete the effective source identity.

Compact output charges each stored node/edge version, including ID and bitmap
overhead. Snapshot output charges each expanded frame object. Expanded occurrence
counts remain observable; Matrix input, H/K, graph memory reservation and global
admission remain independent guards. This change does not certify a workload or
a production resource limit. The HTTP wrapper reuses owned item buffers through
the existing segment writer, retaining batch byte limits and failure cleanup.

The legacy instant/range relation endpoints keep their original response fields.
Partial and truncation quality is recorded in the bounded
`unify_query_cmdb_relation_quality_total{query_mode,quality,reason}` counter and
item traces, including empty partial results. Repeated reasons within one item
count once; different reasons count separately. Detailed metric names stay in
traces. Each batch item has an independent metadata status scope. Capacity
limits and other execution failures retain the existing item code/message error
contract; they are not converted to successful empty results.

Required relation missing-route errors remain strict for new topology/path
queries. In the legacy adapter, a typed missing physical route makes that
candidate unavailable: later candidates are still queried, and available or
empty results retain internal `relation_no_physical_route:<metric>` quality.
Cancellation and deadlines stop immediately. See [the API contract](api/timegraph.md)
for telemetry and the VM-compatible range grid. New path/topology response
quality fields are unchanged.

Legacy `path_resource` is a contiguous resource sequence, including any supplied
source or target endpoint. `[""]` selects a direct relation; other empty entries
are invalid. Candidate paths are ordered by length and resource sequence, and
the first nonempty candidate wins. When all successful candidates are empty,
the response retains the last successfully evaluated candidate path. Normal
mode reports `path_search_depth_limit` if the configured search depth truncates
coverage; YOLO enumerates finite simple schema paths even with a path constraint.

Each legacy candidate is loaded in hop order. The first relation keeps the
source matcher; subsequent relations use batches of at most 64 complete
endpoint identities joined by OR. Returned points must also match the preceding
frontier at the same evaluation timestamp. An empty frontier stops further
reads without clearing any partial quality already observed. This avoids
unfiltered intermediate relation reads and preserves compound identities,
direction and temporal reachability. The topology loader remains separate.

## Sustained YOLO operation (2026-10-13)

With `cmdb.v1beta3.yolo_mode=true`, TimeGraph does not impose its configured hop
ceiling, 60-second query deadline, sample/output/memory budgets or admission gate.
The request-scoped flag also bypasses the local PromQL engine timeout, sample
budget and admission tracker, and the VM client deadline; instant VM requests
omit the client-supplied `timeout` field. Registered TimeGraph routes also clear
HTTP body-read and response-write deadlines after routing, so the server write
window cannot discard a completed long query. Parent cancellation still applies. Shared engines and VM instances are not
mutated, so ordinary metric queries retain their configured limits.

Caller-specified hops, paths, time grids and positive `Limit` retain their query
meaning. YOLO does not inject the default root `Limit`; the current legacy
adapter already disables that default and the execution layer does not consume
it. Automatic schema path searches enumerate finite simple resource-type paths
and respond to parent cancellation. Input/schema validity and parent deadlines
still apply. Downstream service limits and Pod resources are separate settings.

For the current rollout, sustained YOLO is the accepted operating policy. Normal
budget-mode capacity acceptance and additional stress scenarios are not release
prerequisites. This policy does not turn unexecuted scenarios into passed tests.

The scoped HTTP middleware covers the two topology, two v1beta3 multi-resource
and two TimeGraph path-resource POST routes, including configured route aliases
and proxy dispatch through the registered handler chain. Ordinary metric/legacy
VM routes retain server deadlines. Header acquisition and the outer proxy
envelope happen before the TimeGraph route is identified and retain their
transport settings; the middleware does not reset caller deadlines.
