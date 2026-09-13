// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// TestAssembleQueryGroupIsTheInverseOfTheObjectSplit: splitting a compiled
// Query Group into its execution object and its Plans' output contexts and
// putting it back together yields the Query Group the compiler produced,
// field for field, PlanRevision aside - for a source with and without a
// Python revision, under the legacy and the native protocol.
func TestAssembleQueryGroupIsTheInverseOfTheObjectSplit(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol string
		edit     func(map[string]any)
	}{
		{name: "legacy source without a python revision", protocol: ""},
		{name: "python revision under the forced legacy protocol", protocol: "legacy", edit: func(document map[string]any) { document["strategy_revision"] = float64(5) }},
		{name: "python revision under the native protocol", protocol: "native", edit: func(document map[string]any) {
			document["strategy_revision"] = float64(5)
			document["labels"] = []any{"ops", "tier-1"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			built := buildObjectCatalog(t, test.protocol, objectStrategyDocument(t, test.edit))
			contexts := make(map[execution.PlanIdentity]controlplane.OutputContextObject)
			for _, plan := range built.group.Plans {
				contexts[plan.Identity] = controlplane.BuildOutputContext(plan)
			}
			assembled, err := controlplane.AssembleQueryGroup(controlplane.BuildQueryGroupObject(built.group), contexts)
			if err != nil {
				t.Fatal(err)
			}
			want := built.group
			for index := range want.Plans {
				want.Plans[index].PlanRevision = ""
			}
			if !reflect.DeepEqual(assembled, want) {
				t.Fatalf("assembled Query Group differs from the compiled one:\n got=%+v\nwant=%+v", assembled, want)
			}
			// The round trip is also invisible to the object digest.
			before, err := controlplane.DeriveQueryGroupObjectDigest(built.group)
			if err != nil {
				t.Fatal(err)
			}
			after, err := controlplane.DeriveQueryGroupObjectDigest(assembled)
			if err != nil || after != before {
				t.Fatalf("object digest changed across the round trip: %s -> %s (%v)", before, after, err)
			}
		})
	}
	// A context that belongs to another Plan, or a missing one, is refused
	// rather than assembled into a Query Group that says something else.
	built := buildObjectCatalog(t, "", objectStrategyDocument(t, nil))
	object := controlplane.BuildQueryGroupObject(built.group)
	if _, err := controlplane.AssembleQueryGroup(object, nil); err == nil {
		t.Fatal("a Plan without an output context must be refused")
	}
	foreign := controlplane.BuildOutputContext(built.group.Plans[0])
	foreign.StrategyRef.StrategyID = "other"
	if _, err := controlplane.AssembleQueryGroup(object, map[execution.PlanIdentity]controlplane.OutputContextObject{built.group.Plans[0].Identity: foreign}); err == nil {
		t.Fatal("an output context of another strategy must be refused")
	}
}

type objectReadObserver struct {
	mu    sync.Mutex
	reads map[string]int
}

func (observer *objectReadObserver) Observe(_ context.Context, observation observability.Observation) {
	if observation.ObjectRead == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.reads == nil {
		observer.reads = make(map[string]int)
	}
	observer.reads[observation.ObjectRead.Kind+"/"+observation.ObjectRead.Result]++
}

func (observer *objectReadObserver) count(kind, result string) int {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return observer.reads[kind+"/"+result]
}

// objectGetCountingHook counts and optionally delays GETs of catalog object
// keys, so a test can tell how many network reads a set of readers cost.
type objectGetCountingHook struct {
	gets  atomic.Int64
	delay time.Duration
}

func (hook *objectGetCountingHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if strings.ToLower(cmd.Name()) == "get" && len(cmd.Args()) > 1 {
		if key, ok := cmd.Args()[1].(string); ok && (strings.Contains(key, ":qgobj:") || strings.Contains(key, ":outctx:")) {
			hook.gets.Add(1)
			time.Sleep(hook.delay)
		}
	}
	return ctx, nil
}

func (*objectGetCountingHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (*objectGetCountingHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (*objectGetCountingHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// TestActivatedSegmentIsReadByContent follows one publication from the
// leader to a Worker: the Segment the activation opens names the object and
// the output contexts the manifest wrote; a Worker reading that Segment gets
// the Query Group by content, equal to what the Snapshot holds, from the
// network once and from its cache after; and every way the content path is
// not taken - a Segment without a digest, a missing object, corrupt bytes -
// falls back to the Snapshot under its own counted reason.
func TestActivatedSegmentIsReadByContent(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:object-read"
	observer := &objectReadObserver{}
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	repository.ConfigureObserver(observer)
	if err := repository.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
	compiler, semantics := runtimePlanCompiler(t)
	at := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	catalog := objectCatalogTwoGroups(t, 80)
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
	manifest, err := repository.LoadCatalogManifest(ctx, catalog.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	group := catalog.QueryGroups[0]
	schedule, err := runtime.ReadFrozenSchedule(ctx, group.Identity, 60)
	if err != nil {
		t.Fatal(err)
	}
	segment := schedule.Segment
	var named execution.ObjectDigest
	for _, entry := range manifest.QueryGroups {
		if entry.QueryGroup == group.Identity {
			named = entry.ObjectDigest
		}
	}
	if segment.ObjectDigest == "" || segment.ObjectDigest != named {
		t.Fatalf("the activated Segment names %q, the manifest %q", segment.ObjectDigest, named)
	}
	if len(segment.OutputContextRefs) != len(group.Plans) || segment.OutputContextRefFor(group.Plans[0].Identity) == "" {
		t.Fatalf("the activated Segment names no output context: %+v", segment.OutputContextRefs)
	}

	fallbackCalls := 0
	fallback := func(ctx context.Context) (controlplane.QueryGroup, error) {
		fallbackCalls++
		return repository.LoadQueryGroup(ctx, segment.Publication.SnapshotRevision, group.Identity)
	}
	read, err := repository.LoadSegmentQueryGroup(ctx, segment, 60, fallback)
	if err != nil {
		t.Fatal(err)
	}
	want := group
	for index := range want.Plans {
		want.Plans[index].PlanRevision = ""
	}
	if !reflect.DeepEqual(read, want) || fallbackCalls != 0 {
		t.Fatalf("Query Group read by content differs from the Snapshot's (fallback calls=%d):\n got=%+v\nwant=%+v", fallbackCalls, read, want)
	}
	if observer.count("segment", "object") != 1 || observer.count("query_group", "miss") != 1 || observer.count("output_context", "miss") != 1 {
		t.Fatalf("first read counters=%+v", observer.reads)
	}
	if _, err := repository.LoadSegmentQueryGroup(ctx, segment, 60, fallback); err != nil || fallbackCalls != 0 {
		t.Fatalf("second read: err=%v fallback calls=%d", err, fallbackCalls)
	}
	if observer.count("query_group", "hit") != 1 || observer.count("output_context", "hit") != 1 || observer.count("segment", "object") != 2 {
		t.Fatalf("second read counters=%+v", observer.reads)
	}

	// A Segment without a digest is read the way it always was.
	legacy := segment
	legacy.ObjectDigest, legacy.OutputContextRefs = "", nil
	if _, err := repository.LoadSegmentQueryGroup(ctx, legacy, 60, fallback); err != nil || fallbackCalls != 1 || observer.count("segment", "legacy_segment") != 1 {
		t.Fatalf("legacy segment: err=%v fallback calls=%d counters=%+v", err, fallbackCalls, observer.reads)
	}
	// A Segment naming an object but no context for a Plan falls back too.
	withoutRef := segment
	withoutRef.OutputContextRefs = nil
	if _, err := repository.LoadSegmentQueryGroup(ctx, withoutRef, 60, fallback); err != nil || fallbackCalls != 2 || observer.count("segment", "segment_without_ref") != 1 {
		t.Fatalf("segment without ref: err=%v fallback calls=%d counters=%+v", err, fallbackCalls, observer.reads)
	}
	// A missing object falls back; a fresh process is used so no cache hides
	// the absence.
	cold, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cold.ConfigureObserver(observer)
	objectKey := prefix + ":qgobj:" + string(segment.ObjectDigest)
	stored, err := client.Get(ctx, objectKey).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, objectKey).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := cold.LoadSegmentQueryGroup(ctx, segment, 60, fallback); err != nil || fallbackCalls != 3 || observer.count("segment", "object_missing") != 1 {
		t.Fatalf("missing object: err=%v fallback calls=%d counters=%+v", err, fallbackCalls, observer.reads)
	}
	// Corrupt bytes under the digest are refused and fall back.
	if err := client.Set(ctx, objectKey, append(append([]byte(nil), stored...), ' '), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := cold.LoadSegmentQueryGroup(ctx, segment, 60, fallback); err != nil || fallbackCalls != 4 || observer.count("segment", "object_invalid") != 1 || observer.count("query_group", "invalid") != 1 {
		t.Fatalf("corrupt object: err=%v fallback calls=%d counters=%+v", err, fallbackCalls, observer.reads)
	}
	if err := client.Set(ctx, objectKey, stored, 0).Err(); err != nil {
		t.Fatal(err)
	}
	// A fallback that fails is the caller's error, not hidden.
	failing := func(context.Context) (controlplane.QueryGroup, error) {
		return controlplane.QueryGroup{}, errors.New("snapshot gone")
	}
	if _, err := cold.LoadSegmentQueryGroup(ctx, legacy, 60, failing); err == nil || err.Error() != "snapshot gone" {
		t.Fatalf("failing fallback: err=%v", err)
	}
	// A Segment naming an object of another Query Group is refused.
	other := segment
	other.QueryGroup = catalog.QueryGroups[1].Identity
	if _, err := cold.LoadSegmentQueryGroup(ctx, other, 60, fallback); err != nil || fallbackCalls != 5 || observer.count("segment", "object_mismatch") != 1 {
		t.Fatalf("mismatched object: err=%v fallback calls=%d counters=%+v", err, fallbackCalls, observer.reads)
	}
}

// TestConcurrentReadersOfOneObjectShareOneNetworkRead: many Slots of one
// process missing the same digest at once cost one GET, and every one of
// them gets the object.
func TestConcurrentReadersOfOneObjectShareOneNetworkRead(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:object-read-shared"
	hook := &objectGetCountingHook{delay: 50 * time.Millisecond}
	client.AddHook(hook)
	observer := &objectReadObserver{}
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	repository.ConfigureObserver(observer)
	if err := repository.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	catalog := objectCatalogTwoGroups(t, 80)
	if _, _, err := repository.PublishCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	digest, err := controlplane.DeriveQueryGroupObjectDigest(catalog.QueryGroups[0])
	if err != nil {
		t.Fatal(err)
	}
	const readers = 16
	var wg sync.WaitGroup
	results := make([]error, readers)
	for index := 0; index < readers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			object, err := repository.LoadQueryGroupObject(ctx, digest)
			if err == nil && object.Identity != catalog.QueryGroups[0].Identity {
				err = errors.New("wrong object")
			}
			results[index] = err
		}(index)
	}
	wg.Wait()
	for index, err := range results {
		if err != nil {
			t.Fatalf("reader %d: %v", index, err)
		}
	}
	if gets := hook.gets.Load(); gets != 1 {
		t.Fatalf("%d concurrent readers cost %d network reads, want 1", readers, gets)
	}
	if observer.count("query_group", "miss") != 1 || observer.count("query_group", "share") != readers-1 {
		t.Fatalf("counters=%+v", observer.reads)
	}
}

// The persisted Segment carries the digest and the refs under field names an
// older binary ignores; the timeline stays decodable by the shape it knew.
func TestPersistedSegmentCarriesDigestUnderIgnorableFields(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:object-read-persisted"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
	compiler, semantics := runtimePlanCompiler(t)
	at := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	snapshot, _, err := repository.PublishCatalog(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
		t.Fatal(err)
	}
	raw, err := client.Get(ctx, prefix+":schedule_timeline:"+string(catalog.QueryGroups[0].Identity)).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Segments []struct {
			Schedule struct {
				Segment struct {
					Start             execution.EvaluationTime
					ObjectDigest      string
					OutputContextRefs []struct {
						Plan   execution.PlanIdentity
						Digest string
					}
				}
			} `json:"schedule"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	segment := persisted.Segments[0].Schedule.Segment
	want, err := controlplane.DeriveQueryGroupObjectDigest(catalog.QueryGroups[0])
	if err != nil {
		t.Fatal(err)
	}
	if segment.Start != 60 || segment.ObjectDigest != string(want) || len(segment.OutputContextRefs) != 1 || segment.OutputContextRefs[0].Plan != catalog.QueryGroups[0].Plans[0].Identity {
		t.Fatalf("persisted segment=%+v want digest %s", segment, want)
	}
	// The shape an older binary decodes, with unknown fields refused, still
	// reads everything it knew.
	var older struct {
		Segments []struct {
			Schedule struct {
				Segment struct {
					Publication      execution.SnapshotPublicationRef
					QueryGroup       execution.QueryGroupIdentity
					QueryRevision    execution.QueryRevision
					ScheduleRevision execution.ScheduleRevision
					Start            execution.EvaluationTime
					End              *execution.EvaluationTime
				}
			} `json:"schedule"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &older); err != nil || older.Segments[0].Schedule.Segment.QueryGroup != catalog.QueryGroups[0].Identity {
		t.Fatalf("older shape: err=%v segment=%+v", err, older.Segments[0].Schedule.Segment)
	}
}
