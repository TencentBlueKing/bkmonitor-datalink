package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// productionShapedTimeline builds a Schedule timeline of the size production
// carries. The 183 Plans below marshal to 146,696 bytes, against the 146,816
// bytes measured for one live timeline, so heap figures taken here describe
// the object the running process caches rather than a toy.
const productionShapedTimelinePlans = 183

func productionShapedTimeline(t testing.TB, queryGroup execution.QueryGroupIdentity, plans int) persistedScheduleTimeline {
	t.Helper()
	publication := execution.SnapshotPublicationRef{
		SnapshotRevision: execution.SnapshotRevision("2f6a3c1d8b5e47a09c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f60718293a4b5c6d7"),
		PublicationEpoch: 7,
	}
	schedules := make([]execution.FrozenPlanSchedule, 0, plans)
	records := make([]PlanActivationRecord, 0, plans)
	for index := 0; index < plans; index++ {
		spec := execution.ScheduleSpec{
			EvaluationIntervalSeconds: 60, Alignment: execution.EvaluationTime(index % 60), Timezone: "Asia/Shanghai",
		}
		revision, err := execution.DerivePlanScheduleRevision(spec)
		if err != nil {
			t.Fatal(err)
		}
		identity := execution.PlanIdentity{
			TenantID:   "system",
			BusinessID: strconv.Itoa(100000 + index%37),
			StrategyID: strconv.Itoa(4000000 + index),
		}
		schedules = append(schedules, execution.FrozenPlanSchedule{
			Identity: identity, ScheduleRevision: revision, Spec: spec,
		})
		records = append(records, PlanActivationRecord{
			Fact: execution.PlanActivationFact{
				Plan: identity, Selection: execution.ActivationCurrent,
				Selected: execution.ActivatedPlan{
					Identity: identity,
					StateGeneration: execution.StateGeneration(
						"9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b"),
					StateApplyEpoch: 3, ScheduleRevision: revision, RequiredFullSlots: 2,
				},
			},
			Publication: SnapshotPublicationRef{
				SnapshotRevision: publication.SnapshotRevision,
				PublicationEpoch: uint64(publication.PublicationEpoch),
			},
		})
	}
	groupRevision, err := execution.DeriveQueryGroupScheduleRevision(schedules)
	if err != nil {
		t.Fatal(err)
	}
	timeline := persistedScheduleTimeline{
		SchemaVersion: scheduleTimelineSchemaVersion, RecordRevision: 12, QueryGroup: queryGroup,
		Segments: []persistedScheduleSegment{{
			Schedule: execution.FrozenQueryGroupSchedule{
				Segment: execution.ScheduleSegmentFact{
					Publication: publication, QueryGroup: queryGroup,
					QueryRevision:    execution.QueryRevision("c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f70"),
					ScheduleRevision: groupRevision, Start: 1700000000,
				},
				Plans: schedules,
			},
			Plans: records,
		}},
	}
	if err := validateScheduleTimeline(timeline); err != nil {
		t.Fatal(err)
	}
	return timeline
}

func productionShapedTimelinePayload(t testing.TB, queryGroup execution.QueryGroupIdentity, plans int) []byte {
	t.Helper()
	payload, err := json.Marshal(productionShapedTimeline(t, queryGroup, plans))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// retainedHeapPerCopy measures what one decoded timeline keeps alive, by
// holding a batch of them across a collection. It is the only way to size the
// cache honestly: the object is charged to the heap, not to the payload.
func retainedHeapPerCopy(t testing.TB, queryGroup execution.QueryGroupIdentity, payload []byte, copies int) float64 {
	t.Helper()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	retained := make([]persistedScheduleTimeline, 0, copies)
	for index := 0; index < copies; index++ {
		decoded, err := decodeScheduleTimeline(queryGroup, payload)
		if err != nil {
			t.Fatal(err)
		}
		retained = append(retained, decoded)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(retained)
	return float64(after.HeapAlloc-before.HeapAlloc) / float64(copies)
}

// TestParsedScheduleTimelineHeapFootprint is the measurement the cache budget
// is derived from. The decoded object was expected to dwarf its JSON; it does
// not, because the JSON repeats a field name per value while the object
// repeats a struct field and the string bytes.
//
// Recorded on this fixture: 183 Plans, 146,708 byte payload, about 141,000
// bytes of retained heap - 0.96 payloads. The ratio runs 0.36 at one Plan,
// 1.04 at twenty and 0.89 at six hundred, which is what the 9/8 in
// cachedTimelineBytes bounds.
func TestParsedScheduleTimelineHeapFootprint(t *testing.T) {
	for _, plans := range []int{1, 20, productionShapedTimelinePlans, 600} {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("qg-footprint-%d", plans))
		payload := productionShapedTimelinePayload(t, queryGroup, plans)
		measured := retainedHeapPerCopy(t, queryGroup, payload, 64)
		charged := float64(cachedTimelineBytes(len(payload)))
		t.Logf("plans=%d payload=%d parsed_heap=%.0f ratio=%.3f charged=%.0f",
			plans, len(payload), measured, measured/float64(len(payload)), charged)
		// An entry is charged the decoded object it holds. Charging less than
		// it holds is the failure that matters: the process would sit above a
		// budget it believes it is under.
		if charged < measured {
			t.Fatalf("plans=%d charges %.0f bytes for %.0f bytes of decoded timeline", plans, charged, measured)
		}
		// Charging far more wastes budget instead of overrunning it, so this
		// only has to hold where the budget is actually spent.
		if plans == productionShapedTimelinePlans && charged > 1.5*measured {
			t.Fatalf("production shape charges %.0f bytes for %.0f bytes held", charged, measured)
		}
	}
}

// timelineCacheFixture serves one activation header and a timeline per Query
// Group out of a map, so a test can count the body reads the cache avoided.
func timelineCacheFixture(t *testing.T, queryGroups []execution.QueryGroupIdentity, plans int) (*RedisCatalogRepository, *scopeRedis) {
	t.Helper()
	client := &scopeRedis{values: map[string]string{}}
	repository, err := NewRedisCatalogRepository(client, "alarmd:control:timeline-cache", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	client.values[repository.activationHeaderKey()] = "header-1"
	for _, queryGroup := range queryGroups {
		client.values[repository.scheduleTimelineKey(queryGroup)] =
			string(productionShapedTimelinePayload(t, queryGroup, plans))
	}
	return repository, client
}

// TestScheduleTimelineCacheHitReusesDecodedTimeline is the regression guard on
// the change itself. Before it, a hit re-ran json.Unmarshal and the full
// timeline validation - including a canonical SHA-256 digest over every Plan -
// and handed back a freshly built object every time. A hit now returns the
// object decoded when the payload was read, which is what sharing the backing
// array proves.
func TestScheduleTimelineCacheHitReusesDecodedTimeline(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("qg-hit")
	repository, client := timelineCacheFixture(t, []execution.QueryGroupIdentity{queryGroup}, productionShapedTimelinePlans)
	ctx := context.Background()
	version := controlVersion{header: "header-1", known: true}

	first, err := repository.loadScheduleTimelineAt(ctx, queryGroup, version)
	if err != nil {
		t.Fatal(err)
	}
	readsAfterFirst := client.gets
	second, err := repository.loadScheduleTimelineAt(ctx, queryGroup, version)
	if err != nil {
		t.Fatal(err)
	}
	if client.gets != readsAfterFirst {
		t.Fatalf("a hit read the timeline body again: gets %d -> %d", readsAfterFirst, client.gets)
	}
	if &first.Segments[0] != &second.Segments[0] {
		t.Fatal("a hit rebuilt the decoded timeline instead of serving the cached one")
	}
	stats := repository.ControlReadCacheStats()
	if stats.Timeline.Hits != 1 || stats.Timeline.Misses != 1 || stats.Timeline.Refreshes != 0 {
		t.Fatalf("timeline cache outcomes = %+v", stats.Timeline)
	}
}

// TestScheduleTimelineForUpdateReadsLiveAndLeavesTheCacheIntact covers the one
// hazard shared decoding introduces: the compare-and-set paths close the open
// Segment in place and append the next one, and with a shared object those
// writes would land in the cache. They are kept apart by reading live rather
// than by copying, so the test states both halves - the read happened, and the
// writes that followed it did not reach the cached object.
func TestScheduleTimelineForUpdateReadsLiveAndLeavesTheCacheIntact(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("qg-update")
	repository, client := timelineCacheFixture(t, []execution.QueryGroupIdentity{queryGroup}, 4)
	ctx := WithControlVersionScope(context.Background())

	before, err := repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		t.Fatal(err)
	}
	openStart := before.Segments[0].Schedule.Segment.Start
	readsAfterWarm := client.gets

	update, payload, err := repository.loadScheduleTimelineForUpdate(ctx, queryGroup)
	if err != nil {
		t.Fatal(err)
	}
	if client.gets == readsAfterWarm {
		t.Fatal("the update path served the timeline from the cache instead of reading it live")
	}
	// The bytes exist for exactly one reason: they are the value a
	// compare-and-set must find unchanged.
	if len(payload) == 0 {
		t.Fatal("the update path returned no persisted bytes to fence the write on")
	}
	boundary := openStart + 3600
	closed := update.Segments[0].Schedule
	closed.Segment.End = &boundary
	update.Segments[0].Schedule = closed
	update.Segments = append(update.Segments, persistedScheduleSegment{})
	update.RecordRevision++
	retiredAt := boundary
	update.RetiredAt = &retiredAt

	after, err := repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Segments) != 1 {
		t.Fatalf("the cached timeline grew to %d Segments", len(after.Segments))
	}
	if after.Segments[0].Schedule.Segment.End != nil {
		t.Fatal("the cached timeline's open Segment was closed by a writer")
	}
	if after.RecordRevision != before.RecordRevision || after.RetiredAt != nil {
		t.Fatalf("the cached timeline was retired by a writer: revision=%d retired=%v",
			after.RecordRevision, after.RetiredAt)
	}
}

// TestControlTimelineCacheHoldsTheOwnedWorkingSet is the regression guard on
// the budget. The replaced 32 MiB constant held about 220 of the timelines one
// Worker owns, and production showed the consequence directly: over an hour
// the version header never moved, so every one of the 41% of lookups that
// missed had been evicted by that bound.
//
// The 300 timelines below are 43 MiB of payload - past the replaced constant,
// inside the reference container's derived share.
func TestControlTimelineCacheHoldsTheOwnedWorkingSet(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates the working set of one Worker")
	}
	const owned = 300
	queryGroups := make([]execution.QueryGroupIdentity, 0, owned)
	for index := 0; index < owned; index++ {
		queryGroups = append(queryGroups, execution.QueryGroupIdentity(fmt.Sprintf("qg-owned-%03d", index)))
	}
	repository, _ := timelineCacheFixture(t, queryGroups, productionShapedTimelinePlans)
	ctx := context.Background()
	version := controlVersion{header: "header-1", known: true}
	for _, queryGroup := range queryGroups {
		if _, err := repository.loadScheduleTimelineAt(ctx, queryGroup, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, queryGroup := range queryGroups {
		if _, err := repository.loadScheduleTimelineAt(ctx, queryGroup, version); err != nil {
			t.Fatal(err)
		}
	}
	stats := repository.ControlReadCacheStats()
	if stats.Timeline.Hits != owned || stats.Timeline.Misses != owned {
		t.Fatalf("second pass over %d owned Query Groups: %+v", owned, stats.Timeline)
	}
	if stats.TimelineOccupancy.Entries != owned || stats.TimelineOccupancy.Evictions != 0 {
		t.Fatalf("cache holding %d of %d owned Query Groups after %d evictions",
			stats.TimelineOccupancy.Entries, owned, stats.TimelineOccupancy.Evictions)
	}
}
