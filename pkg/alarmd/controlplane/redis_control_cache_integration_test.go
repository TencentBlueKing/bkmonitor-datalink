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
	mu     sync.Mutex
	counts map[string]int
	bytes  map[string]int
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

func (*controlReadCountingHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (*controlReadCountingHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func (hook *controlReadCountingHook) reset() {
	hook.mu.Lock()
	hook.counts = map[string]int{}
	hook.bytes = map[string]int{}
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
		scoped, done := controlplane.WithSnapshotReadScope(ctx)
		defer done()
		return runtime.FreezeSlotContract(scoped, fixture.request)
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
	snapshotKey := fixture.prefix + ":snapshot:" + string(fixture.snapshot.Publication.SnapshotRevision)
	body, err := fixture.client.Get(ctx, snapshotKey).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// One Query Group's object is most of a one-Query-Group Snapshot; the
	// saving is per Query Group of the population, not visible here.
	if transferred := hook.objectBytes("object") + hook.objectBytes("context"); transferred == 0 {
		t.Fatal("no object bytes were read")
	}
	t.Logf("snapshot body=%d bytes never read; object+context=%d bytes read once for %d runs", len(body), hook.objectBytes("object")+hook.objectBytes("context"), runs)
}

// A Segment whose objects are gone, or that predates the catalog, is frozen
// the way it always was: from the Snapshot body, read once per revision and
// probed cheaply after.
func TestControlReadCacheReadsSnapshotBodyOncePerRevision(t *testing.T) {
	fixture := newControlReadCacheFixture(t, "snapshot-revision")
	ctx := context.Background()
	repository, runtime := fixture.coldRepository(t)
	snapshotKey := fixture.prefix + ":snapshot:" + string(fixture.snapshot.Publication.SnapshotRevision)
	epochKey := fixture.prefix + ":snapshot_epoch:" + string(fixture.snapshot.Publication.SnapshotRevision)
	body, err := fixture.client.Get(ctx, snapshotKey).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	epochText, err := fixture.client.Get(ctx, epochKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	// Age the catalog objects out so the Segment falls back to the body.
	for _, pattern := range []string{"*:qgobj:*", "*:outctx:*"} {
		keys, err := fixture.client.Keys(ctx, fixture.prefix+pattern).Result()
		if err != nil || len(keys) == 0 {
			t.Fatalf("catalog objects %s = %v (%v), want some to age out", pattern, keys, err)
		}
		if err := fixture.client.Del(ctx, keys...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	// The one complete read is an MGET of body and epoch value together.
	coldReadBytes := len(body) + len(epochText)
	fixture.hook.reset()
	freeze := func() (execution.FrozenSlotContractFact, error) {
		scoped, done := controlplane.WithSnapshotReadScope(ctx)
		defer done()
		return runtime.FreezeSlotContract(scoped, fixture.request)
	}
	const runs = 5
	for i := 0; i < runs; i++ {
		fact, err := freeze()
		if err != nil || fact.Contract != fixture.contract.Contract {
			t.Fatalf("FreezeSlotContract #%d = (%+v, %v)", i, fact.Contract, err)
		}
	}
	hook := fixture.hook
	if got := hook.bodyReads("snapshot"); got != 1 {
		t.Fatalf("snapshot body reads=%d, want 1 for %d separate RunOne scopes", got, runs)
	}
	if got := hook.objectBytes("snapshot"); got != coldReadBytes {
		t.Fatalf("snapshot bytes transferred=%d, want one body (%d) instead of %d", got, coldReadBytes, runs*coldReadBytes)
	}
	if got := hook.count("get", "epoch"); got != runs-1 {
		t.Fatalf("epoch probes=%d, want one per warm run (%d)", got, runs-1)
	}
	if got := hook.count("strlen", "snapshot"); got != runs-1 {
		t.Fatalf("body length probes=%d, want one per warm run (%d)", got, runs-1)
	}
	if got := hook.count("get", "publication"); got != runs {
		t.Fatalf("publication occurrence reads=%d, want one per run (%d)", got, runs)
	}
	t.Logf("snapshot body=%d bytes: before %d bytes per %d runs, after %d bytes plus %d small probes",
		len(body), runs*len(body), runs, hook.objectBytes("snapshot"), hook.count("get", "epoch")+hook.count("strlen", "snapshot"))
	if stats := repository.ControlReadCacheStats().Snapshot; stats != (controlplane.ControlReadCacheObjectStats{Hits: runs - 1, Misses: 1}) {
		t.Fatalf("snapshot cache stats=%+v, want %d hits and 1 miss", stats, runs-1)
	}

	// A missing body must surface exactly as before: the length probe fails
	// and the complete read path reports the unavailable Snapshot.
	if err := fixture.client.Del(ctx, snapshotKey).Err(); err != nil {
		t.Fatal(err)
	}
	hook.reset()
	_, err = freeze()
	var classified *controlplane.FreezeSlotContractError
	if !errors.As(err, &classified) || classified.Class != controlplane.FreezeSlotFailureSnapshotRead || !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("deleted body error=%v, want snapshot_read ErrSnapshotUnavailable", err)
	}
	// The fallback MGET returns only the epoch value next to the missing body.
	if got := hook.objectBytes("snapshot"); got != len(epochText) {
		t.Fatalf("deleted body still transferred %d bytes, want only the %d byte epoch", got, len(epochText))
	}
	if err := fixture.client.Set(ctx, snapshotKey, body, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	hook.reset()
	if _, err := freeze(); err != nil {
		t.Fatalf("restored body: %v", err)
	}
	if got := hook.objectBytes("snapshot"); got != 0 {
		t.Fatalf("restored identical body was transferred again (%d bytes)", got)
	}

	// A body whose length changed is re-read and classified as corrupt.
	if err := fixture.client.Set(ctx, snapshotKey, "{", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	_, err = freeze()
	var corrupt *controlplane.PersistedSnapshotCorruptError
	if !errors.As(err, &corrupt) {
		t.Fatalf("changed body error=%v, want persisted corruption", err)
	}
	if err := fixture.client.Set(ctx, snapshotKey, body, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	// A changed publication epoch forces one complete verified read, after
	// which the revision cache serves again under the new epoch.
	if err := fixture.client.Set(ctx, epochKey, "999", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	hook.reset()
	if _, err := freeze(); err != nil {
		t.Fatalf("changed epoch: %v", err)
	}
	if got := hook.bodyReads("snapshot"); got != 1 {
		t.Fatalf("changed epoch body reads=%d, want exactly one re-verification", got)
	}
	hook.reset()
	if _, err := freeze(); err != nil {
		t.Fatal(err)
	}
	if got := hook.bodyReads("snapshot"); got != 0 {
		t.Fatalf("re-verified epoch still re-reads the body (%d)", got)
	}
	stats := repository.ControlReadCacheStats().Snapshot
	if stats.Refreshes != 3 {
		t.Fatalf("snapshot cache stats=%+v, want 3 refreshes (deleted body, changed body, changed epoch)", stats)
	}
}
