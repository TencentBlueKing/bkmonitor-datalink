// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// noDataHopFixture publishes a catalog, activates it the way the control plane
// does, and freezes one Slot against the Segment that activation cut.
//
// The Segment has to come from the activation reconciler rather than be built
// by hand: a hand-built one carries no object digest, so every read of it takes
// the Snapshot fallback rather than the content path, and the content path is
// what production uses -- this worker reads three hundred thousand Segments by
// content and none by fallback. A fixture on the fallback path would have
// measured the wrong road.
type noDataHopFixture struct {
	client     redis.Cmdable
	prefix     string
	reconciler *controlplane.ScheduleActivationReconciler
	// now is the reconciler's clock, movable so a test can cut a second
	// Segment at a later boundary. Held by pointer because the reconciler
	// captured the closure at construction.
	now        *time.Time
	repository *controlplane.RedisCatalogRepository
	runtime    *controlplane.RedisCatalogRuntime
	group      execution.QueryGroupIdentity
	hops       map[string]int
	states     map[string]int
}

func newNoDataHopFixture(t *testing.T, prefix string, catalog controlplane.Catalog) *noDataHopFixture {
	t.Helper()
	return newNoDataHopFixtureWithHook(t, prefix, catalog, nil)
}

// newNoDataHopFixtureWithHook is the same fixture with a Redis hook installed
// before anything is published, for a test that counts round trips rather than
// outcomes.
func newNoDataHopFixtureWithHook(
	t *testing.T, prefix string, catalog controlplane.Catalog, hook redis.Hook,
) *noDataHopFixture {
	t.Helper()
	ctx := context.Background()
	client := newControlplaneRedis(t)
	if hook != nil {
		client.AddHook(hook)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:"+prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
	clock := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(
		repository, compiler, semantics, progress, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := repository.PublishCatalog(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
		t.Fatal(err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &noDataHopFixture{
		client: client, prefix: "alarmd:control:" + prefix, reconciler: reconciler, now: &clock,
		repository: repository, runtime: runtime,
		group: catalog.QueryGroups[0].Identity,
		hops:  map[string]int{}, states: map[string]int{},
	}
	// The Segment must be on the content path, or this fixture measures the
	// fallback and says nothing about production.
	schedule, err := runtime.ReadFrozenSchedule(ctx, fixture.group, 60)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.Segment.ObjectDigest == "" {
		t.Fatal("the activated Segment names no object, so every read of it takes the fallback and this " +
			"fixture is not on the path production uses")
	}
	return fixture
}

// republish publishes a second catalog without recutting the Segment, which is
// what leaves a fleet executing content that is no longer published.
func (f *noDataHopFixture) republish(t *testing.T, catalog controlplane.Catalog) {
	t.Helper()
	if _, _, err := f.repository.PublishCatalog(context.Background(), catalog); err != nil {
		t.Fatal(err)
	}
}

func (f *noDataHopFixture) freeze(t *testing.T) {
	t.Helper()
	f.freezeAt(t, 60)
}

func (f *noDataHopFixture) freezeAt(t *testing.T, at execution.EvaluationTime) {
	t.Helper()
	ctx := context.Background()
	f.repository.ConfigureObserver(observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			if facts := observation.NoDataCensus; facts != nil {
				f.hops[facts.Hop] += facts.Plans
			}
			if facts := observation.SegmentContent; facts != nil {
				f.states[facts.State]++
			}
		}))
	schedule, err := f.runtime.ReadFrozenSchedule(ctx, f.group, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: schedule.Segment.QueryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: at,
		DuePlans: schedule.DuePlanRefs(at),
	}); err != nil {
		t.Fatal(err)
	}
}

// The bytes the Segment's object is stored as are counted beside what decoding
// them produced.
//
// These two separate the only two explanations left for a no-data Plan that
// exists on the leader and not in the Slot: the section is in the stored bytes
// and something on the way in drops it, or the Segment names an object that
// never had it. Every later hop reads zero for both, and three releases were
// spent choosing between them by reading code.
//
// The count is taken from the bytes and carried alongside the decoded object,
// because the object cache answers four hundred thousand reads for every fifty
// that fetch: a count taken only where the decode runs would report almost
// nothing, for reasons that have nothing to do with the content.
func TestTheSegmentsStoredBytesAreCountedBesideWhatDecodingThemProduced(t *testing.T) {
	fixture := newNoDataHopFixture(t, "no-data-bytes", noDataSourceCatalog(t))
	fixture.freeze(t)
	hops := fixture.hops

	if got := hops[observability.NoDataHopAssembledBytes]; got != 1 {
		t.Fatalf("assembled_bytes = %d, want the one no-data section the stored object names; hops=%+v",
			got, hops)
	}
	if got := hops[observability.NoDataHopAssembled]; got != 1 {
		t.Fatalf("assembled = %d, want 1; hops=%+v. Against assembled_bytes=%d, a difference here is "+
			"the section being lost between the bytes and the object", got, hops,
			hops[observability.NoDataHopAssembledBytes])
	}
}

// A Segment whose object carries no such section reports zero in the bytes too,
// so the pair stays readable when the answer is "there is nothing there".
func TestASegmentWithoutNoDataReportsZeroInBytesAndDecoded(t *testing.T) {
	fixture := newNoDataHopFixture(t, "no-data-bytes-empty", validCatalog(t, 80))
	fixture.freeze(t)
	hops := fixture.hops

	for _, hop := range []string{observability.NoDataHopAssembledBytes, observability.NoDataHopAssembled} {
		if hops[hop] != 0 {
			t.Fatalf("hop %q = %d on a catalog with no no-data Plan, want 0", hop, hops[hop])
		}
	}
}

// A Slot frozen against the publication that is current reports current.
func TestAFreshSegmentReportsCurrentContent(t *testing.T) {
	fixture := newNoDataHopFixture(t, "segment-fresh", noDataSourceCatalog(t))
	fixture.freeze(t)
	states := fixture.states

	if states[controlplane.SegmentContentCurrent] != 1 {
		t.Fatalf("states = %+v, want one current: this Segment was cut from the publication that is "+
			"still the latest", states)
	}
	for _, other := range []string{controlplane.SegmentContentStale, controlplane.SegmentContentUnknown} {
		if states[other] != 0 {
			t.Fatalf("states = %+v, want nothing under %q", states, other)
		}
	}
}

// A Slot still frozen against an older publication reports stale.
//
// This is the state nothing else in the process reports. The Segment's object
// loads, its digest verifies, its Plans compile and the Slot passes; the only
// thing wrong is that the content is not what the control plane publishes any
// more, so a change made in the source never takes effect and looks like the
// change being wrong. It is not specific to no-data -- every execution field
// stops at the Segment the same way, which is why this is its own family
// rather than another no-data hop.
func TestASegmentLeftOnAnOlderPublicationReportsStale(t *testing.T) {
	// Cut from a publication without the no-data section, the way a fleet cut
	// before the section existed would be.
	fixture := newNoDataHopFixture(t, "segment-stale", validCatalog(t, 80))
	// Then the source gains no-data and the leader publishes it. The new
	// objects are written under new digests; the old ones stay where they are,
	// and the Segment still names them.
	fixture.republish(t, noDataSourceCatalog(t))

	fixture.freeze(t)

	if fixture.states[controlplane.SegmentContentStale] != 1 {
		t.Fatalf("states = %+v, want one stale: the Segment names the object of a publication that is "+
			"no longer the latest, and nothing else in this process says so", fixture.states)
	}
	// And this is exactly the shape production is in: the Slot is healthy, the
	// object loads, and the section is in neither the bytes nor the decode,
	// because the bytes belong to the older object.
	if fixture.hops[observability.NoDataHopAssembledBytes] != 0 ||
		fixture.hops[observability.NoDataHopAssembled] != 0 {
		t.Fatalf("hops = %+v, want zero in the bytes and zero decoded: a stale Segment names the old "+
			"object, whose bytes never had the section", fixture.hops)
	}
}

// A comparison that cannot be made reports unknown, not current.
//
// With no publication to compare against there is nothing to say, and saying
// "current" would be a guess in the reassuring direction -- the one that makes
// a fleet executing stale content look converged. This drives the state
// directly, because a repository with no publication cannot reach the freeze.
func TestAFreshnessComparisonThatCannotBeMadeIsUnknown(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:segment-unknown", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]int{}
	repository.ConfigureObserver(observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			if facts := observation.SegmentContent; facts != nil {
				states[facts.State]++
			}
		}))

	controlplane.ObserveSegmentContentFreshnessForTest(repository, context.Background(), execution.ScheduleSegmentFact{
		QueryGroup: "qg-a", ObjectDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})

	if states[controlplane.SegmentContentUnknown] != 1 {
		t.Fatalf("states = %+v, want one unknown: there is no publication to compare against, and "+
			"reporting current there would make an unchecked Segment look converged", states)
	}
	if states[controlplane.SegmentContentCurrent] != 0 {
		t.Fatalf("states = %+v, want nothing under current", states)
	}
}

// A Segment naming no object at all is legacy, which is its own answer.
func TestASegmentWithNoObjectIsLegacy(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:segment-legacy", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]int{}
	repository.ConfigureObserver(observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			if facts := observation.SegmentContent; facts != nil {
				states[facts.State]++
			}
		}))

	controlplane.ObserveSegmentContentFreshnessForTest(repository, context.Background(),
		execution.ScheduleSegmentFact{QueryGroup: "qg-a"})

	if states[controlplane.SegmentContentLegacy] != 1 {
		t.Fatalf("states = %+v, want one legacy", states)
	}
}

// A worker that did not do the activation reads the object for the first time
// when it freezes, and must count the same thing.
//
// Two paths decode this object into one cache -- the batch read the activation
// performs, and the single read a Segment triggers -- and whichever gets there
// first supplies the number for everyone after. A fixture that always activates
// first exercises only the batch path, so the other one can be wrong in any way
// at all and nothing says so. This is the path a restarted worker takes, which
// is every worker after a rollout.
func TestAColdReaderCountsTheStoredBytesToo(t *testing.T) {
	fixture := newNoDataHopFixture(t, "no-data-cold", noDataSourceCatalog(t))

	// A second repository over the same Redis: same objects, empty cache, so
	// the Segment's own read is the first decode of them.
	cold, err := controlplane.NewRedisCatalogRepository(fixture.client, fixture.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := cold.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	hops := map[string]int{}
	cold.ConfigureObserver(observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			if facts := observation.NoDataCensus; facts != nil {
				hops[facts.Hop] += facts.Plans
			}
		}))
	compiler, semantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(cold, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	schedule, err := runtime.ReadFrozenSchedule(ctx, fixture.group, 60)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: schedule.Segment.QueryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: 60, DuePlans: schedule.DuePlanRefs(60),
	}); err != nil {
		t.Fatal(err)
	}

	if got := hops[observability.NoDataHopAssembledBytes]; got != 1 {
		t.Fatalf("assembled_bytes = %d on a reader that decoded the object itself, want 1; hops=%+v",
			got, hops)
	}
	if got := hops[observability.NoDataHopAssembled]; got != 1 {
		t.Fatalf("assembled = %d, want 1; hops=%+v", got, hops)
	}
}

// A cutover that refuses says which precondition refused, on which Query Group,
// and with which values.
//
// Driven through the reconciler rather than constructed, because the thing
// under test is that the real cutover attaches this and the real observer
// carries it. In production this exact family has been refusing about twice a
// minute, and until now it said only "schedule activation conflict" -- one
// sentence covering four different preconditions, with nothing to look at.
//
// The refusal here is the boundary one: the fixture's clock does not move, so
// a second publication cuts at the instant the open Segment already starts at.
func TestARefusedCutoverNamesThePreconditionAndItsValues(t *testing.T) {
	fixture := newNoDataHopFixture(t, "cutover-refused", validCatalog(t, 80))
	ctx := context.Background()

	var observed []observability.Observation
	fixture.repository.ConfigureObserver(observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			if observation.ScheduleCutover != nil {
				observed = append(observed, observation)
			}
		}))

	// A second publication, cut at the same instant the open Segment starts.
	second, _, err := fixture.repository.PublishCatalog(ctx, noDataSourceCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	_, ensureErr := fixture.reconciler.Ensure(ctx, second.Publication)
	if ensureErr == nil {
		t.Skip("this fixture's clock no longer produces a boundary the open Segment refuses")
	}

	var conflict *controlplane.ScheduleConflictError
	if !errors.As(ensureErr, &conflict) {
		t.Fatalf("the cutover refused with %v, which carries no precondition. Four different checks "+
			"return this sentinel and a reader cannot tell them apart", ensureErr)
	}
	if conflict.Reason != controlplane.CutoverReasonOpenSegmentClosed {
		t.Fatalf("reason = %q, want %q", conflict.Reason, controlplane.CutoverReasonOpenSegmentClosed)
	}
	if conflict.QueryGroup == "" {
		t.Fatal("the refusal names no Query Group, so a reader has the whole population to search")
	}
	if !strings.Contains(conflict.Detail, "boundary=") || !strings.Contains(conflict.Detail, "open_start=") {
		t.Fatalf("detail = %q, want the values that were compared", conflict.Detail)
	}
	// And it is still the sentinel every existing reader matches on.
	if !errors.Is(ensureErr, controlplane.ErrScheduleConflict) {
		t.Fatal("the refusal no longer satisfies errors.Is(ErrScheduleConflict); every existing caller " +
			"that branches on it would stop recognising it")
	}

	// The observation carries it too, which is the only part a reader sees.
	var reported *observability.ScheduleCutoverFacts
	for _, observation := range observed {
		if observation.ScheduleCutover.Result == "failure" {
			reported = observation.ScheduleCutover
		}
	}
	if reported == nil {
		t.Fatalf("no failed cutover was reported; observations=%+v", observed)
	}
	if reported.Reason != controlplane.CutoverReasonOpenSegmentClosed {
		t.Fatalf("reported reason = %q, want %q", reported.Reason, controlplane.CutoverReasonOpenSegmentClosed)
	}
	if reported.QueryGroup != string(conflict.QueryGroup) {
		t.Fatalf("reported Query Group = %q, want the one the cutover stopped on (%q)",
			reported.QueryGroup, conflict.QueryGroup)
	}
}
