// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// controlReadCountingHook counts Redis commands per control object and sums
// the reply bytes that actually crossed the wire, so tests can prove which
// bodies were transferred rather than which calls were made.
type controlReadCountingHook struct {
	mu      sync.Mutex
	counts  map[string]int
	bytes   map[string]int
	batches int
}

func newControlReadCountingHook() *controlReadCountingHook {
	return &controlReadCountingHook{counts: map[string]int{}, bytes: map[string]int{}}
}

func controlObjectOfKey(key string) string {
	switch {
	case strings.HasSuffix(key, ":activation_header"):
		return "header"
	case strings.HasSuffix(key, ":activation"):
		return "activation"
	case strings.Contains(key, ":schedule_timeline:"):
		return "timeline"
	case strings.Contains(key, ":snapshot_epoch:"):
		return "epoch"
	case strings.Contains(key, ":snapshot:"):
		return "snapshot"
	case strings.Contains(key, ":publication:"):
		return "publication"
	case strings.Contains(key, ":active_qg_set:"):
		return "active_set"
	case strings.Contains(key, ":qgobj:"):
		return "object"
	case strings.Contains(key, ":outctx:"):
		return "context"
	default:
		return "other"
	}
}

func controlObjectOfCommand(cmd redis.Cmder) (string, string) {
	args := cmd.Args()
	name := strings.ToLower(cmd.Name())
	keyIndex := 1
	if name == "eval" {
		keyIndex = 3
	}
	if len(args) <= keyIndex {
		return name, "other"
	}
	return name, controlObjectOfKey(fmt.Sprint(args[keyIndex]))
}

func (hook *controlReadCountingHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	name, object := controlObjectOfCommand(cmd)
	hook.mu.Lock()
	hook.counts[name+" "+object]++
	hook.mu.Unlock()
	return ctx, nil
}

func (hook *controlReadCountingHook) AfterProcess(_ context.Context, cmd redis.Cmder) error {
	_, object := controlObjectOfCommand(cmd)
	size := 0
	switch typed := cmd.(type) {
	case *redis.StringCmd:
		size = len(typed.Val())
	case *redis.SliceCmd:
		for _, value := range typed.Val() {
			if text, ok := value.(string); ok {
				size += len(text)
			}
		}
	case *redis.Cmd:
		if values, err := typed.Slice(); err == nil {
			for _, value := range values {
				if text, ok := value.(string); ok {
					size += len(text)
				}
			}
		}
	}
	hook.mu.Lock()
	hook.bytes[object] += size
	hook.mu.Unlock()
	return nil
}

// The pipeline hooks used to do nothing, which made every command sent in a
// batch invisible to these counts. That is fine only while nothing on the read
// path batches; the version read now sends its header GET and activation STRLEN
// together, and a hook that ignores batches would have reported those commands
// as having stopped rather than as having been merged.
//
// Commands are counted the same either way, so the per-command assertions below
// mean what they always meant. batches is the separate fact: it says how many
// round trips those commands cost.
func (hook *controlReadCountingHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	hook.mu.Lock()
	hook.batches++
	for _, cmd := range cmds {
		name, object := controlObjectOfCommand(cmd)
		hook.counts[name+" "+object]++
	}
	hook.mu.Unlock()
	return ctx, nil
}

func (hook *controlReadCountingHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	for _, cmd := range cmds {
		if err := hook.AfterProcess(ctx, cmd); err != nil {
			return err
		}
	}
	return nil
}

func (hook *controlReadCountingHook) batchCount() int {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	return hook.batches
}

func (hook *controlReadCountingHook) reset() {
	hook.mu.Lock()
	hook.counts = map[string]int{}
	hook.bytes = map[string]int{}
	hook.batches = 0
	hook.mu.Unlock()
}

func (hook *controlReadCountingHook) count(command, object string) int {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	return hook.counts[command+" "+object]
}

func (hook *controlReadCountingHook) bodyReads(object string) int {
	return hook.count("get", object) + hook.count("mget", object) + hook.count("eval", object)
}

func (hook *controlReadCountingHook) objectBytes(object string) int {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	return hook.bytes[object]
}

type controlReadCacheFixture struct {
	client     *redis.Client
	hook       *controlReadCountingHook
	prefix     string
	repository *controlplane.RedisCatalogRepository
	reconciler *controlplane.ScheduleActivationReconciler
	catalog    controlplane.Catalog
	snapshot   controlplane.PublishedSnapshot
	queryGroup execution.QueryGroupIdentity
	request    execution.FreezeSlotContractRequest
	contract   execution.FrozenSlotContractFact
	clock      *time.Time
}

// newControlReadCacheFixture publishes one scheduled catalog, activates it as
// the Control Leader would, freezes one Slot and only then attaches the
// counting hook. The returned fixture repository is warm; tests open cold
// repositories on the same client to observe first reads.
func newControlReadCacheFixture(t *testing.T, name string) controlReadCacheFixture {
	t.Helper()
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:read-cache-" + name
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	clock := time.Unix(83, 0)
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
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
	queryGroup := catalog.QueryGroups[0].Identity
	schedule, err := runtime.ReadFrozenSchedule(ctx, queryGroup, 120)
	if err != nil {
		t.Fatal(err)
	}
	request := execution.FreezeSlotContractRequest{
		QueryGroup: queryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: 120, DuePlans: schedule.DuePlanRefs(120),
	}
	contract, err := runtime.FreezeSlotContract(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	hook := newControlReadCountingHook()
	client.AddHook(hook)
	return controlReadCacheFixture{
		client: client, hook: hook, prefix: prefix, repository: repository, reconciler: reconciler,
		catalog: catalog, snapshot: snapshot, queryGroup: queryGroup, request: request, contract: contract, clock: &clock,
	}
}

func (fixture controlReadCacheFixture) coldRepository(t *testing.T) (*controlplane.RedisCatalogRepository, *controlplane.RedisCatalogRuntime) {
	t.Helper()
	repository, err := controlplane.NewRedisCatalogRepository(fixture.client, fixture.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return repository, runtime
}

func (fixture controlReadCacheFixture) activationRequest() execution.PlanActivationRequest {
	return execution.PlanActivationRequest{
		Contract: fixture.contract.Contract,
		Plans:    []execution.PlanIdentity{fixture.contract.DuePlans[0].Identity},
	}
}

func TestControlReadCacheReadsActivationAndTimelineOncePerHeader(t *testing.T) {
	fixture := newControlReadCacheFixture(t, "once-per-header")
	ctx := context.Background()
	repository, runtime := fixture.coldRepository(t)
	activationLen, err := fixture.client.StrLen(ctx, fixture.prefix+":activation").Result()
	if err != nil {
		t.Fatal(err)
	}
	timelineLen, err := fixture.client.StrLen(ctx, fixture.prefix+":schedule_timeline:"+string(fixture.queryGroup)).Result()
	if err != nil {
		t.Fatal(err)
	}
	headerLen, err := fixture.client.StrLen(ctx, fixture.prefix+":activation_header").Result()
	if err != nil {
		t.Fatal(err)
	}
	fixture.hook.reset()

	const authorizations = 5
	for i := 0; i < authorizations; i++ {
		result, err := repository.LoadActivations(ctx, fixture.activationRequest())
		if err != nil || len(result.Facts) != 1 || result.Facts[0].Selection != execution.ActivationCurrent {
			t.Fatalf("LoadActivations #%d = (%+v, %v)", i, result, err)
		}
	}
	hook := fixture.hook
	if got := hook.bodyReads("activation"); got != 1 {
		t.Fatalf("activation body reads=%d, want 1 for %d authorizations", got, authorizations)
	}
	if got := hook.bodyReads("timeline"); got != 1 {
		t.Fatalf("timeline body reads=%d, want 1 for %d authorizations", got, authorizations)
	}
	if got := hook.count("get", "header"); got != authorizations {
		t.Fatalf("header probes=%d, want one per authorization (%d)", got, authorizations)
	}
	if got := hook.count("strlen", "activation"); got != authorizations {
		t.Fatalf("activation length probes=%d, want one per authorization (%d)", got, authorizations)
	}
	// The two probes above are one round trip, not two. This is the assertion
	// the command counts cannot make: they were already both being sent, and
	// what changed is that they now travel together.
	if got := hook.batchCount(); got != authorizations {
		t.Fatalf("version read batches=%d, want one per authorization (%d): the header and the "+
			"activation length have to travel in one round trip", got, authorizations)
	}
	if got := hook.objectBytes("activation"); got != int(activationLen) {
		t.Fatalf("activation bytes transferred=%d, want one body (%d)", got, activationLen)
	}
	if got := hook.objectBytes("timeline"); got != int(timelineLen) {
		t.Fatalf("timeline bytes transferred=%d, want one body (%d)", got, timelineLen)
	}
	t.Logf("per authorization before: activation=%d timeline=%d bytes; after: header=%d bytes + strlen, bodies once per header (%d/%d)",
		activationLen, timelineLen, headerLen, activationLen, timelineLen)
	stats := repository.ControlReadCacheStats()
	if stats.Activation != (controlplane.ControlReadCacheObjectStats{Hits: authorizations - 1, Misses: 1}) ||
		stats.Timeline != (controlplane.ControlReadCacheObjectStats{Hits: authorizations - 1, Misses: 1}) {
		t.Fatalf("cache stats=%+v, want %d hits and 1 miss per object", stats, authorizations-1)
	}

	hook.reset()
	for i := 0; i < 3; i++ {
		if _, err := repository.LoadActivation(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.ReadFrozenSchedule(ctx, fixture.queryGroup, 120); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.NextSlotAfter(ctx, fixture.queryGroup, 120); err != nil {
			t.Fatal(err)
		}
		if _, _, err := runtime.ReadScheduleRetirement(ctx, fixture.queryGroup); err != nil {
			t.Fatal(err)
		}
	}
	if got := hook.bodyReads("activation") + hook.bodyReads("timeline"); got != 0 {
		t.Fatalf("warm Leader and Schedule reads transferred %d bodies, want 0", got)
	}
	if got := hook.count("get", "header"); got != 12 {
		t.Fatalf("header probes=%d, want one per read (12)", got)
	}
}

func TestControlReadCacheRefreshesAfterCutoverAndKeepsHistoricalSemantics(t *testing.T) {
	fixture := newControlReadCacheFixture(t, "cutover")
	ctx := context.Background()
	repository, runtime := fixture.coldRepository(t)
	before, err := repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ReadFrozenSchedule(ctx, fixture.queryGroup, 120); err != nil {
		t.Fatal(err)
	}
	fixture.hook.reset()

	// The Leader repository performs the publication cutover; the cold worker
	// repository must observe it through the header alone.
	*fixture.clock = time.Unix(180, 0)
	secondCatalog := catalogWithSchedule(t, validCatalog(t, 81), 60, 0)
	second, _, err := fixture.repository.PublishCatalog(ctx, secondCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.reconciler.Ensure(ctx, second.Publication); err != nil {
		t.Fatal(err)
	}
	fixture.hook.reset()
	after, err := repository.LoadActivation(ctx)
	if err != nil || after.RecordRevision != before.RecordRevision+1 || after.Current != second.Publication {
		t.Fatalf("activation after cutover=(%+v, %v), want revision %d current %+v", after, err, before.RecordRevision+1, second.Publication)
	}
	schedule, err := runtime.ReadFrozenSchedule(ctx, fixture.queryGroup, 180)
	if err != nil || schedule.Segment.Publication.SnapshotRevision != second.Publication.SnapshotRevision {
		t.Fatalf("Schedule after cutover=(%+v, %v)", schedule, err)
	}
	historical, err := repository.LoadActivations(ctx, fixture.activationRequest())
	if err != nil || len(historical.Facts) != 1 || historical.Facts[0].Selection != execution.ActivationNone {
		t.Fatalf("historical authorization after cutover=(%+v, %v), want NONE from the closed Segment", historical, err)
	}
	if got := fixture.hook.bodyReads("activation"); got != 1 {
		t.Fatalf("activation body reads across the header change=%d, want 1", got)
	}
	if got := fixture.hook.bodyReads("timeline"); got != 1 {
		t.Fatalf("timeline body reads across the header change=%d, want 1", got)
	}
	// The activation read observed the new header first and replaced the
	// cached version, so the timeline read that followed is a cold miss.
	stats := repository.ControlReadCacheStats()
	if stats.Activation != (controlplane.ControlReadCacheObjectStats{Hits: 1, Misses: 1, Refreshes: 1}) ||
		stats.Timeline != (controlplane.ControlReadCacheObjectStats{Hits: 1, Misses: 2}) {
		t.Fatalf("cache stats=%+v, want activation {1,1,1} and timeline {1,2,0}", stats)
	}
}

func TestControlReadCacheMissingActivationSurfacesUnavailable(t *testing.T) {
	fixture := newControlReadCacheFixture(t, "missing-activation")
	ctx := context.Background()
	repository, runtime := fixture.coldRepository(t)
	if _, err := repository.LoadActivations(ctx, fixture.activationRequest()); err != nil {
		t.Fatal(err)
	}
	activationKey := fixture.prefix + ":activation"
	payload, err := fixture.client.Get(ctx, activationKey).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Del(ctx, activationKey).Err(); err != nil {
		t.Fatal(err)
	}
	fixture.hook.reset()
	if _, err := repository.LoadActivation(ctx); !errors.Is(err, controlplane.ErrActivationUnavailable) {
		t.Fatalf("cached activation hid deletion: %v", err)
	}
	if _, err := repository.LoadActivations(ctx, fixture.activationRequest()); !errors.Is(err, controlplane.ErrActivationUnavailable) {
		t.Fatalf("cached activation hid deletion for authorization: %v", err)
	}
	if got := fixture.hook.bodyReads("activation"); got != 0 {
		t.Fatalf("missing activation was detected only after %d body reads, want 0", got)
	}
	if err := fixture.client.Set(ctx, activationKey, payload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.LoadActivation(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixture.hook.bodyReads("activation"); got != 1 {
		t.Fatalf("restored activation body reads=%d, want 1 after the cache was cleared", got)
	}

	// Timelines are validated by the header alone: with an unchanged header a
	// warm reader keeps serving the verified copy while a cold reader sees the
	// persisted state. Every persisted timeline write advances the header.
	timelineKey := fixture.prefix + ":schedule_timeline:" + string(fixture.queryGroup)
	if err := fixture.client.Del(ctx, timelineKey).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ReadFrozenSchedule(ctx, fixture.queryGroup, 120); err != nil {
		t.Fatalf("warm timeline under an unchanged header: %v", err)
	}
	_, coldRuntime := fixture.coldRepository(t)
	if _, err := coldRuntime.ReadFrozenSchedule(ctx, fixture.queryGroup, 120); !errors.Is(err, controlplane.ErrScheduleUnavailable) {
		t.Fatalf("cold timeline read=%v, want ErrScheduleUnavailable", err)
	}

	if err := fixture.client.Del(ctx, fixture.prefix+":activation_header", activationKey).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.LoadActivation(ctx); !errors.Is(err, controlplane.ErrActivationUnavailable) {
		t.Fatalf("missing header and activation=%v, want ErrActivationUnavailable", err)
	}
}

// A Segment that names its content is frozen from the catalog objects: the
// whole Snapshot body is never read, the object and the Plan's output context
// are read from the network once and served from the process cache after.
func TestControlReadCacheFreezesFromCatalogObjectsWithoutTheSnapshotBody(t *testing.T) {
	fixture := newControlReadCacheFixture(t, "snapshot-objects")
	ctx := context.Background()
	repository, runtime := fixture.coldRepository(t)
	if err := repository.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	fixture.hook.reset()
	freeze := func() (execution.FrozenSlotContractFact, error) {
		return runtime.FreezeSlotContract(ctx, fixture.request)
	}
	const runs = 5
	for i := 0; i < runs; i++ {
		fact, err := freeze()
		if err != nil || fact.Contract != fixture.contract.Contract {
			t.Fatalf("FreezeSlotContract #%d = (%+v, %v)", i, fact.Contract, err)
		}
	}
	hook := fixture.hook
	if got := hook.bodyReads("snapshot"); got != 0 {
		t.Fatalf("snapshot body reads=%d, want none when the Segment names its content", got)
	}
	if got := hook.bodyReads("object"); got != 1 {
		t.Fatalf("object reads=%d, want 1 for %d runs", got, runs)
	}
	if got := hook.bodyReads("context"); got != 1 {
		t.Fatalf("output context reads=%d, want 1 for %d runs", got, runs)
	}
	if transferred := hook.objectBytes("object") + hook.objectBytes("context"); transferred == 0 {
		t.Fatal("no object bytes were read")
	}
	t.Logf("object+context=%d bytes read once for %d runs", hook.objectBytes("object")+hook.objectBytes("context"), runs)
}

// A Segment is frozen from the catalog objects alone. No snapshot body is
// written, none is read, and when the objects are gone the freeze reports
// the objects as unavailable, the class a Worker retries, instead of the
// body it once fell back to.
func TestControlReadCacheNeverFallsBackToTheSnapshotBody(t *testing.T) {
	fixture := newControlReadCacheFixture(t, "snapshot-revision")
	ctx := context.Background()
	_, runtime := fixture.coldRepository(t)
	snapshotKey := fixture.prefix + ":snapshot:" + string(fixture.snapshot.Publication.SnapshotRevision)
	if exists := fixture.client.Exists(ctx, snapshotKey).Val(); exists != 0 {
		t.Fatal("a snapshot body was written")
	}
	fixture.hook.reset()
	freeze := func() (execution.FrozenSlotContractFact, error) {
		return runtime.FreezeSlotContract(ctx, fixture.request)
	}
	const runs = 5
	for i := 0; i < runs; i++ {
		fact, err := freeze()
		if err != nil || fact.Contract != fixture.contract.Contract {
			t.Fatalf("FreezeSlotContract #%d = (%+v, %v)", i, fact.Contract, err)
		}
	}
	hook := fixture.hook
	if got := hook.bodyReads("snapshot"); got != 0 {
		t.Fatalf("snapshot body reads=%d, want none for %d runs", got, runs)
	}
	if got := hook.bodyReads("object"); got == 0 {
		t.Fatal("no catalog object was read")
	}

	// A body planted under the revision key is not read either.
	if err := fixture.client.Set(ctx, snapshotKey, "{", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	hook.reset()
	if _, err := freeze(); err != nil {
		t.Fatalf("planted body: %v", err)
	}
	if got := hook.bodyReads("snapshot"); got != 0 {
		t.Fatalf("snapshot body reads=%d with a body planted, want none", got)
	}

	// Aged-out objects surface as unavailable objects, never as a body read.
	for _, pattern := range []string{"*:qgobj:*", "*:outctx:*"} {
		keys, err := fixture.client.Keys(ctx, fixture.prefix+pattern).Result()
		if err != nil || len(keys) == 0 {
			t.Fatalf("catalog objects %s = %v (%v), want some to age out", pattern, keys, err)
		}
		if err := fixture.client.Del(ctx, keys...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	hook.reset()
	_, err := freeze()
	var classified *controlplane.FreezeSlotContractError
	if !errors.As(err, &classified) || classified.Class != controlplane.FreezeSlotFailurePlanMaterialize || !errors.Is(err, controlplane.ErrCatalogObjectUnavailable) {
		t.Fatalf("deleted objects error=%v, want plan_materialize ErrCatalogObjectUnavailable", err)
	}
	if got := hook.bodyReads("snapshot"); got != 0 {
		t.Fatalf("snapshot body reads=%d after the objects were deleted, want none", got)
	}
}
