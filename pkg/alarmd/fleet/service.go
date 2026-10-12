// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// GapRegistryUnavailable means the set of replicas that should have published
// could not be read, so there is no way to tell a quiet deployment from an
// unreadable one.
const GapRegistryUnavailable GapKind = "REGISTRY_UNAVAILABLE"

// GapSnapshotsUnreadable means this process could not read the published
// snapshots at all this round. It is not the same as every replica missing:
// the replicas may all have published and be read fine by the next call,
// and a view that listed each of them as missing would have said so of four
// running replicas -- which it did, once, on a verification cluster, with no
// field left to say the read itself had failed.
const GapSnapshotsUnreadable GapKind = "SNAPSHOTS_UNREADABLE"

// GapSnapshotsDeferred means this process did not read the snapshots this
// time: the observation memory line had no room for what they decode to
// (ErrSnapshotsDeferred). Like an unreadable read it says nothing of any
// replica, and asked again it may read them. Read whole or not read: a view
// of half the replicas would count as if it were all of them.
const GapSnapshotsDeferred GapKind = "SNAPSHOTS_DEFERRED"

// ExpectationSource reads the authoritative object set from the control plane.
//
// It is deliberately not derived from the calling replica's own state: on a
// multi-replica deployment only the control leader observes the whole active
// set and the others observe an empty one, so a local reading would have most
// replicas reporting full coverage of nothing.
type ExpectationSource interface {
	Expectation(ctx context.Context) (Expectation, error)
}

// ReplicaRegistry lists the replicas expected to publish a snapshot.
type ReplicaRegistry interface {
	ReadyReplicas(ctx context.Context, at time.Time) ([]string, error)
}

// SnapshotReader reads published snapshots for the given replicas.
type SnapshotReader interface {
	Load(ctx context.Context, replicas []string) ([]Snapshot, error)
}

// unadmittedReader is a SnapshotReader that can also read without asking
// the observation memory line (RedisStore.loadUnadmitted): the reads a
// verdict is decided from.
type unadmittedReader interface {
	loadUnadmitted(ctx context.Context, replicas []string) ([]Snapshot, error)
}

// summarizingReader is a SnapshotReader that can read snapshots the way the
// verdict reads them and keep only each one's summary as it is decoded
// (RedisStore.summarizeUnadmitted).
type summarizingReader interface {
	summarizeUnadmitted(ctx context.Context, replicas []string, stallAfter time.Duration) ([]ReplicaSummary, error)
}

// SummaryReader reads what replicas publish beside their snapshots: their
// summaries, and their owned lists, which are read only when the digests
// disagree. A replica with nothing readable is absent from the result, as
// from Load.
type SummaryReader interface {
	LoadSummaries(ctx context.Context, replicas []string) ([]ReplicaSummary, error)
	LoadOwned(ctx context.Context, replicas []string) (map[string][]string, error)
}

// Service answers deployment-wide questions from any replica.
type Service struct {
	// localRow builds this replica's live row for an object it tracks; set
	// once at wiring (SetLocalRows), nil where there is no local tracker.
	localRow func(queryGroup string) (Anomaly, bool)
	// localQueryRanges is this replica's account of an object's latest asked
	// ranges, set the same way (SetLocalQueryRanges).
	localQueryRanges func(queryGroup string) (QueryRanges, bool)
	expectations     ExpectationSource
	registry         ReplicaRegistry
	snapshots        SnapshotReader
	freshness        time.Duration
	now              func() time.Time

	// The denominator and the replica list are read from the control plane on
	// the same Redis the pipeline depends on, and reading the active object set
	// decodes the whole set. This surface is meant to be embedded in a page, so
	// its cost would otherwise scale with how many people are looking at it.
	// The mutex is held across the refresh on purpose: concurrent viewers then
	// collapse into one read instead of racing to issue their own.
	// verdicts is the record of the deployment verdict's changes; see
	// RecordVerdict.
	verdicts verdictHistory

	sourceMu    sync.Mutex
	sourcesAt   time.Time
	sourcesFor  time.Duration
	replicas    []string
	replicasErr error
	expectation Expectation
	expectErr   error
	// expected is the digest of the expectation's objects, taken when the
	// expectation is read rather than on every view.
	expected SetDigest

	// summaries is the summarized read in progress, which a caller that
	// comes while it runs waits for and shares instead of reading again;
	// loads is the same for the snapshots a page's view is built from, by
	// the replicas read.
	summaries sharedReads[summarized]
	loads     sharedReads[loaded]
}

type summarized struct {
	view View
	part ReplicaPart
}

type loaded struct {
	snapshots []Snapshot
	err       error
}

// sharedReads are reads that the callers asking for the same key while one
// runs share: the first caller starts it, and every caller waits for it or
// for its own context, whichever ends first.
//
// The read keeps the first caller's values but not its cancellation: the
// first caller going away must not fail the others. It runs while anyone
// waits for it and is stopped when the last of them has gone, and a caller
// that comes after that starts a read of its own.
//
// What the read holds of the memory line it holds for every caller that
// uses its answer (pageHolds): on a collector of its own, released once the
// read has returned and each caller that took the answer has released its
// own collector -- not with the first caller's, which may be done while the
// others still build their views -- and at once for a caller that stopped
// waiting.
type sharedReads[T any] struct {
	mu      sync.Mutex
	flights map[string]*sharedFlight[T]
}

type sharedFlight[T any] struct {
	done    chan struct{}
	stop    context.CancelFunc
	waiters int
	answer  T
	// holds is what the read held, and users the read itself and every
	// caller that has not yet let go of its answer.
	holds *pageHolds
	users int
}

// do is read's answer for key, shared with the callers that ask for key
// while it runs; gone is the answer to a caller whose context ended first.
func (reads *sharedReads[T]) do(ctx context.Context, key string, read func(context.Context) T, gone func(error) T) T {
	reads.mu.Lock()
	if reads.flights == nil {
		reads.flights = map[string]*sharedFlight[T]{}
	}
	flight := reads.flights[key]
	if flight == nil {
		readCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
		flight = &sharedFlight[T]{done: make(chan struct{}), stop: stop, holds: &pageHolds{}, users: 1}
		readCtx = flight.holds.in(readCtx)
		reads.flights[key] = flight
		go func() {
			answer := read(readCtx)
			reads.mu.Lock()
			flight.answer = answer
			if reads.flights[key] == flight {
				delete(reads.flights, key)
			}
			reads.mu.Unlock()
			stop()
			close(flight.done)
			reads.letGo(flight)
		}()
	}
	flight.waiters++
	flight.users++
	reads.mu.Unlock()
	select {
	case <-flight.done:
		if holds := pageHoldsOf(ctx); holds != nil {
			holds.add(func() { reads.letGo(flight) }, flight.holds.storedBytes())
		} else {
			reads.letGo(flight)
		}
		return flight.answer
	case <-ctx.Done():
		reads.mu.Lock()
		flight.waiters--
		if flight.waiters == 0 {
			if reads.flights[key] == flight {
				delete(reads.flights, key)
			}
			flight.stop()
		}
		reads.mu.Unlock()
		reads.letGo(flight)
		return gone(ctx.Err())
	}
}

// letGo is one user of the flight's answer done with it; the last releases
// what the read held.
func (reads *sharedReads[T]) letGo(flight *sharedFlight[T]) {
	reads.mu.Lock()
	flight.users--
	last := flight.users == 0
	reads.mu.Unlock()
	if last {
		flight.holds.release()
	}
}

// NewService wires the three sources. freshness is how old a snapshot may be
// before it stops counting as coverage, and must be shorter than the store's
// retention: equal budgets make the stale branch unreachable, because a
// snapshot would expire at the same moment it stopped being fresh.
func NewService(
	expectations ExpectationSource,
	registry ReplicaRegistry,
	snapshots SnapshotReader,
	freshness time.Duration,
	now func() time.Time,
) (*Service, error) {
	if expectations == nil || registry == nil || snapshots == nil {
		return nil, errors.New("alarmd fleet: service requires expectations, a registry and snapshots")
	}
	if freshness <= 0 {
		freshness = DefaultTTL / 4
	}
	// Clamped, not rejected. A freshness budget that outgrows retention is a
	// diagnostics setting, and refusing to build the service would let it stop
	// the process whose facts it exists to describe -- which is the one thing
	// this whole path is not allowed to do.
	if retention, ok := snapshots.(interface{ TTL() time.Duration }); ok {
		if budget := retention.TTL(); budget > 0 && freshness >= budget {
			freshness = budget / 2
		}
	}
	if now == nil {
		now = time.Now
	}
	// Half the freshness budget: long enough that a page refreshing every second
	// costs one control-plane read per publish cycle rather than one per view,
	// short enough that a replica cannot go stale without the next view seeing
	// it, since staleness is judged against the same budget.
	return &Service{
		expectations: expectations, registry: registry, snapshots: snapshots,
		freshness: freshness, sourcesFor: freshness / 2, now: now,
	}, nil
}

// sources returns the denominator and the replica list, reading them at most
// once per cache window. Failures are cached too: a control plane that is down
// stays down for the window, and retrying it per request would add load to a
// dependency that is already failing.
func (service *Service) sources(ctx context.Context, at time.Time) ([]string, error, Expectation, error) {
	replicas, replicasErr, expectation, expectErr, _ := service.sourcesWithDigest(ctx, at)
	return replicas, replicasErr, expectation, expectErr
}

// sourcesWithDigest is sources with the digest of the expectation's objects.
func (service *Service) sourcesWithDigest(ctx context.Context, at time.Time) ([]string, error, Expectation, error, SetDigest) {
	service.sourceMu.Lock()
	defer service.sourceMu.Unlock()
	if !service.sourcesAt.IsZero() && at.Sub(service.sourcesAt) < service.sourcesFor {
		return service.replicas, service.replicasErr, service.expectation, service.expectErr, service.expected
	}
	service.replicas, service.replicasErr = service.registry.ReadyReplicas(ctx, at)
	service.expectation, service.expectErr = service.expectations.Expectation(ctx)
	if service.expectErr != nil {
		service.expectation = Expectation{}
	}
	service.expected = DigestOf(service.expectation.IDs)
	service.sourcesAt = at
	return service.replicas, service.replicasErr, service.expectation, service.expectErr, service.expected
}

// View assembles the current answer.
//
// Every dependency failure becomes a gap rather than an error: a caller that
// gets an error learns nothing, while a caller that gets a view marked UNKNOWN
// learns exactly which part is missing. The one thing this must never do is
// return a healthy-looking view built from whatever happened to be readable.
func (service *Service) View(ctx context.Context) View {
	at := service.now()

	replicas, replicasErr, expectation, expectationErr := service.sources(ctx, at)
	if replicasErr != nil {
		return registryUnavailable(expectation, replicasErr)
	}

	snapshots, snapshotsErr := service.loadShared(ctx, replicas)
	if snapshotsErr != nil {
		// Reading snapshots failed as a whole, so nothing can be said of any
		// replica. Coverage is therefore zero, which is what the aggregation
		// is given; the per-replica "missing" gaps it derives from that are
		// replaced below, because they would state something the read did
		// not establish.
		snapshots = nil
	}

	view := Aggregate(expectation, snapshots, replicas, at, service.freshness)
	readFailed(&view, snapshotsErr, expectationErr)
	return view
}

// loadShared is the snapshots' Load, shared by the callers that ask for the
// same replicas, in any order, while a read of them is in progress: pages
// that come together cost one read, and one grant from the memory line. The
// snapshots are only read from; each caller builds its own view of them.
func (service *Service) loadShared(ctx context.Context, replicas []string) ([]Snapshot, error) {
	sorted := append([]string(nil), replicas...)
	sort.Strings(sorted)
	answer := service.loads.do(ctx, strings.Join(sorted, "\x00"), func(ctx context.Context) loaded {
		snapshots, err := service.snapshots.Load(ctx, replicas)
		return loaded{snapshots: snapshots, err: err}
	}, func(err error) loaded { return loaded{err: err} })
	return answer.snapshots, answer.err
}

// registryUnavailable is the view when the replicas that should have
// published could not be read.
func registryUnavailable(expectation Expectation, err error) View {
	return View{
		expectation: expectation,
		Health:      HealthUnknown,
		Gaps:        []Gap{{Kind: GapRegistryUnavailable, Detail: gapDetail(err)}},
		Anomalies:   []Anomaly{},
		Replicas:    []string{},
	}
}

// readFailed says on the view what its reads could not: the published
// replicas, when none could be read, and why the denominator is missing.
func readFailed(view *View, snapshotsErr, expectationErr error) {
	if snapshotsErr != nil {
		kept := make([]Gap, 0, len(view.Gaps)+1)
		for _, gap := range view.Gaps {
			if gap.Kind != GapReplicaMissing {
				kept = append(kept, gap)
			}
		}
		failed := Gap{Kind: GapSnapshotsUnreadable, Detail: gapDetail(snapshotsErr)}
		if errors.Is(snapshotsErr, ErrSnapshotsDeferred) {
			failed = Gap{Kind: GapSnapshotsDeferred}
		}
		// Nor is nothing read a shortfall of ownership: the replicas own
		// what they own, and none was asked.
		shortfall := kept[:0]
		for _, gap := range kept {
			if gap.Kind != GapOwnershipShortfall {
				shortfall = append(shortfall, gap)
			}
		}
		view.Gaps = append(shortfall, failed)
		// Said here and not left to the aggregation: a view that read no
		// snapshot cannot tell, whatever the aggregation made of an empty
		// set. Today it produced a gap per expected replica and so was
		// already UNKNOWN; this does not depend on that staying true.
		view.Health = HealthUnknown
	}
	if expectationErr != nil {
		for index := range view.Gaps {
			if view.Gaps[index].Kind == GapDenominatorUnavailable {
				view.Gaps[index].Detail = gapDetail(expectationErr)
			}
		}
	}
}

// Summarized is View read from the replicas' summaries instead of their
// snapshots, with the part their rows add up to: what the health route and
// the verdict scrape are read from. A replica that published no summary,
// or one without the scrape's counts -- an older build during a rollout --
// is summarized here from its snapshot, at the snapshot's own TakenAt, so
// it reads as it would have published. Neither read asks the observation
// memory line: a verdict unknown for want of observation memory is an
// alert that follows the load, not alarmd.
//
// A caller that comes while a read is in progress waits for it and shares
// its answer: a page's viewers then cost one read between them, with no
// period of its own to keep answers for.
func (service *Service) Summarized(ctx context.Context, stallAfter time.Duration) (View, ReplicaPart) {
	answer := service.summaries.do(ctx, "", func(ctx context.Context) summarized {
		view, part := service.summarize(ctx, stallAfter)
		return summarized{view: view, part: part}
	}, func(err error) summarized { return summarized{view: unreadView(err)} })
	return answer.view, answer.part
}

// unreadView is the view to a caller that stopped waiting for the read.
func unreadView(err error) View {
	return View{Health: HealthUnknown, Gaps: []Gap{{Kind: GapSnapshotsUnreadable, Detail: gapDetail(err)}},
		Anomalies: []Anomaly{}, Replicas: []string{}}
}

func (service *Service) summarize(ctx context.Context, stallAfter time.Duration) (View, ReplicaPart) {
	at := service.now()
	replicas, replicasErr, expectation, expectationErr, expected := service.sourcesWithDigest(ctx, at)
	if replicasErr != nil {
		return registryUnavailable(expectation, replicasErr), ReplicaPart{}
	}
	reader, publishing := service.snapshots.(SummaryReader)
	var summaries []ReplicaSummary
	var readErr error
	published := make(map[string]bool, len(replicas))
	if publishing {
		summaries, readErr = reader.LoadSummaries(ctx, replicas)
		// A summary without the scrape's counts is a build's from before
		// them, and says nothing of those counts: read as none, so the
		// replica is summarized from its snapshot rather than counted as
		// having no rows.
		kept := summaries[:0]
		for _, summary := range summaries {
			if summary.Part.Metrics == nil {
				continue
			}
			published[summary.Head.Replica] = true
			kept = append(kept, summary)
		}
		summaries = kept
	}
	missing := make([]string, 0, len(replicas)-len(published))
	for _, replica := range replicas {
		if !published[replica] {
			missing = append(missing, replica)
		}
	}
	// A replica that published no summary is read from its snapshot
	// without asking the memory line: the verdict is decided from this
	// read, and recorded. Its snapshot unread, that replica alone is
	// unread; the others' summaries stand.
	var fallbackErr error
	if readErr == nil && len(missing) > 0 {
		var fallback []ReplicaSummary
		fallback, fallbackErr = service.summarizeFromSnapshots(ctx, missing, stallAfter)
		summaries = append(summaries, fallback...)
	}
	if readErr != nil {
		summaries = nil
	}
	byReplica := make(map[string]*ReplicaSummary, len(summaries))
	for index := range summaries {
		byReplica[summaries[index].Head.Replica] = &summaries[index]
	}
	ownedSets := func(counted []string) ([][]string, bool) {
		sets, whole, read := make([][]string, 0, len(counted)), true, make([]string, 0, len(counted))
		for _, replica := range counted {
			summary := byReplica[replica]
			if summary.fromSnapshot {
				sets = append(sets, summary.owned)
				whole = whole && len(summary.owned) >= summary.Head.Owned
				continue
			}
			read = append(read, replica)
		}
		if len(read) == 0 {
			return sets, whole
		}
		lists, err := reader.LoadOwned(ctx, read)
		if err != nil {
			return sets, false
		}
		for _, replica := range read {
			list, found := lists[replica]
			whole = whole && found
			sets = append(sets, list)
		}
		return sets, whole
	}
	view, part := AggregateSummaries(expectation, expected, summaries, replicas, at, service.freshness, ownedSets)
	if fallbackErr != nil {
		unread := make(map[string]bool, len(missing))
		for _, replica := range missing {
			unread[replica] = true
		}
		for index, gap := range view.Gaps {
			if gap.Kind == GapReplicaMissing && unread[gap.Replica] {
				view.Gaps[index] = Gap{Kind: GapSnapshotsUnreadable, Replica: gap.Replica, Detail: gapDetail(fallbackErr)}
			}
		}
	}
	readFailed(&view, readErr, expectationErr)
	return view, part
}

// admitKeptView asks the memory line for a view kept past the page that
// read it, where the snapshots' reader can ask; otherwise it is kept.
func (service *Service) admitKeptView(bytes uint64) bool {
	if service == nil {
		return true
	}
	if admitter, ok := service.snapshots.(interface{ admitKept(uint64) bool }); ok {
		return admitter.admitKept(bytes)
	}
	return true
}

// summarizeFromSnapshots summarizes the named replicas from their
// snapshots, read as loadForVerdict reads them; a reader that can keeps
// only each summary as it decodes the snapshot.
func (service *Service) summarizeFromSnapshots(ctx context.Context, replicas []string, stallAfter time.Duration) ([]ReplicaSummary, error) {
	if summarizing, ok := service.snapshots.(summarizingReader); ok {
		return summarizing.summarizeUnadmitted(ctx, replicas, stallAfter)
	}
	snapshots, err := service.loadForVerdict(ctx, replicas)
	summaries := make([]ReplicaSummary, 0, len(snapshots))
	for _, snapshot := range snapshots {
		summaries = append(summaries, summaryFromSnapshot(snapshot, stallAfter))
	}
	return summaries, err
}

// loadForVerdict reads snapshots without asking the observation memory
// line, where the reader can: a verdict decided from them must not turn
// unknown because observation is short of memory.
func (service *Service) loadForVerdict(ctx context.Context, replicas []string) ([]Snapshot, error) {
	if unadmitted, ok := service.snapshots.(unadmittedReader); ok {
		return unadmitted.loadUnadmitted(ctx, replicas)
	}
	return service.snapshots.Load(ctx, replicas)
}

// gapDetail classifies a dependency failure instead of quoting it.
//
// This body is served on the listener a host platform may route to, so anything
// it carries is readable by whoever can reach that port. A raw error from a
// Redis client names the endpoint it failed to reach, which says more about the
// deployment's internals than a caller asking whether alarmd is healthy needs to
// know. The full text stays where it was already going: this process's logs and
// the failure metrics of the store that produced it.
func gapDetail(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timed out"
	default:
		return "unavailable"
	}
}

// SetLocalRows gives the service this replica's tracker, for the rows no
// snapshot carries: a healthy object's, which only the replica tracking it
// can build. Set once at wiring, before the routes serve.
func (service *Service) SetLocalRows(row func(queryGroup string) (Anomaly, bool)) {
	if service != nil {
		service.localRow = row
	}
}

// SetLocalQueryRanges binds what this replica knows of an object's latest
// asked ranges (Tracker.QueryRanges) for the object detail.
func (service *Service) SetLocalQueryRanges(ranges func(queryGroup string) (QueryRanges, bool)) {
	if service != nil {
		service.localQueryRanges = ranges
	}
}

// LocalQueryRanges is this replica's account of the object's latest asked
// ranges, when it has one.
func (service *Service) LocalQueryRanges(queryGroup string) (QueryRanges, bool) {
	if service == nil || service.localQueryRanges == nil {
		return QueryRanges{}, false
	}
	return service.localQueryRanges(queryGroup)
}

// LocalRow is this replica's live row for the object, when it tracks it.
func (service *Service) LocalRow(queryGroup string) (Anomaly, bool) {
	if service == nil || service.localRow == nil {
		return Anomaly{}, false
	}
	return service.localRow(queryGroup)
}
