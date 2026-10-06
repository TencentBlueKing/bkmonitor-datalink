package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// rebuildFixture is a deployment with one activation in force: a published
// catalog, its first activation, and the leader's repository that read it.
type rebuildFixture struct {
	client      *redis.Client
	prefix      string
	repository  *controlplane.RedisCatalogRepository
	reconciler  *controlplane.ScheduleActivationReconciler
	publication controlplane.SnapshotPublicationRef
	body        []byte
	header      string
}

func newRebuildFixture(t *testing.T, name string) *rebuildFixture {
	t.Helper()
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:rebuild-" + name
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := repository.PublishCatalog(ctx, validCatalog(t, 80))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
		t.Fatal(err)
	}
	body, err := client.Get(ctx, prefix+":activation").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	header, err := client.Get(ctx, prefix+":activation_header").Result()
	if err != nil {
		t.Fatal(err)
	}
	return &rebuildFixture{client: client, prefix: prefix, repository: repository, reconciler: reconciler,
		publication: snapshot.Publication, body: body, header: header}
}

func (f *rebuildFixture) loseBody(t *testing.T) {
	t.Helper()
	if err := f.client.Del(context.Background(), f.prefix+":activation").Err(); err != nil {
		t.Fatal(err)
	}
}

// restarted is the same deployment seen by a leader that never read the
// activation: no last-good copy.
func (f *rebuildFixture) restarted(t *testing.T) (*controlplane.RedisCatalogRepository, *controlplane.ScheduleActivationReconciler) {
	t.Helper()
	repository, err := controlplane.NewRedisCatalogRepository(f.client, f.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(120, 0) })
	if err != nil {
		t.Fatal(err)
	}
	return repository, reconciler
}

func (f *rebuildFixture) timelineKeys(t *testing.T) []string {
	t.Helper()
	keys, err := f.client.Keys(context.Background(), f.prefix+":schedule_timeline:*").Result()
	if err != nil || len(keys) == 0 {
		t.Fatalf("timeline keys %v %v", keys, err)
	}
	sort.Strings(keys)
	return keys
}

func (f *rebuildFixture) bodyGone(t *testing.T) bool {
	t.Helper()
	n, err := f.client.Exists(context.Background(), f.prefix+":activation").Result()
	if err != nil {
		t.Fatal(err)
	}
	return n == 0
}

func decodedActivation(t *testing.T, payload []byte) controlplane.ActivationState {
	t.Helper()
	var state controlplane.ActivationState
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatal(err)
	}
	sort.Slice(state.Plans, func(i, j int) bool {
		return fmt.Sprint(state.Plans[i].Fact.Key()) < fmt.Sprint(state.Plans[j].Fact.Key())
	})
	return state
}

// A body lost under its header is a read the fleet sees as unavailable and a
// leader sees as rebuildable. Before this the leader took the first-activation
// path, which refuses any header, every round, for good.
func TestALostBodyIsNamedAndTheLeaderWritesItBackByteForByte(t *testing.T) {
	f := newRebuildFixture(t, "last-good")
	ctx := context.Background()
	f.loseBody(t)
	if _, err := f.repository.LoadActivation(ctx); !errors.Is(err, controlplane.ErrActivationBodyMissing) ||
		!errors.Is(err, controlplane.ErrActivationUnavailable) {
		t.Fatalf("load after the loss = %v, want body missing, which is also unavailable", err)
	}
	state, err := f.reconciler.Ensure(ctx, f.publication)
	if err != nil {
		t.Fatalf("ensure after the loss: %v", err)
	}
	restored, err := f.client.Get(ctx, f.prefix+":activation").Bytes()
	if err != nil || string(restored) != string(f.body) {
		t.Fatalf("restored body differs from the lost one (err %v)", err)
	}
	if header, _ := f.client.Get(ctx, f.prefix+":activation_header").Result(); header != f.header {
		t.Fatalf("header moved: %q, want %q", header, f.header)
	}
	if state.Current != f.publication {
		t.Fatalf("ensure answered %+v", state.Current)
	}
	if counts := f.repository.ActivationRebuildCounts(); counts[controlplane.ActivationRebuilt] != 1 {
		t.Fatalf("counts = %v, want one rebuilt", counts)
	}
}

// A leader that never read the activation - restarted after the loss - has
// no copy and recovers the body from the open Segments. The Plans are the
// ones that were lost.
func TestARestartedLeaderRecoversTheBodyFromTheSegments(t *testing.T) {
	f := newRebuildFixture(t, "segments")
	ctx := context.Background()
	f.loseBody(t)
	repository, reconciler := f.restarted(t)
	if _, err := reconciler.Ensure(ctx, f.publication); err != nil {
		t.Fatalf("ensure after the loss: %v", err)
	}
	restored, err := f.client.Get(ctx, f.prefix+":activation").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := decodedActivation(t, restored), decodedActivation(t, f.body); !reflect.DeepEqual(got, want) {
		t.Fatalf("recovered activation differs:\n got %+v\nwant %+v", got, want)
	}
	if counts := repository.ActivationRebuildCounts(); counts[controlplane.ActivationRebuiltDrainingUnknown] != 1 {
		t.Fatalf("counts = %v, want one rebuilt_draining_unknown", counts)
	}
}

// Every refusal is named, writes nothing, and leaves the body missing.
func TestARebuildThatCannotBeProvenWritesNothing(t *testing.T) {
	t.Run("a header naming a pending publication", func(t *testing.T) {
		f := newRebuildFixture(t, "pending")
		ctx := context.Background()
		f.loseBody(t)
		pendingHeader := strings.TrimSuffix(f.header, "-") + strings.Split(f.header, "|")[1]
		if err := f.client.Set(ctx, f.prefix+":activation_header", pendingHeader, 0).Err(); err != nil {
			t.Fatal(err)
		}
		_, err := f.reconciler.Ensure(ctx, f.publication)
		if !errors.Is(err, controlplane.ErrActivationRebuildRefused) || !f.bodyGone(t) ||
			f.repository.ActivationRebuildCounts()[controlplane.ActivationRebuildHeaderPending] != 1 {
			t.Fatalf("err %v counts %v", err, f.repository.ActivationRebuildCounts())
		}
	})
	t.Run("a Query Group of the publication without its timeline", func(t *testing.T) {
		f := newRebuildFixture(t, "timeline-missing")
		ctx := context.Background()
		f.loseBody(t)
		if err := f.client.Del(ctx, f.timelineKeys(t)[0]).Err(); err != nil {
			t.Fatal(err)
		}
		_, err := f.reconciler.Ensure(ctx, f.publication)
		if !errors.Is(err, controlplane.ErrActivationRebuildRefused) || !f.bodyGone(t) ||
			f.repository.ActivationRebuildCounts()[controlplane.ActivationRebuildTimelineMissing] != 1 {
			t.Fatalf("err %v counts %v", err, f.repository.ActivationRebuildCounts())
		}
	})
	t.Run("a remembered body the Segments disagree with", func(t *testing.T) {
		f := newRebuildFixture(t, "coverage")
		ctx := context.Background()
		f.loseBody(t)
		key := f.timelineKeys(t)[0]
		raw, err := f.client.Get(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		// One Plan's record changed on the Segment only: the remembered body
		// no longer matches what the Segments carry.
		tampered := strings.Replace(raw, `"RequiredFullSlots":`, `"RequiredFullSlots":9`, 1)
		if tampered == raw {
			t.Fatal("setup: no record to tamper with")
		}
		if err := f.client.Set(ctx, key, tampered, time.Hour).Err(); err != nil {
			t.Fatal(err)
		}
		_, err = f.reconciler.Ensure(ctx, f.publication)
		if !errors.Is(err, controlplane.ErrActivationRebuildRefused) || !f.bodyGone(t) ||
			f.repository.ActivationRebuildCounts()[controlplane.ActivationRebuildCoverageInvalid] != 1 {
			t.Fatalf("err %v counts %v", err, f.repository.ActivationRebuildCounts())
		}
	})
}

// Another writer moving a timeline between the read and the write wins: the
// rebuild writes nothing and says so.
func TestATimelineMovedDuringTheRebuildIsAConflict(t *testing.T) {
	f := newRebuildFixture(t, "conflict")
	ctx := context.Background()
	f.loseBody(t)
	key := f.timelineKeys(t)[0]
	raw, err := f.client.Get(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	other := redis.NewClient(&redis.Options{Addr: f.client.Options().Addr})
	defer other.Close()
	f.client.AddHook(moveBeforeRebuild{client: other, key: key, value: raw + " "})
	outcome, err := f.repository.RebuildActivationBody(ctx)
	if err != nil || outcome != controlplane.ActivationRebuildConflict || !f.bodyGone(t) {
		t.Fatalf("outcome %s err %v body gone %v", outcome, err, f.bodyGone(t))
	}
}

type moveBeforeRebuild struct {
	client     *redis.Client
	key, value string
}

func (hook moveBeforeRebuild) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if args := cmd.Args(); len(args) > 1 && strings.EqualFold(cmd.Name(), "eval") {
		if script, ok := args[1].(string); ok && strings.Contains(script, "redis.call('EXISTS', KEYS[2]) == 1") {
			if err := hook.client.Set(ctx, hook.key, hook.value, time.Hour).Err(); err != nil {
				return ctx, err
			}
		}
	}
	return ctx, nil
}
func (moveBeforeRebuild) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (moveBeforeRebuild) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (moveBeforeRebuild) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// A header that is gone too is the first-activation case it always was: the
// read is unavailable and not a body missing.
func TestAMissingHeaderIsNotABodyMissing(t *testing.T) {
	f := newRebuildFixture(t, "header-gone")
	ctx := context.Background()
	if err := f.client.Del(ctx, f.prefix+":activation", f.prefix+":activation_header").Err(); err != nil {
		t.Fatal(err)
	}
	_, err := f.repository.LoadActivation(ctx)
	if !errors.Is(err, controlplane.ErrActivationUnavailable) || errors.Is(err, controlplane.ErrActivationBodyMissing) {
		t.Fatalf("load = %v, want unavailable and not body missing", err)
	}
	if outcome, err := f.repository.RebuildActivationBody(ctx); err != nil || outcome != controlplane.ActivationRebuildNotNeeded {
		t.Fatalf("rebuild without a header = %s %v", outcome, err)
	}
}

// A cutover that moved a strategy to another Query Group leaves the old one
// draining: not in the publication, still executing to its boundary. The
// copy the leader last read carries it and is written back as it was; a
// leader without that copy cannot know it from the Segments, loses only that
// entry, and says so.
func TestDrainingSurvivesWithTheLastReadCopyAndIsNamedWithoutIt(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:rebuild-draining"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	clock := []time.Time{time.Unix(60, 0), time.Unix(90, 0)}
	calls := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time {
		at := clock[min(calls, len(clock)-1)]
		calls++
		return at
	})
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := repository.PublishCatalog(ctx, catalogWithQueryTable(t, "system.cpu"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, old.Publication); err != nil {
		t.Fatal(err)
	}
	next, _, err := repository.PublishCatalog(ctx, catalogWithQueryTable(t, "system.mem"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconciler.Ensure(ctx, next.Publication)
	if err != nil || len(state.Draining) != 1 {
		t.Fatalf("setup: draining %+v err %v", state.Draining, err)
	}
	body, err := client.Get(ctx, prefix+":activation").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	f := &rebuildFixture{client: client, prefix: prefix, repository: repository, reconciler: reconciler,
		publication: next.Publication, body: body}

	f.loseBody(t)
	if _, err := reconciler.Ensure(ctx, next.Publication); err != nil {
		t.Fatal(err)
	}
	if restored, _ := client.Get(ctx, prefix+":activation").Bytes(); string(restored) != string(body) {
		t.Fatal("the last-read copy was not written back as it was")
	}

	f.loseBody(t)
	restarted, restartedReconciler := f.restarted(t)
	if _, err := restartedReconciler.Ensure(ctx, next.Publication); err != nil {
		t.Fatal(err)
	}
	restored, err := client.Get(ctx, prefix+":activation").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got, want := decodedActivation(t, restored), decodedActivation(t, body)
	if len(got.Draining) != 0 {
		t.Fatalf("draining %+v, want none recovered without the copy", got.Draining)
	}
	want.Draining = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("apart from Draining the recovered activation differs:\n got %+v\nwant %+v", got, want)
	}
	if counts := restarted.ActivationRebuildCounts(); counts[controlplane.ActivationRebuiltDrainingUnknown] != 1 {
		t.Fatalf("counts = %v, want the missing Draining named", counts)
	}
}

// A Query Group the publication brings back before its retirement drained is
// held: in the publication, retired on its timeline, in Draining. A leader
// without the last-read copy recovers it from that timeline, so the body it
// rebuilds is the one that was lost.
func TestAHeldQueryGroupIsRecoveredIntoDrainingFromItsTimeline(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:rebuild-held"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	both := twoQueryGroupCatalog(t)
	returning, staying := both.QueryGroups[0], both.QueryGroups[1]
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		returning.Identity: {Status: execution.ProgressMissing}, staying.Identity: {Status: execution.ProgressMissing},
	}}
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress,
		func() time.Time { return time.Unix(90, 0) })
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := repository.PublishCatalog(ctx, both)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Ensure(ctx, first.Publication); err != nil {
		t.Fatal(err)
	}
	onlyStaying := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{staying}}
	onlyStaying.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", onlyStaying.QueryGroups))
	second, _, err := repository.PublishCatalog(ctx, onlyStaying)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, second.Publication); err != nil {
		t.Fatal(err)
	}
	// Not drained: the cursor is short of the retired boundary.
	progress.byGroup[returning.Identity] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: returning.Identity}, NextSlot: 70, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull,
	}}
	third, _, err := repository.PublishCatalog(ctx, both)
	if err != nil {
		t.Fatal(err)
	}
	held, err := reconciler.Ensure(ctx, third.Publication)
	if err != nil || len(held.Draining) != 1 || held.Draining[0].QueryGroup != returning.Identity {
		t.Fatalf("setup: held = %+v, %v", held.Draining, err)
	}
	body, err := client.Get(ctx, prefix+":activation").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	f := &rebuildFixture{client: client, prefix: prefix, publication: third.Publication, body: body}
	f.loseBody(t)
	// A restarted leader: no copy read, and the Progress reader a production
	// leader always has.
	restarted, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	restartedReconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(restarted, compiler, semantics, progress,
		func() time.Time { return time.Unix(95, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restartedReconciler.Ensure(ctx, third.Publication); err != nil {
		t.Fatal(err)
	}
	restored, err := client.Get(ctx, prefix+":activation").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := decodedActivation(t, restored), decodedActivation(t, body); !reflect.DeepEqual(got, want) {
		t.Fatalf("recovered activation differs:\n got %+v\nwant %+v", got, want)
	}
	if counts := restarted.ActivationRebuildCounts(); counts[controlplane.ActivationRebuiltDrainingUnknown] != 1 {
		t.Fatalf("counts = %v", counts)
	}
}
