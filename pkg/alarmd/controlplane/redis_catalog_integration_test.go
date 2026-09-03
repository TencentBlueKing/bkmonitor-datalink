package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type scanCountingHook struct {
	count atomic.Int64
}

func (hook *scanCountingHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if strings.EqualFold(cmd.Name(), "scan") {
		hook.count.Add(1)
	}
	return ctx, nil
}

func (*scanCountingHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (*scanCountingHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (*scanCountingHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

type serialActivationCASHook struct {
	evals      atomic.Int64
	completed  atomic.Bool
	firstDone  chan struct{}
	afterFirst func() error
}

func (hook *serialActivationCASHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() == "eval" && hook.evals.Add(1) == 2 {
		select {
		case <-hook.firstDone:
		case <-ctx.Done():
			return ctx, ctx.Err()
		}
	}
	return ctx, nil
}

func (hook *serialActivationCASHook) AfterProcess(_ context.Context, cmd redis.Cmder) error {
	if cmd.Name() != "eval" || !hook.completed.CompareAndSwap(false, true) {
		return nil
	}
	err := hook.afterFirst()
	close(hook.firstDone)
	return err
}

func (*serialActivationCASHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (*serialActivationCASHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

type beforeEvalHook struct {
	once sync.Once
	run  func() error
}

func (hook *beforeEvalHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() != "eval" {
		return ctx, nil
	}
	var err error
	hook.once.Do(func() { err = hook.run() })
	return ctx, err
}

func (*beforeEvalHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (*beforeEvalHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (*beforeEvalHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

func TestRedisCatalogRepositoryPublishesImmutableContentAddressedSnapshot(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	publisher, err := controlplane.NewSnapshotPublisher(repository)
	if err != nil {
		t.Fatal(err)
	}

	first, created, err := publisher.Publish(context.Background(), catalog)
	if err != nil || !created {
		t.Fatalf("first publish=(%#v, %t, %v)", first, created, err)
	}
	catalog.ObservationID = strings.Repeat("a", 64)
	catalog.Dispositions = append(catalog.Dispositions, controlplane.ObjectDisposition{
		SourceID: "1001", Scope: "PLAN", Disposition: controlplane.DispositionSourceIncomplete, Reason: "AUDIT_ONLY",
	})
	second, created, err := publisher.Publish(context.Background(), catalog)
	if err != nil || created || second.Publication != first.Publication {
		t.Fatalf("idempotent publish=(%#v, %t, %v), first=%#v", second, created, err, first)
	}
	loaded, err := repository.LoadSnapshot(context.Background(), catalog.SnapshotRevision)
	if err != nil || loaded.Publication != first.Publication || len(loaded.QueryGroups) != 1 {
		t.Fatalf("loaded snapshot=(%#v, %v)", loaded, err)
	}
	group, err := repository.LoadQueryGroup(context.Background(), catalog.SnapshotRevision, catalog.QueryGroups[0].Identity)
	if err != nil || group.Identity != catalog.QueryGroups[0].Identity {
		t.Fatalf("loaded query group=(%#v, %v)", group, err)
	}
	plan, err := repository.LoadPlan(context.Background(), catalog.SnapshotRevision, catalog.QueryGroups[0].Plans[0].Identity)
	if err != nil || plan.Identity != catalog.QueryGroups[0].Plans[0].Identity {
		t.Fatalf("loaded plan=(%#v, %v)", plan, err)
	}
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil || audit.ObservationID != catalog.ObservationID || len(audit.Dispositions) != len(catalog.Dispositions) {
		t.Fatalf("latest audit=(%#v, %v)", audit, err)
	}

	newCatalog := validCatalog(t, 81)
	newSnapshot, created, err := publisher.Publish(context.Background(), newCatalog)
	if err != nil || !created || newSnapshot.Publication.PublicationEpoch != first.Publication.PublicationEpoch+1 {
		t.Fatalf("new publish=(%#v, %t, %v), first=%#v", newSnapshot, created, err, first)
	}
	latest, err := repository.LoadLatestPublication(context.Background())
	if err != nil || latest != newSnapshot.Publication {
		t.Fatalf("latest=(%#v, %v), want %#v", latest, err, newSnapshot.Publication)
	}
}

func TestRedisCatalogRepositoryTypesPersistedSnapshotCorruption(t *testing.T) {
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:corrupt-snapshot"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	if _, _, err := repository.PublishCatalog(context.Background(), catalog); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(context.Background(), prefix+":snapshot:"+string(catalog.SnapshotRevision), []byte("{"), time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	_, err = repository.LoadSnapshot(context.Background(), catalog.SnapshotRevision)
	var corrupt *controlplane.PersistedSnapshotCorruptError
	if !errors.As(err, &corrupt) {
		t.Fatalf("LoadSnapshot(corrupt) error=%v", err)
	}
}

func TestRedisCatalogRepositoryRepublishesHistoricalSnapshotWithNewOccurrence(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:republish", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	firstCatalog := validCatalog(t, 80)
	first, _, err := repository.PublishCatalog(ctx, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := repository.PublishCatalog(ctx, validCatalog(t, 81))
	if err != nil {
		t.Fatal(err)
	}
	republished, created, err := repository.PublishCatalog(ctx, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if !created || republished.Publication.SnapshotRevision != first.Publication.SnapshotRevision ||
		republished.Publication.PublicationEpoch <= second.Publication.PublicationEpoch {
		t.Fatalf("republished Snapshot=(%#v, %t), first=%#v second=%#v", republished, created, first, second)
	}
	latest, err := repository.LoadLatestPublication(ctx)
	if err != nil || latest != republished.Publication {
		t.Fatalf("latest publication=(%#v, %v), want %#v", latest, err, republished.Publication)
	}
	for _, publication := range []controlplane.SnapshotPublicationRef{first.Publication, republished.Publication} {
		loaded, err := repository.LoadPublishedSnapshot(ctx, publication)
		if err != nil || loaded.Publication != publication || !reflect.DeepEqual(loaded.QueryGroups, first.QueryGroups) {
			t.Fatalf("loaded occurrence %v=(%#v, %v)", publication, loaded, err)
		}
	}
}

func TestRedisCatalogRepositoryExpiresPublicationOccurrencesIndependently(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:occurrence-ttl"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	firstCatalog := validCatalog(t, 80)
	first, _, err := repository.PublishCatalog(ctx, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.PublishCatalog(ctx, validCatalog(t, 81)); err != nil {
		t.Fatal(err)
	}
	republished, _, err := repository.PublishCatalog(ctx, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	firstKey := prefix + ":publication:" + strconv.FormatUint(first.Publication.PublicationEpoch, 10)
	republishedKey := prefix + ":publication:" + strconv.FormatUint(republished.Publication.PublicationEpoch, 10)
	for _, key := range []string{firstKey, republishedKey} {
		if ttl, err := client.PTTL(ctx, key).Result(); err != nil || ttl <= 0 {
			t.Fatalf("publication occurrence TTL %s=(%s,%v)", key, ttl, err)
		}
	}
	if count, err := client.Exists(ctx, prefix+":publications_by_epoch").Result(); err != nil || count != 0 {
		t.Fatalf("shared occurrence container exists=(%d,%v)", count, err)
	}
	if expired, err := client.PExpire(ctx, firstKey, -time.Millisecond).Result(); err != nil || !expired {
		t.Fatalf("expire first occurrence=(%t,%v)", expired, err)
	}
	if _, err := repository.LoadPublishedSnapshot(ctx, first.Publication); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("expired first occurrence error=%v", err)
	}
	loaded, err := repository.LoadPublishedSnapshot(ctx, republished.Publication)
	if err != nil || loaded.Publication != republished.Publication {
		t.Fatalf("republished occurrence=(%#v,%v)", loaded, err)
	}
}

func TestRedisCatalogRepositoryLoadsHistoricalOccurrenceFromLegacyHashWithoutRenewingTTL(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:legacy-occurrence-hash"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	firstCatalog := validCatalog(t, 80)
	first, _, err := repository.PublishCatalog(ctx, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.PublishCatalog(ctx, validCatalog(t, 81)); err != nil {
		t.Fatal(err)
	}
	republished, _, err := repository.PublishCatalog(ctx, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	firstKey := prefix + ":publication:" + strconv.FormatUint(first.Publication.PublicationEpoch, 10)
	if err := client.Del(ctx, firstKey).Err(); err != nil {
		t.Fatal(err)
	}
	legacyHash := prefix + ":publications_by_epoch"
	if err := client.HSet(ctx, legacyHash, strconv.FormatUint(first.Publication.PublicationEpoch, 10),
		string(first.Publication.SnapshotRevision)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.PExpire(ctx, legacyHash, 10*time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	latestRevisionEpoch, err := client.Get(ctx, prefix+":snapshot_epoch:"+string(first.Publication.SnapshotRevision)).Result()
	if err != nil || latestRevisionEpoch != strconv.FormatUint(republished.Publication.PublicationEpoch, 10) {
		t.Fatalf("latest revision occurrence=(%q,%v), republished=%#v", latestRevisionEpoch, err, republished.Publication)
	}
	before, err := client.PTTL(ctx, legacyHash).Result()
	if err != nil || before <= 0 {
		t.Fatalf("legacy Hash TTL before=(%s,%v)", before, err)
	}
	loaded, err := repository.LoadPublishedSnapshot(ctx, first.Publication)
	if err != nil || loaded.Publication != first.Publication || !reflect.DeepEqual(loaded.QueryGroups, first.QueryGroups) {
		t.Fatalf("legacy historical occurrence=(%#v,%v), first=%#v", loaded, err, first)
	}
	after, err := client.PTTL(ctx, legacyHash).Result()
	if err != nil || after <= 0 || after > before {
		t.Fatalf("legacy Hash TTL after=(%s,%v), before=%s", after, err, before)
	}
	if count, err := client.Exists(ctx, firstKey).Result(); err != nil || count != 0 {
		t.Fatalf("legacy occurrence was migrated=(%d,%v)", count, err)
	}
}

func TestRedisCatalogRepositoryRejectsStalePublicationExpectations(t *testing.T) {
	for _, test := range []struct {
		name      string
		candidate func(*testing.T) controlplane.Catalog
	}{
		{name: "historical revision", candidate: func(t *testing.T) controlplane.Catalog { return validCatalog(t, 80) }},
		{name: "new revision", candidate: func(t *testing.T) controlplane.Catalog { return validCatalog(t, 82) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newControlplaneRedis(t)
			ctx := context.Background()
			repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:stale:"+strings.ReplaceAll(test.name, " ", "-"), time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			first, _, err := repository.PublishCatalog(ctx, validCatalog(t, 80))
			if err != nil {
				t.Fatal(err)
			}
			winner, _, err := repository.PublishCatalog(ctx, validCatalog(t, 81))
			if err != nil {
				t.Fatal(err)
			}
			candidate := test.candidate(t)
			if _, _, err := repository.PublishCatalogIfCurrent(ctx, first.Publication, candidate); !errors.Is(err, controlplane.ErrPublicationConflict) {
				t.Fatalf("stale publish error=%v", err)
			}
			latest, err := repository.LoadLatestPublication(ctx)
			if err != nil || latest != winner.Publication {
				t.Fatalf("latest after stale publish=(%#v, %v), want %#v", latest, err, winner.Publication)
			}
			if candidate.SnapshotRevision != first.Publication.SnapshotRevision {
				if _, err := repository.LoadSnapshot(ctx, candidate.SnapshotRevision); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
					t.Fatalf("stale new candidate was persisted: %v", err)
				}
			}
			next, _, err := repository.PublishCatalog(ctx, validCatalog(t, 83))
			if err != nil || next.Publication.PublicationEpoch != winner.Publication.PublicationEpoch+1 {
				t.Fatalf("next publication=(%#v, %v), winner=%#v", next, err, winner)
			}
		})
	}
}

func TestSnapshotPublisherDoesNotPublishAuditForStaleCandidate(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:publisher-stale", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := controlplane.NewSnapshotPublisher(repository)
	if err != nil {
		t.Fatal(err)
	}
	firstCatalog := validCatalog(t, 80)
	first, _, err := publisher.Publish(ctx, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	winnerCatalog := validCatalog(t, 81)
	winner, _, err := publisher.Publish(ctx, winnerCatalog)
	if err != nil {
		t.Fatal(err)
	}
	stale := validCatalog(t, 82)
	if _, _, err := publisher.PublishIfCurrent(ctx, first.Publication, stale); !errors.Is(err, controlplane.ErrPublicationConflict) {
		t.Fatalf("stale Publisher error=%v", err)
	}
	audit, err := repository.LoadLatestAudit(ctx)
	if err != nil || audit.Publication != winner.Publication || audit.ObservationID != winnerCatalog.ObservationID {
		t.Fatalf("latest audit=(%#v, %v), winner=%#v", audit, err, winner)
	}
}

func TestSnapshotPublisherCannotMoveLatestAuditAfterItsPublicationLosesCurrent(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:audit-fence", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := controlplane.NewSnapshotPublisher(repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := publisher.Publish(ctx, validCatalog(t, 80)); err != nil {
		t.Fatal(err)
	}
	pausedCatalog := validCatalog(t, 81)
	paused, _, err := repository.PublishCatalog(ctx, pausedCatalog)
	if err != nil {
		t.Fatal(err)
	}
	winnerCatalog := validCatalog(t, 82)
	winner, _, err := publisher.Publish(ctx, winnerCatalog)
	if err != nil {
		t.Fatal(err)
	}
	pausedAudit := controlplane.SourceAuditState{
		ObservationID: pausedCatalog.ObservationID,
		Publication:   paused.Publication,
		Dispositions:  append([]controlplane.ObjectDisposition(nil), pausedCatalog.Dispositions...),
	}
	if err := repository.PublishAudit(ctx, pausedAudit); !errors.Is(err, controlplane.ErrPublicationConflict) {
		t.Fatalf("stale latest audit error=%v", err)
	}
	audit, err := repository.LoadLatestAudit(ctx)
	if err != nil || audit.Publication != winner.Publication || audit.ObservationID != winnerCatalog.ObservationID {
		t.Fatalf("latest audit=(%#v,%v), winner=%#v", audit, err, winner)
	}
}

func TestSourceReconcilerReturnsPublicationConflictWinner(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	document := realThresholdDocuments(t)[0]
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source := newRedisStrategySource(t, client)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:source-stale", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	planner := &recordingPlanner{facts: queryFacts(t)}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	initial, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || initial.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publication=(%#v, %v)", initial, err)
	}
	changed := strings.Replace(string(document), `"threshold":80`, `"threshold":82`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", changed, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("changed pending=(%#v, %v)", result, err)
	}
	winnerCatalog := validCatalog(t, 81)
	var winner controlplane.PublishedSnapshot
	winnerPlanner := queryPlannerFunc(func(ctx context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
		if winner.Publication == (controlplane.SnapshotPublicationRef{}) {
			var publishErr error
			winner, _, publishErr = repository.PublishCatalog(ctx, winnerCatalog)
			if publishErr != nil {
				return execution.QueryPlanFacts{}, publishErr
			}
		}
		return queryFacts(t), nil
	})
	result, err := reconciler.Refresh(ctx, source, winnerPlanner)
	if err != nil || result.Status != controlplane.SourceRefreshPublicationConflict || result.Publication != winner.Publication {
		t.Fatalf("publication conflict=(%#v, %v), winner=%#v", result, err, winner)
	}
}

func TestSourceReconcilerPublishesNewOccurrenceWhenHistoricalContentReturns(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	document := realThresholdDocuments(t)[0]
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source := newRedisStrategySource(t, client)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:source-a-b-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	planner := &recordingPlanner{facts: queryFacts(t)}
	publish := func(wantPending bool) controlplane.SourceRefreshResult {
		t.Helper()
		if wantPending {
			pending, err := reconciler.Refresh(ctx, source, planner)
			if err != nil || pending.Status != controlplane.SourceRefreshPendingConfirmation {
				t.Fatalf("pending refresh=(%#v,%v)", pending, err)
			}
		}
		result, err := reconciler.Refresh(ctx, source, planner)
		if err != nil || result.Status != controlplane.SourceRefreshPublished {
			t.Fatalf("published refresh=(%#v,%v)", result, err)
		}
		return result
	}
	first := publish(true)
	changed := strings.Replace(string(document), `"threshold":80`, `"threshold":81`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", changed, 0).Err(); err != nil {
		t.Fatal(err)
	}
	second := publish(true)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	republished := publish(true)
	if republished.Publication.SnapshotRevision != first.Publication.SnapshotRevision ||
		republished.Publication.PublicationEpoch <= second.Publication.PublicationEpoch {
		t.Fatalf("Source A-B-A publications=%#v/%#v/%#v", first.Publication, second.Publication, republished.Publication)
	}
}

func TestLegacyRedisSourceCompilesAndPublishesSharedQueryGroup(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1002,1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	observation, err := controlplane.ObserveStable(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: observation.Strategies, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ObservationID != observation.ObservationID || len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 2 {
		t.Fatalf("compiled catalog=%#v observation=%#v", catalog, observation)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:full", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := controlplane.NewSnapshotPublisher(repository)
	if err != nil {
		t.Fatal(err)
	}
	published, created, err := publisher.Publish(ctx, catalog)
	if err != nil || !created {
		t.Fatalf("publish=(%#v, %t, %v)", published, created, err)
	}
	loaded, err := repository.LoadQueryGroup(ctx, published.Publication.SnapshotRevision, catalog.QueryGroups[0].Identity)
	if err != nil || len(loaded.Plans) != 2 || loaded.QueryPlan.QueryRevision == "" {
		t.Fatalf("loaded query group=(%#v, %v)", loaded, err)
	}
}

func TestSourceReconcilerConfirmsChangeAcrossIndependentRefreshAndRestart(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:reconcile", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	first, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || first.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("first refresh=(%#v, %v)", first, err)
	}
	if _, err := repository.LoadLatestPublication(ctx); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("unconfirmed candidate was published: %v", err)
	}
	changed := strings.Replace(string(documents[1]), `"threshold":90`, `"threshold":91`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", changed, 0).Err(); err != nil {
		t.Fatal(err)
	}

	// Recreate the reconciler: confirmation must come from the persisted prior
	// independent refresh, not process memory or a second read in one call.
	restarted, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	second, err := restarted.Refresh(ctx, source, planner)
	if err != nil || second.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("second refresh=(%#v, %v)", second, err)
	}
	if _, err := repository.LoadLatestPublication(ctx); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("changed second candidate was published: %v", err)
	}
	third, err := restarted.Refresh(ctx, source, planner)
	if err != nil || third.Status != controlplane.SourceRefreshPublished || third.Publication.PublicationEpoch == 0 {
		t.Fatalf("third refresh=(%#v, %v)", third, err)
	}
	fourth, err := restarted.Refresh(ctx, source, planner)
	if err != nil || fourth.Status != controlplane.SourceRefreshUnchanged || fourth.Publication != third.Publication {
		t.Fatalf("unchanged refresh=(%#v, %v), published=%#v", fourth, err, third)
	}
}

func TestSourceReconcilerPublishesOnlyRuntimeExecutablePlans(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := runtimeCompileIsolationDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002,1003]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002", "1003"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:runtime-compile-isolation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompilerWithLimits(t, 2, 1)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("published=(%#v, %v)", published, err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, published.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QueryGroups) != 1 || len(snapshot.QueryGroups[0].Plans) != 1 {
		t.Fatalf("runtime executable Query Groups=%#v", snapshot.QueryGroups)
	}
	plan := snapshot.QueryGroups[0].Plans[0]
	if plan.Identity.StrategyID != "1001" || len(plan.Plan.StrategyIR.Levels) != 1 || plan.Plan.StrategyIR.Levels[0].Definition.LevelID != 1 {
		t.Fatalf("runtime executable Plan=%#v", plan)
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionUnsupported, contract.ReasonLevelBudgetExceeded)
	assertAuditDisposition(t, repository, "1002", controlplane.DispositionUnsupported, contract.ReasonLevelBudgetExceeded)
	assertAuditDisposition(t, repository, "1003", controlplane.DispositionUnsupported, contract.ReasonPlanBudgetExceeded)
	assertNoAcceptedPlanDisposition(t, repository, "1002")
	assertNoAcceptedPlanDisposition(t, repository, "1003")

	activator, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, func() time.Time {
		return time.Unix(180, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := activator.Ensure(ctx, published.Publication)
	if err != nil || len(activation.Plans) != 1 || activation.Plans[0].Fact.Plan.StrategyID != "1001" {
		t.Fatalf("healthy initial activation=(%#v, %v)", activation, err)
	}
}

func TestSourceReconcilerRuntimeCompilerKeepsOnlyInvalidLastGood(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:runtime-last-good", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	normal, stateSemantics := runtimePlanCompiler(t)
	strict, _ := runtimePlanCompilerWithBudgets(t, 1, 16, 4096)
	compiler := &revisionTerminalCompiler{normal: normal, strict: strict,
		invalid: map[string]struct{}{"1800000001": {}}, unsupported: map[string]struct{}{"1800000002": {}}}
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	initial, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || initial.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", initial, err)
	}
	initialSnapshot, err := repository.LoadSnapshot(ctx, initial.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	initialPlans := plansByStrategy(initialSnapshot)

	changed := []json.RawMessage{
		withStrategyUpdateTime(t, documents[0], 1800000001),
		withStrategyUpdateTime(t, documents[1], 1800000002),
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(changed[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("changed pending=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("changed publish=(%#v, %v)", published, err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, published.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	plans := plansByStrategy(snapshot)
	if len(plans) != 1 || plans["1001"].PlanRevision != initialPlans["1001"].PlanRevision {
		t.Fatalf("runtime last-good Plans=%#v initial=%#v", plans, initialPlans)
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionStaleConfig, contract.ReasonPlanInvalid)
	assertAuditDisposition(t, repository, "1002", controlplane.DispositionUnsupported, contract.ReasonPlanBudgetExceeded)
	assertNoAcceptedPlanDisposition(t, repository, "1001")
	assertNoAcceptedPlanDisposition(t, repository, "1002")
}

func TestSourceReconcilerMergesInvalidLevelLastGoodWithoutUnsupportedLevel(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	initialDocument := addThresholdLevels(t, realThresholdDocuments(t)[0], []uint32{2, 3}, []uint32{1, 1})
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(initialDocument), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:runtime-level-last-good", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	normal, stateSemantics := runtimePlanCompiler(t)
	compiler := &revisionTerminalCompiler{normal: normal, strict: normal,
		invalid: map[string]struct{}{}, unsupported: map[string]struct{}{}, mixedLevels: map[string]struct{}{"1800000003": {}}}
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	initial, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || initial.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", initial, err)
	}
	initialSnapshot, err := repository.LoadSnapshot(ctx, initial.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	initialPlan := initialSnapshot.QueryGroups[0].Plans[0]
	initialLevels := levelIRByID(initialPlan.Plan)

	changedDocument := withThresholdForLevel(t, withStrategyUpdateTime(t, initialDocument, 1800000003), 1, 81)
	changedDocument = withThresholdForLevel(t, changedDocument, 2, 82)
	changedCatalog, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{
		SourceID: "1001", Document: changedDocument,
		Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	}}, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	changedLevels := levelIRByID(changedCatalog.QueryGroups[0].Plans[0].Plan)
	if reflect.DeepEqual(changedLevels[2], initialLevels[2]) {
		t.Fatal("changed Level 2 fixture does not differ from last-good")
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(changedDocument), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("changed pending=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("changed publish=(%#v, %v)", published, err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, published.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QueryGroups) != 1 || len(snapshot.QueryGroups[0].Plans) != 1 {
		t.Fatalf("mixed Level snapshot=%#v", snapshot.QueryGroups)
	}
	plan := snapshot.QueryGroups[0].Plans[0]
	levels := levelIRByID(plan.Plan)
	if len(levels) != 2 || !reflect.DeepEqual(levels[1], changedLevels[1]) || !reflect.DeepEqual(levels[2], initialLevels[2]) {
		t.Fatalf("mixed Levels=%#v changed=%#v initial=%#v", levels, changedLevels, initialLevels)
	}
	if _, retainedUnsupported := levels[3]; retainedUnsupported {
		t.Fatalf("unsupported Level was retained: %#v", levels[3])
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionStaleConfig, contract.ReasonLevelInvalid)
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionUnsupported, contract.ReasonAlgorithmUnsupported)
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionAccepted, "")

	activator, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, func() time.Time {
		return time.Unix(180, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := activator.Ensure(ctx, published.Publication); err != nil {
		t.Fatal(err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := runtime.ReadInitialFrozenSchedule(ctx, snapshot.QueryGroups[0].Identity)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := schedule.FirstSlot()
	if !ok {
		t.Fatal("mixed Level schedule has no first Slot")
	}
	if _, err := runtime.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: schedule.Segment.QueryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: first, DuePlans: schedule.DuePlanRefs(first),
	}); err != nil {
		t.Fatalf("mixed Level frozen contract=%v", err)
	}
}

func TestSourceReconcilerExcludesMergedPlanWhenAuthoritativeRecompileFails(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	brokenDocument := addThresholdLevels(t, documents[0], []uint32{2, 3}, []uint32{1, 1})
	healthyDocument := withBusinessScope(t, documents[1], 3, "bkcc__3")
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for id, document := range map[string]json.RawMessage{"1001": brokenDocument, "1002": healthyDocument} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(document), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:runtime-merged-recompile", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	normal, stateSemantics := runtimePlanCompiler(t)
	compiler := &revisionTerminalCompiler{
		normal: normal, strict: normal, invalid: map[string]struct{}{}, unsupported: map[string]struct{}{},
		mixedLevels: map[string]struct{}{"1800000003": {}}, mergedInvalid: map[string]struct{}{"1800000003": {}},
	}
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	initial, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || initial.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", initial, err)
	}
	initialSnapshot, err := repository.LoadSnapshot(ctx, initial.Publication.SnapshotRevision)
	if err != nil || len(initialSnapshot.QueryGroups) != 2 {
		t.Fatalf("initial Query Groups=(%#v, %v)", initialSnapshot.QueryGroups, err)
	}

	changedDocument := withThresholdForLevel(t, withStrategyUpdateTime(t, brokenDocument, 1800000003), 1, 81)
	changedDocument = withThresholdForLevel(t, changedDocument, 2, 82)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(changedDocument), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("changed pending=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("changed publish=(%#v, %v)", published, err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, published.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	plans := plansByStrategy(snapshot)
	if len(snapshot.QueryGroups) != 1 || len(plans) != 1 || plans["1002"].Identity.StrategyID != "1002" {
		t.Fatalf("locally excluded merged Plan snapshot=%#v", snapshot.QueryGroups)
	}
	assertNoAcceptedPlanDisposition(t, repository, "1001")
	assertNoAuditDisposition(t, repository, "1001", controlplane.DispositionStaleConfig)
	assertAuditDispositionExact(t, repository, "1001", "LEVEL", 2,
		controlplane.DispositionConfigRejected, contract.ReasonLevelInvalid)
	assertAuditDispositionExact(t, repository, "1001", "LEVEL", 3,
		controlplane.DispositionUnsupported, contract.ReasonAlgorithmUnsupported)
	assertAuditDispositionExact(t, repository, "1001", "PLAN", 0,
		controlplane.DispositionConfigRejected, contract.ReasonPlanInvalid)
	assertAuditDispositionExact(t, repository, "1002", "PLAN", 0,
		controlplane.DispositionAccepted, "")
}

func TestSourceReconcilerRetainsLastGoodForMissingAndInvalidObjectWhileHealthySiblingAdvances(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:last-good", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	initial, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || initial.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", initial, err)
	}
	initialSnapshot, err := repository.LoadSnapshot(ctx, initial.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	initialPlans := plansByStrategy(initialSnapshot)

	// A strategy whose new cache object loses its authoritative identity fact
	// keeps the persisted last-good Plan. Recreating the reconciler proves that
	// this decision is based on repository state rather than process memory.
	var identityMissing map[string]any
	if err := json.Unmarshal(documents[0], &identityMissing); err != nil {
		t.Fatal(err)
	}
	delete(identityMissing, "space_uid")
	identityMissingPayload, err := json.Marshal(identityMissing)
	if err != nil {
		t.Fatal(err)
	}
	changedSibling := strings.Replace(string(documents[1]), `"threshold":90`, `"threshold":91`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", identityMissingPayload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", changedSibling, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("identity missing pending=(%#v, %v)", result, err)
	}
	reconciler, err = controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	identityMissingResult, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || identityMissingResult.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("identity missing publish=(%#v, %v)", identityMissingResult, err)
	}
	identityMissingSnapshot, err := repository.LoadSnapshot(ctx, identityMissingResult.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	identityMissingPlans := plansByStrategy(identityMissingSnapshot)
	if identityMissingPlans["1001"].PlanRevision != initialPlans["1001"].PlanRevision ||
		identityMissingPlans["1002"].PlanRevision == initialPlans["1002"].PlanRevision {
		t.Fatalf("identity missing last-good=%#v initial=%#v", identityMissingPlans, initialPlans)
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionSourceIncomplete, "SOURCE_IDENTITY_UNAVAILABLE")

	if err := client.Del(ctx, "bkmonitor.cache.strategy_1001").Err(); err != nil {
		t.Fatal(err)
	}
	changedSibling = strings.Replace(string(documents[1]), `"threshold":90`, `"threshold":92`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", changedSibling, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("missing pending=(%#v, %v)", result, err)
	}
	missing, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || missing.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("missing publish=(%#v, %v)", missing, err)
	}
	missingSnapshot, err := repository.LoadSnapshot(ctx, missing.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	missingPlans := plansByStrategy(missingSnapshot)
	if missingPlans["1001"].PlanRevision != initialPlans["1001"].PlanRevision ||
		missingPlans["1002"].PlanRevision == identityMissingPlans["1002"].PlanRevision {
		t.Fatalf("missing object last-good=%#v identity-missing=%#v", missingPlans, identityMissingPlans)
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionSourceIncomplete, "SOURCE_OBJECT_INCOMPLETE")

	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", `{"id":1001,`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	changedSibling = strings.Replace(string(documents[1]), `"threshold":90`, `"threshold":93`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", changedSibling, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("invalid pending=(%#v, %v)", result, err)
	}
	invalid, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || invalid.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("invalid publish=(%#v, %v)", invalid, err)
	}
	invalidSnapshot, err := repository.LoadSnapshot(ctx, invalid.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	invalidPlans := plansByStrategy(invalidSnapshot)
	if invalidPlans["1001"].PlanRevision != initialPlans["1001"].PlanRevision ||
		invalidPlans["1002"].PlanRevision == missingPlans["1002"].PlanRevision {
		t.Fatalf("invalid object last-good=%#v missing=%#v", invalidPlans, missingPlans)
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionStaleConfig, "STRATEGY_DOCUMENT_INVALID")
}

func TestRedisCatalogRepositoryRejectsRevisionMismatchAndCollision(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	tampered := catalog
	tampered.SnapshotRevision = "not-the-content-digest"
	if _, _, err := repository.PublishCatalog(context.Background(), tampered); err == nil {
		t.Fatal("tampered snapshot revision was accepted")
	}

	key := "alarmd:control:test:snapshot:" + string(catalog.SnapshotRevision)
	if err := client.Set(context.Background(), key, `{"different":"content"}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.PublishCatalog(context.Background(), catalog); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("collision error=%v", err)
	}
}

func TestRedisCatalogRepositoryPublishesEmptySnapshot(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:empty", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{}, Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := controlplane.NewSnapshotPublisher(repository)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, created, err := publisher.Publish(context.Background(), catalog)
	if err != nil || !created {
		t.Fatalf("empty publish=(%#v, %t, %v)", snapshot, created, err)
	}
	loaded, err := repository.LoadSnapshot(context.Background(), catalog.SnapshotRevision)
	if err != nil || loaded.QueryGroups == nil || len(loaded.QueryGroups) != 0 || loaded.Publication != snapshot.Publication {
		t.Fatalf("empty loaded=(%#v, %v)", loaded, err)
	}
	missing := catalog
	missing.QueryGroups = nil
	if _, _, err := repository.PublishCatalog(context.Background(), missing); err == nil || !strings.Contains(err.Error(), "incomplete catalog publication") {
		t.Fatalf("missing Catalog collection error=%v", err)
	}
}

func TestRedisCatalogRepositoryActivationCASAndProjection(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	plan := catalog.QueryGroups[0].Plans[0]
	schedule := frozenSchedule(t, snapshot.Publication, catalog.QueryGroups[0], 60, nil)
	state := activationState(t, 1, snapshot, schedule, nil)
	fact := state.Plans[0].Fact
	initial := []execution.InitialScheduleActivationFact{{Segment: schedule.Segment}}
	if err := repository.CompareAndSetInitialScheduleActivation(context.Background(), controlplane.ActivationExpectation{}, state, initial); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.LoadActivation(context.Background())
	if err != nil || loaded.RecordRevision != 1 || len(loaded.Plans) != 1 || loaded.Plans[0].Fact != fact {
		t.Fatalf("loaded activation=(%#v, %v)", loaded, err)
	}
	if err := repository.CompareAndSetInitialScheduleActivation(context.Background(), controlplane.ActivationExpectation{}, state, initial); !errors.Is(err, controlplane.ErrActivationConflict) {
		t.Fatalf("stale activation CAS error=%v", err)
	}

	contract := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: catalog.QueryGroups[0].Identity, EvaluationTime: 60},
		SnapshotRevision: snapshot.Publication.SnapshotRevision, QueryRevision: catalog.QueryGroups[0].QueryPlan.QueryRevision,
		ScheduleRevision: catalog.QueryGroups[0].ScheduleRevision, ScheduleSegmentStart: 60, DuePlanSetDigest: "due-plans-v1",
	}
	missingPlan := execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "9999"}
	activations, err := repository.LoadActivations(context.Background(), execution.PlanActivationRequest{Contract: contract, Plans: []execution.PlanIdentity{plan.Identity, missingPlan}})
	if err != nil || len(activations.Facts) != 2 || activations.Facts[0] != fact || activations.Facts[1].Selection != execution.ActivationNone {
		t.Fatalf("activation projection=(%#v, %v)", activations, err)
	}
}

func TestRedisCatalogRepositoryRenewsOnlyActivationGuardedCurrentObjects(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:renew-current"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var observations []observability.Observation
	repository.ConfigureObserver(observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observation)
	}))
	compiler, semantics := runtimePlanCompiler(t)
	clock := []time.Time{time.Unix(83, 0), time.Unix(180, 0)}
	clockIndex := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time {
		value := clock[clockIndex]
		clockIndex++
		return value
	})
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	oldSnapshot, _, err := repository.PublishCatalog(ctx, oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	oldState, err := reconciler.Ensure(ctx, oldSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	currentCatalog := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{}}
	currentCatalog.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", currentCatalog.QueryGroups))
	currentSnapshot, _, err := repository.PublishCatalog(ctx, currentCatalog)
	if err != nil {
		t.Fatal(err)
	}
	currentState, err := reconciler.Ensure(ctx, currentSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	oldKeys := []string{
		prefix + ":snapshot:" + string(oldState.Current.SnapshotRevision),
		prefix + ":snapshot_epoch:" + string(oldState.Current.SnapshotRevision),
		prefix + ":publication:" + strconv.FormatUint(oldState.Current.PublicationEpoch, 10),
		prefix + ":active_qg_set:" + oldState.ActiveQGSetRef.Digest,
	}
	currentKeys := []string{
		prefix + ":snapshot:" + string(currentState.Current.SnapshotRevision),
		prefix + ":snapshot_epoch:" + string(currentState.Current.SnapshotRevision),
		prefix + ":publication:" + strconv.FormatUint(currentState.Current.PublicationEpoch, 10),
		prefix + ":active_qg_set:" + currentState.ActiveQGSetRef.Digest,
	}
	latestKey := prefix + ":latest_publication"
	for _, key := range append(append(append([]string{}, oldKeys...), currentKeys...), latestKey) {
		if err := client.PExpire(ctx, key, 2*time.Second).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.RenewCurrentActivationObjects(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range currentKeys {
		if ttl, ttlErr := client.PTTL(ctx, key).Result(); ttlErr != nil || ttl < 30*time.Minute {
			t.Fatalf("current object %s TTL=(%s,%v), want renewed", key, ttl, ttlErr)
		}
	}
	for _, key := range oldKeys {
		if ttl, ttlErr := client.PTTL(ctx, key).Result(); ttlErr != nil || ttl <= 0 || ttl > 2*time.Second {
			t.Fatalf("historical object %s TTL=(%s,%v), want unchanged", key, ttl, ttlErr)
		}
	}
	if ttl, ttlErr := client.PTTL(ctx, latestKey).Result(); ttlErr != nil || ttl <= 0 || ttl > 2*time.Second {
		t.Fatalf("latest publication TTL=(%s,%v), want unchanged", ttl, ttlErr)
	}

	for _, key := range currentKeys {
		if err := client.PExpire(ctx, key, 2*time.Second).Err(); err != nil {
			t.Fatal(err)
		}
	}
	auxiliary := redis.NewClient(&redis.Options{Addr: client.Options().Addr})
	t.Cleanup(func() { _ = auxiliary.Close() })
	mappingClient := redis.NewClient(&redis.Options{Addr: client.Options().Addr})
	t.Cleanup(func() { _ = mappingClient.Close() })
	mappingClient.AddHook(&beforeEvalHook{run: func() error {
		return auxiliary.Set(ctx, currentKeys[1], "999", 2*time.Second).Err()
	}})
	mappingRepository, err := controlplane.NewRedisCatalogRepository(mappingClient, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := mappingRepository.RenewCurrentActivationObjects(ctx); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("concurrent mapping change renewal error=%v", err)
	}
	for _, key := range currentKeys {
		if ttl, ttlErr := client.PTTL(ctx, key).Result(); ttlErr != nil || ttl <= 0 || ttl > 2*time.Second {
			t.Fatalf("invalid mapping partially renewed %s TTL=(%s,%v)", key, ttl, ttlErr)
		}
	}
	if err := client.Set(ctx, currentKeys[1], strconv.FormatUint(currentState.Current.PublicationEpoch, 10), 2*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	guardedClient := redis.NewClient(&redis.Options{Addr: client.Options().Addr})
	t.Cleanup(func() { _ = guardedClient.Close() })
	guardedClient.AddHook(&beforeEvalHook{run: func() error {
		return auxiliary.Set(ctx, prefix+":activation_header", "concurrent-activation", 0).Err()
	}})
	guardedRepository, err := controlplane.NewRedisCatalogRepository(guardedClient, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := guardedRepository.RenewCurrentActivationObjects(ctx); !errors.Is(err, controlplane.ErrActivationConflict) {
		t.Fatalf("concurrent Activation renewal error=%v", err)
	}
	for _, key := range currentKeys {
		if ttl, ttlErr := client.PTTL(ctx, key).Result(); ttlErr != nil || ttl <= 0 || ttl > 2*time.Second {
			t.Fatalf("CAS loser object %s TTL=(%s,%v), want unchanged", key, ttl, ttlErr)
		}
	}
	operations := make(map[string]bool)
	for _, observation := range observations {
		if observation.ActiveQGSet != nil {
			operations[observation.ActiveQGSet.Operation] = true
		}
	}
	for _, operation := range []string{"encode", "write", "read", "renew"} {
		if !operations[operation] {
			t.Fatalf("missing Active Set %s observation: %#v", operation, observations)
		}
	}
}

func TestRedisCatalogRepositoryRenewCurrentObjectsFailsAtomicallyOnInvalidMappings(t *testing.T) {
	for _, test := range []struct {
		name         string
		missingIndex int
		mutate       func(context.Context, *redis.Client, []string) error
	}{
		{name: "missing Snapshot payload", missingIndex: 0, mutate: func(ctx context.Context, client *redis.Client, keys []string) error {
			return client.Del(ctx, keys[0]).Err()
		}},
		{name: "corrupt Snapshot payload", missingIndex: -1, mutate: func(ctx context.Context, client *redis.Client, keys []string) error {
			return client.Set(ctx, keys[0], "{", 2*time.Second).Err()
		}},
		{name: "missing revision epoch mapping", missingIndex: 1, mutate: func(ctx context.Context, client *redis.Client, keys []string) error {
			return client.Del(ctx, keys[1]).Err()
		}},
		{name: "wrong revision epoch mapping", missingIndex: -1, mutate: func(ctx context.Context, client *redis.Client, keys []string) error {
			return client.Set(ctx, keys[1], "999", 2*time.Second).Err()
		}},
		{name: "missing publication occurrence", missingIndex: 2, mutate: func(ctx context.Context, client *redis.Client, keys []string) error {
			return client.Del(ctx, keys[2]).Err()
		}},
		{name: "wrong publication occurrence", missingIndex: -1, mutate: func(ctx context.Context, client *redis.Client, keys []string) error {
			return client.Set(ctx, keys[2], "wrong-revision", 2*time.Second).Err()
		}},
		{name: "missing Active Set", missingIndex: 3, mutate: func(ctx context.Context, client *redis.Client, keys []string) error {
			return client.Del(ctx, keys[3]).Err()
		}},
		{name: "corrupt Active Set", missingIndex: -1, mutate: func(ctx context.Context, client *redis.Client, keys []string) error {
			return client.Set(ctx, keys[3], `{"schema_version":"alarmd-active-qg-set-v1","query_groups":[]}`, 2*time.Second).Err()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			client := newControlplaneRedis(t)
			prefix := "alarmd:control:renew-invalid:" + strings.ReplaceAll(test.name, " ", "-")
			repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			catalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
			snapshot, _, err := repository.PublishCatalog(ctx, catalog)
			if err != nil {
				t.Fatal(err)
			}
			compiler, semantics := runtimePlanCompiler(t)
			initial, _ := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(83, 0) })
			state, err := initial.Ensure(ctx, snapshot.Publication)
			if err != nil {
				t.Fatal(err)
			}
			keys := []string{
				prefix + ":snapshot:" + string(state.Current.SnapshotRevision),
				prefix + ":snapshot_epoch:" + string(state.Current.SnapshotRevision),
				prefix + ":publication:" + strconv.FormatUint(state.Current.PublicationEpoch, 10),
				prefix + ":active_qg_set:" + state.ActiveQGSetRef.Digest,
			}
			for _, key := range keys {
				if err := client.PExpire(ctx, key, 2*time.Second).Err(); err != nil {
					t.Fatal(err)
				}
			}
			if err := test.mutate(ctx, client, keys); err != nil {
				t.Fatal(err)
			}
			if err := repository.RenewCurrentActivationObjects(ctx); err == nil {
				t.Fatalf("invalid mapping renewal error=%v", err)
			}
			for index, key := range keys {
				ttl, err := client.PTTL(ctx, key).Result()
				if index == test.missingIndex {
					if err != nil || ttl != -2*time.Nanosecond {
						t.Fatalf("missing mapping TTL=(%s,%v)", ttl, err)
					}
					continue
				}
				if err != nil || ttl <= 0 || ttl > 2*time.Second {
					t.Fatalf("failed renewal partially renewed %s TTL=(%s,%v)", key, ttl, err)
				}
			}
		})
	}
}

func TestInitialScheduleActivatorPersistsOneNonAlignedBoundaryAcrossRestart(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:first-activation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := catalog.QueryGroups[0].Identity
	if _, err := runtime.ReadInitialFrozenSchedule(context.Background(), queryGroup); !errors.Is(err, controlplane.ErrScheduleUnavailable) {
		t.Fatalf("missing production activation error=%v", err)
	}

	var clockCalls atomic.Int32
	activator, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, func() time.Time {
		clockCalls.Add(1)
		return time.Unix(83, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := activator.Ensure(context.Background(), snapshot.Publication)
	if err != nil || state.Current != snapshot.Publication || state.RecordRevision != 1 {
		t.Fatalf("initial activation=(%#v, %v)", state, err)
	}
	if len(state.Plans) != 1 || state.Plans[0].Fact.Selected.RequiredFullSlots != 1 {
		t.Fatalf("initial Plan activation=%#v", state.Plans)
	}
	schedule, err := runtime.ReadInitialFrozenSchedule(context.Background(), queryGroup)
	if err != nil {
		t.Fatal(err)
	}
	firstSlot, ok := schedule.FirstSlot()
	if schedule.Segment.Start != 83 || !ok || firstSlot != 120 {
		t.Fatalf("initial segment start=%d first slot=(%d,%v), want start=83 slot=120", schedule.Segment.Start, firstSlot, ok)
	}

	restartedClockCalls := 0
	restarted, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, func() time.Time {
		restartedClockCalls++
		return time.Unix(999, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := restarted.Ensure(context.Background(), snapshot.Publication)
	if err != nil || reloaded.RecordRevision != 1 || reloaded.Current != snapshot.Publication {
		t.Fatalf("restart activation=(%#v, %v)", reloaded, err)
	}
	if clockCalls.Load() != 1 || restartedClockCalls != 0 {
		t.Fatalf("clock calls=(%d,%d), want (1,0)", clockCalls.Load(), restartedClockCalls)
	}
	reloadedSchedule, err := runtime.ReadInitialFrozenSchedule(context.Background(), queryGroup)
	if err != nil || reloadedSchedule.Segment.Start != 83 {
		t.Fatalf("restart schedule=(%#v, %v)", reloadedSchedule, err)
	}
}

func TestScheduleActivationReconcilerUpgradesLegacySamePublicationToActiveQGSetRef(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:legacy-active-qg-upgrade"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	snapshot, _, err := repository.PublishCatalog(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	initial, err := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(83, 0) })
	if err != nil {
		t.Fatal(err)
	}
	state, err := initial.Ensure(ctx, snapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}

	// Reproduce the deployed v1 shape without changing Current, Plans or Schedule.
	legacy := state
	legacy.SchemaVersion = "alarmd-control-activation-v1"
	legacy.ActiveQGSetRef = controlplane.ActiveQueryGroupSetRef{}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, prefix+":activation", payload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	scanHook := &scanCountingHook{}
	client.AddHook(scanHook)

	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time {
		t.Fatal("same-publication upgrade must not allocate a cutover boundary")
		return time.Time{}
	})
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := reconciler.Ensure(ctx, snapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	if got := scanHook.count.Load(); got != 0 {
		t.Fatalf("same-publication upgrade SCAN calls=%d, want 0", got)
	}
	if upgraded.SchemaVersion != "alarmd-control-activation-v2" || upgraded.RecordRevision != legacy.RecordRevision+1 || upgraded.Current != legacy.Current || upgraded.Pending != legacy.Pending || !reflect.DeepEqual(upgraded.Plans, legacy.Plans) || !reflect.DeepEqual(upgraded.Draining, legacy.Draining) {
		t.Fatalf("invalid upgraded activation: %#v", upgraded)
	}
	groups, err := repository.LoadActiveQueryGroupSet(ctx, upgraded.ActiveQGSetRef)
	if err != nil || !reflect.DeepEqual(groups, []execution.QueryGroupIdentity{catalog.QueryGroups[0].Identity}) {
		t.Fatalf("active set=(%#v,%v)", groups, err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := runtime.ReadInitialFrozenSchedule(ctx, catalog.QueryGroups[0].Identity)
	if err != nil || schedule.Segment.Start != 83 {
		t.Fatalf("schedule changed=(%#v,%v)", schedule, err)
	}
}

func TestScheduleActivationReconcilerDoesNotFallbackWhenV2ActiveSetInvalid(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(context.Context, *redis.Client, string) error
	}{
		{name: "missing", mutate: func(ctx context.Context, client *redis.Client, key string) error {
			return client.Del(ctx, key).Err()
		}},
		{name: "count and digest mismatch", mutate: func(ctx context.Context, client *redis.Client, key string) error {
			return client.Set(ctx, key, `{"schema_version":"alarmd-active-qg-set-v1","query_groups":[]}`, time.Hour).Err()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			client := newControlplaneRedis(t)
			prefix := "alarmd:control:v2-active-set-invalid:" + strings.ReplaceAll(test.name, " ", "-")
			repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			catalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
			snapshot, _, err := repository.PublishCatalog(ctx, catalog)
			if err != nil {
				t.Fatal(err)
			}
			compiler, semantics := runtimePlanCompiler(t)
			initial, _ := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(83, 0) })
			state, err := initial.Ensure(ctx, snapshot.Publication)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.mutate(ctx, client, prefix+":active_qg_set:"+state.ActiveQGSetRef.Digest); err != nil {
				t.Fatal(err)
			}
			activationBefore, _ := client.Get(ctx, prefix+":activation").Bytes()
			scheduleKey := prefix + ":schedule_timeline:" + string(catalog.QueryGroups[0].Identity)
			scheduleBefore, _ := client.Get(ctx, scheduleKey).Bytes()
			scanHook := &scanCountingHook{}
			client.AddHook(scanHook)
			reconciler, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, time.Now)
			if _, err := reconciler.Ensure(ctx, snapshot.Publication); err == nil {
				t.Fatal("invalid v2 Active Set must fail closed")
			}
			activationAfter, _ := client.Get(ctx, prefix+":activation").Bytes()
			scheduleAfter, _ := client.Get(ctx, scheduleKey).Bytes()
			if !bytes.Equal(activationBefore, activationAfter) || !bytes.Equal(scheduleBefore, scheduleAfter) {
				t.Fatal("failed v2 load changed Activation or Schedule")
			}
			if got := scanHook.count.Load(); got != 0 {
				t.Fatalf("invalid v2 Active Set SCAN calls=%d, want 0", got)
			}
		})
	}
}

func TestScheduleActivationReconcilerMigratesLegacyAfterOldSnapshotExpiresAndQGIsDeleted(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:legacy-expired-qg-deleted"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var migrationObservations []observability.Observation
	repository.ConfigureObserver(observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.LegacyMigration != nil {
			migrationObservations = append(migrationObservations, observation)
		}
	}))
	if err := repository.ConfigureLegacyMigration(50000, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	oldCatalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	oldSnapshot, _, err := repository.PublishCatalog(ctx, oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	initial, _ := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(83, 0) })
	oldState, err := initial.Ensure(ctx, oldSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	legacy := oldState
	legacy.SchemaVersion = "alarmd-control-activation-v1"
	legacy.ActiveQGSetRef = controlplane.ActiveQueryGroupSetRef{}
	legacyPayload, _ := json.Marshal(legacy)
	if err := client.Set(ctx, prefix+":activation", legacyPayload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, prefix+":snapshot:"+string(oldSnapshot.Publication.SnapshotRevision)).Err(); err != nil {
		t.Fatal(err)
	}
	emptyCatalog := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{}}
	emptyCatalog.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", emptyCatalog.QueryGroups))
	emptySnapshot, _, err := repository.PublishCatalog(ctx, emptyCatalog)
	if err != nil {
		t.Fatal(err)
	}
	activationBefore, _ := client.Get(ctx, prefix+":activation").Bytes()
	scheduleKey := prefix + ":schedule_timeline:" + string(oldCatalog.QueryGroups[0].Identity)
	scheduleBefore, _ := client.Get(ctx, scheduleKey).Bytes()
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	canceledReconciler, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(180, 0) })
	if _, err := canceledReconciler.Ensure(canceledCtx, emptySnapshot.Publication); !errors.Is(err, context.Canceled) {
		t.Fatalf("parent context cancellation error=%v", err)
	}
	activationAfterCancel, _ := client.Get(ctx, prefix+":activation").Bytes()
	scheduleAfterCancel, _ := client.Get(ctx, scheduleKey).Bytes()
	if !bytes.Equal(activationBefore, activationAfterCancel) || !bytes.Equal(scheduleBefore, scheduleAfterCancel) {
		t.Fatal("parent context cancellation changed Activation or Schedule")
	}
	if err := repository.ConfigureLegacyMigration(50000, time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	timeoutReconciler, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(180, 0) })
	if _, err := timeoutReconciler.Ensure(ctx, emptySnapshot.Publication); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("migration timeout error=%v", err)
	}
	activationAfterTimeout, _ := client.Get(ctx, prefix+":activation").Bytes()
	scheduleAfterTimeout, _ := client.Get(ctx, scheduleKey).Bytes()
	if !bytes.Equal(activationBefore, activationAfterTimeout) || !bytes.Equal(scheduleBefore, scheduleAfterTimeout) {
		t.Fatal("migration timeout changed Activation or Schedule")
	}
	if err := client.Set(ctx, prefix+":schedule_timeline:extra", `{}`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureLegacyMigration(1, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	reconciler, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(180, 0) })
	if _, err := reconciler.Ensure(ctx, emptySnapshot.Publication); err == nil {
		t.Fatal("max_scan_keys overflow must fail closed")
	}
	activationAfter, _ := client.Get(ctx, prefix+":activation").Bytes()
	scheduleAfter, _ := client.Get(ctx, scheduleKey).Bytes()
	if !bytes.Equal(activationBefore, activationAfter) || !bytes.Equal(scheduleBefore, scheduleAfter) {
		t.Fatal("scan overflow changed Activation or Schedule")
	}
	_ = client.Del(ctx, prefix+":schedule_timeline:extra").Err()
	_ = repository.ConfigureLegacyMigration(50000, 30*time.Second)
	state, err := reconciler.Ensure(ctx, emptySnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != emptySnapshot.Publication || state.SchemaVersion != "alarmd-control-activation-v2" || len(state.Plans) != 0 {
		t.Fatalf("migration state=%#v", state)
	}
	groups, err := repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
	if err != nil || len(groups) != 0 {
		t.Fatalf("active set=(%#v,%v)", groups, err)
	}
	if len(migrationObservations) != 3 || migrationObservations[0].LegacyMigration.Result != "canceled" ||
		migrationObservations[1].LegacyMigration.Result != "fail_closed" || migrationObservations[2].LegacyMigration.Result != "success" ||
		migrationObservations[2].LegacyMigration.ScanKeys == 0 {
		t.Fatalf("legacy migration observations=%#v", migrationObservations)
	}
}

func TestScheduleActivationReconcilerLegacyMigrationRejectsUnprovableCoverage(t *testing.T) {
	type mutation func(*testing.T, context.Context, *redis.Client, string, controlplane.ActivationState, []string)
	tests := []struct {
		name   string
		mutate mutation
	}{
		{name: "activation plan missing open segment", mutate: func(t *testing.T, ctx context.Context, client *redis.Client, _ string, _ controlplane.ActivationState, scheduleKeys []string) {
			if err := client.Del(ctx, scheduleKeys[1]).Err(); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "same plan covered by two open segments", mutate: func(t *testing.T, ctx context.Context, client *redis.Client, _ string, _ controlplane.ActivationState, scheduleKeys []string) {
			first := readJSONObject(t, ctx, client, scheduleKeys[0])
			second := readJSONObject(t, ctx, client, scheduleKeys[1])
			firstOpen := lastScheduleSegment(t, first)
			secondOpen := lastScheduleSegment(t, second)
			secondOpen["schedule"] = cloneJSONValue(t, firstOpen["schedule"])
			secondOpen["plans"] = cloneJSONValue(t, firstOpen["plans"])
			schedule := secondOpen["schedule"].(map[string]any)
			segment := schedule["Segment"].(map[string]any)
			segment["QueryGroup"] = second["query_group"]
			writeJSONObject(t, ctx, client, scheduleKeys[1], second)
		}},
		{name: "orphan open segment", mutate: func(t *testing.T, ctx context.Context, client *redis.Client, prefix string, state controlplane.ActivationState, _ []string) {
			state.Plans = append([]controlplane.PlanActivationRecord(nil), state.Plans[:1]...)
			writeLegacyActivation(t, ctx, client, prefix, state)
		}},
		{name: "wrong publication", mutate: func(t *testing.T, ctx context.Context, client *redis.Client, _ string, _ controlplane.ActivationState, scheduleKeys []string) {
			timeline := readJSONObject(t, ctx, client, scheduleKeys[0])
			open := lastScheduleSegment(t, timeline)
			schedule := open["schedule"].(map[string]any)
			segment := schedule["Segment"].(map[string]any)
			publication := segment["Publication"].(map[string]any)
			publication["PublicationEpoch"] = publication["PublicationEpoch"].(float64) + 1
			writeJSONObject(t, ctx, client, scheduleKeys[0], timeline)
		}},
		{name: "query group plan conflict", mutate: func(t *testing.T, ctx context.Context, client *redis.Client, _ string, _ controlplane.ActivationState, scheduleKeys []string) {
			timeline := readJSONObject(t, ctx, client, scheduleKeys[0])
			timeline["query_group"] = "conflicting-query-group"
			writeJSONObject(t, ctx, client, scheduleKeys[0], timeline)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			client := newControlplaneRedis(t)
			prefix := "alarmd:control:legacy-negative:" + strings.ReplaceAll(test.name, " ", "-")
			repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if err := repository.ConfigureLegacyMigration(50000, 30*time.Second); err != nil {
				t.Fatal(err)
			}
			catalog := twoQueryGroupCatalog(t)
			snapshot, _, err := repository.PublishCatalog(ctx, catalog)
			if err != nil {
				t.Fatal(err)
			}
			compiler, semantics := runtimePlanCompiler(t)
			initial, _ := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(83, 0) })
			state, err := initial.Ensure(ctx, snapshot.Publication)
			if err != nil {
				t.Fatal(err)
			}
			state.SchemaVersion = "alarmd-control-activation-v1"
			state.ActiveQGSetRef = controlplane.ActiveQueryGroupSetRef{}
			writeLegacyActivation(t, ctx, client, prefix, state)
			if err := client.Del(ctx, prefix+":snapshot:"+string(snapshot.Publication.SnapshotRevision)).Err(); err != nil {
				t.Fatal(err)
			}
			scheduleKeys := make([]string, 0, len(catalog.QueryGroups))
			for _, group := range catalog.QueryGroups {
				scheduleKeys = append(scheduleKeys, prefix+":schedule_timeline:"+string(group.Identity))
			}
			sort.Strings(scheduleKeys)
			test.mutate(t, ctx, client, prefix, state, scheduleKeys)

			activationBefore := readRedisValue(t, ctx, client, prefix+":activation")
			headerBefore := readRedisValue(t, ctx, client, prefix+":activation_header")
			schedulesBefore := readRedisValues(t, ctx, client, scheduleKeys)
			activeSetsBefore, err := client.Keys(ctx, prefix+":active_qg_set:*").Result()
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(activeSetsBefore)
			emptyCatalog := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{}}
			emptyCatalog.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", emptyCatalog.QueryGroups))
			emptySnapshot, _, err := repository.PublishCatalog(ctx, emptyCatalog)
			if err != nil {
				t.Fatal(err)
			}
			clockCalls := 0
			reconciler, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time {
				clockCalls++
				return time.Unix(180, 0)
			})
			if _, err := reconciler.Ensure(ctx, emptySnapshot.Publication); err == nil {
				t.Fatal("unprovable legacy coverage must fail closed")
			}
			if clockCalls != 0 {
				t.Fatalf("cutover/business side-effect clock calls=%d, want 0", clockCalls)
			}
			if after := readRedisValue(t, ctx, client, prefix+":activation"); !bytes.Equal(activationBefore, after) {
				t.Fatal("failed migration changed Activation")
			}
			if after := readRedisValue(t, ctx, client, prefix+":activation_header"); !bytes.Equal(headerBefore, after) {
				t.Fatal("failed migration changed Activation header")
			}
			if after := readRedisValues(t, ctx, client, scheduleKeys); !reflect.DeepEqual(schedulesBefore, after) {
				t.Fatal("failed migration changed Schedule timelines")
			}
			activeSetsAfter, err := client.Keys(ctx, prefix+":active_qg_set:*").Result()
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(activeSetsAfter)
			if !reflect.DeepEqual(activeSetsBefore, activeSetsAfter) {
				t.Fatalf("failed migration created a v2 Active Set: before=%v after=%v", activeSetsBefore, activeSetsAfter)
			}
		})
	}
}

func TestScheduleActivationReconcilerLegacyMigrationResumesAfterObjectWriteBeforeCAS(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:legacy-migration-resume"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureLegacyMigration(50000, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	catalog := twoQueryGroupCatalog(t)
	snapshot, _, err := repository.PublishCatalog(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	initial, _ := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(83, 0) })
	state, err := initial.Ensure(ctx, snapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	activeSetKey := prefix + ":active_qg_set:" + state.ActiveQGSetRef.Digest
	state.SchemaVersion = "alarmd-control-activation-v1"
	state.ActiveQGSetRef = controlplane.ActiveQueryGroupSetRef{}
	writeLegacyActivation(t, ctx, client, prefix, state)
	if err := client.Del(ctx, prefix+":snapshot:"+string(snapshot.Publication.SnapshotRevision), activeSetKey).Err(); err != nil {
		t.Fatal(err)
	}
	scheduleKeys := scheduleTimelineKeys(prefix, catalog)
	activationBefore := readRedisValue(t, ctx, client, prefix+":activation")
	schedulesBefore := readRedisValues(t, ctx, client, scheduleKeys)
	crashErr := errors.New("simulated crash before Activation CAS")
	client.AddHook(&beforeEvalHook{run: func() error { return crashErr }})
	crashing, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time {
		t.Fatal("same-publication migration must not allocate a cutover boundary")
		return time.Time{}
	})
	if _, err := crashing.Ensure(ctx, snapshot.Publication); !errors.Is(err, crashErr) {
		t.Fatalf("interrupted migration error=%v, want %v", err, crashErr)
	}
	if after := readRedisValue(t, ctx, client, prefix+":activation"); !bytes.Equal(activationBefore, after) {
		t.Fatal("interrupted migration changed Activation")
	}
	if after := readRedisValues(t, ctx, client, scheduleKeys); !reflect.DeepEqual(schedulesBefore, after) {
		t.Fatal("interrupted migration changed Schedule timelines")
	}
	if exists, err := client.Exists(ctx, activeSetKey).Result(); err != nil || exists != 1 {
		t.Fatalf("pre-CAS Active Set exists=(%d,%v), want orphan available for retry", exists, err)
	}

	retryClient := redis.NewClient(&redis.Options{Addr: client.Options().Addr})
	t.Cleanup(func() { _ = retryClient.Close() })
	retryRepository, err := controlplane.NewRedisCatalogRepository(retryClient, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := retryRepository.ConfigureLegacyMigration(50000, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	retry, _ := controlplane.NewScheduleActivationReconciler(retryRepository, compiler, semantics, func() time.Time {
		t.Fatal("retry migration must not allocate a cutover boundary")
		return time.Time{}
	})
	winner, err := retry.Ensure(ctx, snapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := retry.Ensure(ctx, snapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(winner, repeated) || winner.RecordRevision != state.RecordRevision+1 || winner.SchemaVersion != "alarmd-control-activation-v2" {
		t.Fatalf("migration retry/repeat states=(%#v,%#v)", winner, repeated)
	}
	groups, err := retryRepository.LoadActiveQueryGroupSet(ctx, winner.ActiveQGSetRef)
	if err != nil || len(groups) != len(catalog.QueryGroups) || winner.ActiveQGSetRef.QGCount != uint64(len(catalog.QueryGroups)) {
		t.Fatalf("winner Active Set groups=%#v ref=%#v err=%v", groups, winner.ActiveQGSetRef, err)
	}
	if after := readRedisValues(t, ctx, retryClient, scheduleKeys); !reflect.DeepEqual(schedulesBefore, after) {
		t.Fatal("retry/repeat migration changed Schedule timelines")
	}
}

func TestScheduleActivationReconcilerConcurrentLegacyMigrationReadsSingleWinner(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:legacy-migration-race"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureLegacyMigration(50000, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	catalog := twoQueryGroupCatalog(t)
	snapshot, _, err := repository.PublishCatalog(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	initial, _ := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(83, 0) })
	state, err := initial.Ensure(ctx, snapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	activeSetKey := prefix + ":active_qg_set:" + state.ActiveQGSetRef.Digest
	state.SchemaVersion = "alarmd-control-activation-v1"
	state.ActiveQGSetRef = controlplane.ActiveQueryGroupSetRef{}
	writeLegacyActivation(t, ctx, client, prefix, state)
	if err := client.Del(ctx, prefix+":snapshot:"+string(snapshot.Publication.SnapshotRevision), activeSetKey).Err(); err != nil {
		t.Fatal(err)
	}
	scheduleKeys := scheduleTimelineKeys(prefix, catalog)
	schedulesBefore := readRedisValues(t, ctx, client, scheduleKeys)
	auxiliary := redis.NewClient(&redis.Options{Addr: client.Options().Addr})
	t.Cleanup(func() { _ = auxiliary.Close() })
	casHook := &serialActivationCASHook{firstDone: make(chan struct{}), afterFirst: func() error {
		return auxiliary.PExpire(ctx, activeSetKey, 2*time.Second).Err()
	}}
	client.AddHook(casHook)
	first, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time {
		t.Fatal("legacy migration must not allocate a cutover boundary")
		return time.Time{}
	})
	second, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time {
		t.Fatal("legacy migration must not allocate a cutover boundary")
		return time.Time{}
	})
	type migrationResult struct {
		state controlplane.ActivationState
		err   error
	}
	results := make(chan migrationResult, 2)
	go func() {
		result, runErr := first.Ensure(ctx, snapshot.Publication)
		results <- migrationResult{state: result, err: runErr}
	}()
	go func() {
		result, runErr := second.Ensure(ctx, snapshot.Publication)
		results <- migrationResult{state: result, err: runErr}
	}()
	one, two := <-results, <-results
	if one.err != nil || two.err != nil || !reflect.DeepEqual(one.state, two.state) {
		t.Fatalf("concurrent legacy migration states=(%#v,%#v) errors=(%v,%v)", one.state, two.state, one.err, two.err)
	}
	if one.state.RecordRevision != state.RecordRevision+1 || one.state.SchemaVersion != "alarmd-control-activation-v2" || one.state.ActiveQGSetRef.Digest == "" || one.state.ActiveQGSetRef.QGCount != uint64(len(catalog.QueryGroups)) {
		t.Fatalf("invalid migration winner=%#v", one.state)
	}
	if got := casHook.evals.Load(); got != 2 {
		t.Fatalf("legacy migration CAS attempts=%d, want 2", got)
	}
	if ttl, err := auxiliary.PTTL(ctx, activeSetKey).Result(); err != nil || ttl <= 0 || ttl > 2*time.Second {
		t.Fatalf("CAS loser renewed/deleted winner object TTL=(%s,%v), want unchanged short TTL", ttl, err)
	}
	if after := readRedisValues(t, ctx, client, scheduleKeys); !reflect.DeepEqual(schedulesBefore, after) {
		t.Fatal("concurrent migration changed Schedule timelines")
	}
	groups, err := repository.LoadActiveQueryGroupSet(ctx, one.state.ActiveQGSetRef)
	if err != nil || len(groups) != len(catalog.QueryGroups) {
		t.Fatalf("winner Active Set groups=%#v err=%v", groups, err)
	}
}

func scheduleTimelineKeys(prefix string, catalog controlplane.Catalog) []string {
	keys := make([]string, 0, len(catalog.QueryGroups))
	for _, group := range catalog.QueryGroups {
		keys = append(keys, prefix+":schedule_timeline:"+string(group.Identity))
	}
	sort.Strings(keys)
	return keys
}

func writeLegacyActivation(t *testing.T, ctx context.Context, client *redis.Client, prefix string, state controlplane.ActivationState) {
	t.Helper()
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, prefix+":activation", payload, 0).Err(); err != nil {
		t.Fatal(err)
	}
}

func readJSONObject(t *testing.T, ctx context.Context, client *redis.Client, key string) map[string]any {
	t.Helper()
	payload := readRedisValue(t, ctx, client, key)
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func writeJSONObject(t *testing.T, ctx context.Context, client *redis.Client, key string, value map[string]any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, key, payload, 0).Err(); err != nil {
		t.Fatal(err)
	}
}

func cloneJSONValue(t *testing.T, value any) any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var cloned any
	if err := json.Unmarshal(payload, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func lastScheduleSegment(t *testing.T, timeline map[string]any) map[string]any {
	t.Helper()
	segments, ok := timeline["segments"].([]any)
	if !ok || len(segments) == 0 {
		t.Fatal("persisted Schedule has no segments")
	}
	segment, ok := segments[len(segments)-1].(map[string]any)
	if !ok {
		t.Fatal("persisted Schedule segment is not an object")
	}
	return segment
}

func readRedisValue(t *testing.T, ctx context.Context, client *redis.Client, key string) []byte {
	t.Helper()
	payload, err := client.Get(ctx, key).Bytes()
	if err != nil && !errors.Is(err, redis.Nil) {
		t.Fatal(err)
	}
	return payload
}

func readRedisValues(t *testing.T, ctx context.Context, client *redis.Client, keys []string) map[string][]byte {
	t.Helper()
	values := make(map[string][]byte, len(keys))
	for _, key := range keys {
		values[key] = readRedisValue(t, ctx, client, key)
	}
	return values
}

func TestInitialScheduleActivatorPersistsEveryPublishedQueryGroup(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:first-activation-multi-qg", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := twoQueryGroupCatalog(t)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	activator, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, func() time.Time {
		return time.Unix(83, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := activator.Ensure(context.Background(), snapshot.Publication)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if len(state.Plans) != 2 {
		t.Fatalf("activated Plans=%d, want 2", len(state.Plans))
	}

	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range catalog.QueryGroups {
		schedule, loadErr := runtime.ReadInitialFrozenSchedule(context.Background(), group.Identity)
		if loadErr != nil {
			t.Fatalf("ReadInitialFrozenSchedule(%s) error = %v", group.Identity, loadErr)
		}
		if schedule.Segment.QueryGroup != group.Identity || schedule.Segment.Start != 83 || len(schedule.Plans) != len(group.Plans) {
			t.Fatalf("schedule(%s) = %#v", group.Identity, schedule)
		}
	}
}

func TestInitialScheduleActivatorConcurrentCASKeepsWinnerFact(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:first-activation-race", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)

	var entered sync.WaitGroup
	entered.Add(2)
	release := make(chan struct{})
	newClock := func(unix int64) func() time.Time {
		return func() time.Time {
			entered.Done()
			<-release
			return time.Unix(unix, 0)
		}
	}
	first, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, newClock(83))
	if err != nil {
		t.Fatal(err)
	}
	second, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, newClock(97))
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		state controlplane.ActivationState
		err   error
	}
	results := make(chan result, 2)
	go func() {
		state, runErr := first.Ensure(context.Background(), snapshot.Publication)
		results <- result{state: state, err: runErr}
	}()
	go func() {
		state, runErr := second.Ensure(context.Background(), snapshot.Publication)
		results <- result{state: state, err: runErr}
	}()
	entered.Wait()
	close(release)
	one, two := <-results, <-results
	if one.err != nil || two.err != nil || one.state.RecordRevision != 1 || !reflect.DeepEqual(two.state, one.state) {
		t.Fatalf("concurrent activations=(%#v,%#v)", one, two)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := runtime.ReadInitialFrozenSchedule(context.Background(), catalog.QueryGroups[0].Identity)
	if err != nil || (schedule.Segment.Start != 83 && schedule.Segment.Start != 97) {
		t.Fatalf("winner schedule=(%#v, %v)", schedule, err)
	}
}

func TestScheduleActivationReconcilerPersistsOnePublicationBoundaryAndExactHalfOpenSlots(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:publication-cutover", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	oldSnapshot, _, err := repository.PublishCatalog(context.Background(), oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	clock := []time.Time{time.Unix(83, 0), time.Unix(90, 0)}
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		at := clock[clockCalls]
		clockCalls++
		return at
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(context.Background(), oldSnapshot.Publication); err != nil {
		t.Fatal(err)
	}
	oldActivation, err := repository.LoadActivation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	activeSetKey := "alarmd:control:publication-cutover:active_qg_set:" + oldActivation.ActiveQGSetRef.Digest
	if err := client.PExpire(context.Background(), activeSetKey, 2*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	scanHook := &scanCountingHook{}
	client.AddHook(scanHook)

	newCatalog := catalogWithSchedule(t, validCatalog(t, 81), 60, 0)
	newSnapshot, _, err := repository.PublishCatalog(context.Background(), newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconciler.Ensure(context.Background(), newSnapshot.Publication)
	if err != nil || state.Current != newSnapshot.Publication || state.RecordRevision != 2 {
		t.Fatalf("publication cutover=(%#v, %v)", state, err)
	}
	if clockCalls != 2 {
		t.Fatalf("publication clock calls=%d, want 2", clockCalls)
	}
	if _, err := reconciler.Ensure(context.Background(), newSnapshot.Publication); err != nil || clockCalls != 2 {
		t.Fatalf("winner reload error=%v clock calls=%d", err, clockCalls)
	}
	if got := scanHook.count.Load(); got != 0 {
		t.Fatalf("v2 cutover and unchanged refresh SCAN calls=%d, want 0", got)
	}
	if ttl, ttlErr := client.PTTL(context.Background(), activeSetKey).Result(); ttlErr != nil || ttl < 30*time.Minute {
		t.Fatalf("CAS winner Active Set TTL=(%s,%v), want refreshed", ttl, ttlErr)
	}

	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := oldCatalog.QueryGroups[0].Identity
	oldSchedule, err := runtime.ReadFrozenSchedule(context.Background(), queryGroup, 83)
	if err != nil || oldSchedule.Segment.End == nil || *oldSchedule.Segment.End != 90 {
		t.Fatalf("old half-open Segment=(%#v, %v)", oldSchedule, err)
	}
	if first, ok := oldSchedule.FirstSlot(); ok || first != 120 {
		t.Fatalf("old [83,90) first Slot=(%d,%t), want candidate 120 without ownership", first, ok)
	}
	newSchedule, err := runtime.ReadFrozenSchedule(context.Background(), queryGroup, 90)
	if err != nil || newSchedule.Segment.Start != 90 || newSchedule.Segment.Publication.SnapshotRevision != newSnapshot.Publication.SnapshotRevision {
		t.Fatalf("new half-open Segment=(%#v, %v)", newSchedule, err)
	}
	if first, ok := newSchedule.FirstSlot(); !ok || first != 120 {
		t.Fatalf("new [90,+inf) first Slot=(%d,%t), want 120", first, ok)
	}
	if oldSchedule.Segment.Contains(90) || oldSchedule.Segment.Contains(120) || newSchedule.Segment.Contains(83) {
		t.Fatalf("Segment ownership overlaps: old=%#v new=%#v", oldSchedule.Segment, newSchedule.Segment)
	}
}

func TestScheduleActivationReconcilerCutsBackToHistoricalSnapshotOnNewOccurrence(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:publication-a-b-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	clock := []time.Time{time.Unix(60, 0), time.Unix(120, 0), time.Unix(180, 0)}
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		at := clock[clockCalls]
		clockCalls++
		return at
	})
	if err != nil {
		t.Fatal(err)
	}
	firstCatalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	first, _, err := repository.PublishCatalog(ctx, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, first.Publication); err != nil {
		t.Fatal(err)
	}
	secondCatalog := catalogWithSchedule(t, validCatalog(t, 81), 60, 0)
	second, _, err := repository.PublishCatalog(ctx, secondCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, second.Publication); err != nil {
		t.Fatal(err)
	}
	republished, _, err := repository.PublishCatalog(ctx, firstCatalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconciler.Ensure(ctx, republished.Publication)
	if err != nil || state.Current != republished.Publication || state.RecordRevision != 3 || clockCalls != 3 {
		t.Fatalf("A-B-A activation=(%#v, %v), clock calls=%d", state, err, clockCalls)
	}
	if len(state.Plans) != 1 || state.Plans[0].Fact.Selected.StateApplyEpoch != execution.StateApplyEpoch(republished.Publication.PublicationEpoch) {
		t.Fatalf("A-B-A Plan activation=%#v", state.Plans)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := firstCatalog.QueryGroups[0].Identity
	for _, want := range []struct {
		at          execution.EvaluationTime
		publication controlplane.SnapshotPublicationRef
	}{
		{at: 60, publication: first.Publication},
		{at: 120, publication: second.Publication},
		{at: 180, publication: republished.Publication},
	} {
		schedule, err := runtime.ReadFrozenSchedule(ctx, queryGroup, want.at)
		if err != nil || schedule.Segment.Publication.SnapshotRevision != want.publication.SnapshotRevision ||
			schedule.Segment.Publication.PublicationEpoch != execution.PublicationEpoch(want.publication.PublicationEpoch) {
			t.Fatalf("Schedule at %d=(%#v, %v), want publication %#v", want.at, schedule, err, want.publication)
		}
	}
	firstSchedule, err := runtime.ReadFrozenSchedule(ctx, queryGroup, 60)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := runtime.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: queryGroup, ScheduleRevision: firstSchedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: firstSchedule.Segment.Start, EvaluationTime: 60,
		DuePlans: firstSchedule.DuePlanRefs(60),
	})
	if err != nil || fact.Contract.SnapshotRevision != first.Publication.SnapshotRevision {
		t.Fatalf("historical occurrence replay=(%#v, %v)", fact, err)
	}
}

func TestScheduleActivationReconcilerForcesWarmingWhenPlanReturnsToActiveQueryGroup(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:plan-reactivation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	clock := []time.Time{time.Unix(60, 0), time.Unix(120, 0), time.Unix(180, 0), time.Unix(240, 0)}
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		at := clock[clockCalls]
		clockCalls++
		return at
	})
	if err != nil {
		t.Fatal(err)
	}

	initialCatalog := sharedQueryGroupCatalog(t, true, false)
	initialSnapshot, _, err := repository.PublishCatalog(ctx, initialCatalog)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := reconciler.Ensure(ctx, initialSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	initialA := activationRecordByStrategy(t, initial, "1001")
	if initialA.Fact.Selected.ForceWarming {
		t.Fatalf("initial Plan A unexpectedly forced WARMING: %#v", initialA)
	}
	expandedCatalog := sharedQueryGroupCatalog(t, true, true)
	if expandedCatalog.QueryGroups[0].Identity != initialCatalog.QueryGroups[0].Identity {
		t.Fatalf("Plan addition changed Query Group identity: initial=%s expanded=%s",
			initialCatalog.QueryGroups[0].Identity, expandedCatalog.QueryGroups[0].Identity)
	}
	expandedSnapshot, _, err := repository.PublishCatalog(ctx, expandedCatalog)
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := reconciler.Ensure(ctx, expandedSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	if activationRecordByStrategy(t, expanded, "1001").Fact.Selected.ForceWarming {
		t.Fatalf("continuous Plan A unexpectedly forced WARMING: %#v", expanded.Plans)
	}
	if !activationRecordByStrategy(t, expanded, "1002").Fact.Selected.ForceWarming {
		t.Fatalf("new Plan B did not force WARMING: %#v", expanded.Plans)
	}

	remainingCatalog := sharedQueryGroupCatalog(t, false, true)
	if remainingCatalog.QueryGroups[0].Identity != initialCatalog.QueryGroups[0].Identity {
		t.Fatalf("Plan removal changed Query Group identity: initial=%s remaining=%s",
			initialCatalog.QueryGroups[0].Identity, remainingCatalog.QueryGroups[0].Identity)
	}
	remainingSnapshot, _, err := repository.PublishCatalog(ctx, remainingCatalog)
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := reconciler.Ensure(ctx, remainingSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Plans) != 1 || remaining.Plans[0].Fact.Plan.StrategyID != "1002" ||
		remaining.Plans[0].Fact.Selected.ForceWarming {
		t.Fatalf("continuous Plan B activation after sibling removal=%#v", remaining.Plans)
	}

	reenabledSnapshot, _, err := repository.PublishCatalog(ctx, expandedCatalog)
	if err != nil {
		t.Fatal(err)
	}
	reenabled, err := reconciler.Ensure(ctx, reenabledSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	returnedA := activationRecordByStrategy(t, reenabled, "1001")
	continuousB := activationRecordByStrategy(t, reenabled, "1002")
	if !returnedA.Fact.Selected.ForceWarming ||
		returnedA.Fact.Selected.StateApplyEpoch != execution.StateApplyEpoch(reenabledSnapshot.Publication.PublicationEpoch) ||
		returnedA.Fact.Selected.StateApplyEpoch <= initialA.Fact.Selected.StateApplyEpoch ||
		returnedA.Publication != reenabledSnapshot.Publication {
		t.Fatalf("reactivated Plan A activation=%#v initial=%#v", returnedA, initialA)
	}
	if continuousB.Fact.Selected.ForceWarming {
		t.Fatalf("continuous Plan B unexpectedly forced WARMING: %#v", continuousB)
	}
}

func TestScheduleActivationReconcilerReturnsWinnerForStalePublicationWithoutClock(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:stale-activation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		clockCalls++
		return time.Unix(int64(clockCalls*60), 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := repository.PublishCatalog(ctx, validCatalog(t, 80))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, first.Publication); err != nil {
		t.Fatal(err)
	}
	winner, _, err := repository.PublishCatalog(ctx, validCatalog(t, 81))
	if err != nil {
		t.Fatal(err)
	}
	winnerState, err := reconciler.Ensure(ctx, winner.Publication)
	if err != nil {
		t.Fatal(err)
	}
	before := clockCalls
	staleState, err := reconciler.Ensure(ctx, first.Publication)
	if err != nil || !reflect.DeepEqual(staleState, winnerState) || clockCalls != before {
		t.Fatalf("stale activation=(%#v, %v), winner=%#v clock=%d/%d", staleState, err, winnerState, clockCalls, before)
	}
}

func TestScheduleActivationReconcilerRejectsEqualEpochDifferentRevision(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:publication-collision", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		clockCalls++
		return time.Unix(60, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := repository.PublishCatalog(ctx, validCatalog(t, 80))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, current.Publication); err != nil {
		t.Fatal(err)
	}
	forged := controlplane.SnapshotPublicationRef{SnapshotRevision: validCatalog(t, 81).SnapshotRevision,
		PublicationEpoch: current.Publication.PublicationEpoch}
	before := clockCalls
	if _, err := reconciler.Ensure(ctx, forged); err == nil || clockCalls != before {
		t.Fatalf("equal-epoch collision error=%v clock=%d/%d", err, clockCalls, before)
	}
}

func TestScheduleActivationReconcilerCASLoserRejectsEqualEpochDifferentRevisionWinner(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:publication-collision-loser"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	initial, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		return time.Unix(60, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := repository.PublishCatalog(ctx, validCatalog(t, 80))
	if err != nil {
		t.Fatal(err)
	}
	currentState, err := initial.Ensure(ctx, current.Publication)
	if err != nil {
		t.Fatal(err)
	}
	candidate, _, err := repository.PublishCatalog(ctx, validCatalog(t, 81))
	if err != nil {
		t.Fatal(err)
	}
	forged := controlplane.SnapshotPublicationRef{
		SnapshotRevision: validCatalog(t, 82).SnapshotRevision,
		PublicationEpoch: candidate.Publication.PublicationEpoch,
	}
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		clockCalls++
		winner := currentState
		winner.RecordRevision++
		winner.Current = forged
		for index := range winner.Plans {
			winner.Plans[index].Publication = forged
			winner.Plans[index].Fact.Selected.StateApplyEpoch = execution.StateApplyEpoch(forged.PublicationEpoch)
		}
		payload, marshalErr := json.Marshal(winner)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		header := strconv.FormatUint(winner.RecordRevision, 10) + "|" + string(forged.SnapshotRevision) + "@" +
			strconv.FormatUint(forged.PublicationEpoch, 10) + "|-"
		if setErr := client.MSet(ctx, prefix+":activation_header", header, prefix+":activation", payload).Err(); setErr != nil {
			t.Fatal(setErr)
		}
		return time.Unix(120, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, candidate.Publication); err == nil ||
		!strings.Contains(err.Error(), "publication epoch collision") || clockCalls != 1 {
		t.Fatalf("CAS loser equal-epoch collision error=%v clock calls=%d", err, clockCalls)
	}
}

func TestScheduleActivationReconcilerConcurrentCASLoserReadsWinnerBoundary(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:publication-cutover-race", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	oldSnapshot, _, err := repository.PublishCatalog(context.Background(), oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	initial, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		return time.Unix(60, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Ensure(context.Background(), oldSnapshot.Publication); err != nil {
		t.Fatal(err)
	}
	activation, err := repository.LoadActivation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	activeSetKey := "alarmd:control:publication-cutover-race:active_qg_set:" + activation.ActiveQGSetRef.Digest
	auxiliary := redis.NewClient(&redis.Options{Addr: client.Options().Addr})
	t.Cleanup(func() { _ = auxiliary.Close() })
	newCatalog := catalogWithSchedule(t, validCatalog(t, 81), 60, 0)
	newSnapshot, _, err := repository.PublishCatalog(context.Background(), newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	casHook := &serialActivationCASHook{firstDone: make(chan struct{}), afterFirst: func() error {
		return auxiliary.PExpire(context.Background(), activeSetKey, 2*time.Second).Err()
	}}
	client.AddHook(casHook)

	var entered sync.WaitGroup
	entered.Add(2)
	release := make(chan struct{})
	newClock := func(unix int64) func() time.Time {
		return func() time.Time {
			entered.Done()
			<-release
			return time.Unix(unix, 0)
		}
	}
	first, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, newClock(90))
	if err != nil {
		t.Fatal(err)
	}
	second, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, newClock(97))
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		state controlplane.ActivationState
		err   error
	}
	results := make(chan result, 2)
	go func() {
		state, runErr := first.Ensure(context.Background(), newSnapshot.Publication)
		results <- result{state: state, err: runErr}
	}()
	go func() {
		state, runErr := second.Ensure(context.Background(), newSnapshot.Publication)
		results <- result{state: state, err: runErr}
	}()
	entered.Wait()
	close(release)
	one, two := <-results, <-results
	if one.err != nil || two.err != nil || !reflect.DeepEqual(one.state, two.state) || one.state.RecordRevision != 2 {
		t.Fatalf("concurrent publication activations states=(%#v,%#v) errors=(%v,%v)", one.state, two.state, one.err, two.err)
	}
	if ttl, ttlErr := auxiliary.PTTL(context.Background(), activeSetKey).Result(); ttlErr != nil || ttl <= 0 || ttl > 2*time.Second {
		t.Fatalf("CAS loser Active Set TTL=(%s,%v), want unchanged short TTL", ttl, ttlErr)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := runtime.ReadFrozenSchedule(context.Background(), oldCatalog.QueryGroups[0].Identity, 100)
	if err != nil || (schedule.Segment.Start != 90 && schedule.Segment.Start != 97) {
		t.Fatalf("winner cutover boundary=(%#v,%v)", schedule, err)
	}
}

func TestScheduleActivationReconcilerProjectsQueryIdentityChangeAsIndependentNewAndRetiredGroups(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:query-identity-cutover", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := catalogWithQueryTable(t, "system.cpu")
	oldSnapshot, _, err := repository.PublishCatalog(context.Background(), oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	clock := []time.Time{time.Unix(60, 0), time.Unix(90, 0)}
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		at := clock[clockCalls]
		clockCalls++
		return at
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(context.Background(), oldSnapshot.Publication); err != nil {
		t.Fatal(err)
	}

	newCatalog := catalogWithQueryTable(t, "system.mem")
	newSnapshot, _, err := repository.PublishCatalog(context.Background(), newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconciler.Ensure(context.Background(), newSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	oldGroup := oldCatalog.QueryGroups[0].Identity
	newGroup := newCatalog.QueryGroups[0].Identity
	if oldGroup == newGroup || len(state.Draining) != 1 || state.Draining[0].QueryGroup != oldGroup || state.Draining[0].RetiredBoundary != 90 {
		t.Fatalf("query identity transition old=%s new=%s draining=%#v", oldGroup, newGroup, state.Draining)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	retiredAt, retired, err := runtime.ReadScheduleRetirement(context.Background(), oldGroup)
	if err != nil || !retired || retiredAt != 90 {
		t.Fatalf("old Query Group retirement=(%d,%t,%v)", retiredAt, retired, err)
	}
	newSchedule, err := runtime.ReadInitialFrozenSchedule(context.Background(), newGroup)
	if err != nil || newSchedule.Segment.Start != 90 {
		t.Fatalf("new Query Group activation=(%#v,%v)", newSchedule, err)
	}
	next, err := runtime.NextSlotAfter(context.Background(), oldGroup, 60)
	if err != nil || next != 90 {
		t.Fatalf("retired Query Group terminal Progress watermark=(%d,%v), want 90", next, err)
	}
}

func TestScheduleActivationReconcilerReactivatesDrainedQueryGroupOnSameProgressTimeline(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:same-qg-reactivation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	initialCatalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	initialSnapshot, _, err := repository.PublishCatalog(context.Background(), initialCatalog)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := initialCatalog.QueryGroups[0].Identity
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		queryGroup: {Status: execution.ProgressMissing},
	}}
	compiler, stateSemantics := runtimePlanCompiler(t)
	clock := []time.Time{time.Unix(60, 0), time.Unix(90, 0), time.Unix(180, 0)}
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(
		repository, compiler, stateSemantics, progress, func() time.Time {
			at := clock[clockCalls]
			clockCalls++
			return at
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(context.Background(), initialSnapshot.Publication); err != nil {
		t.Fatal(err)
	}

	emptyCatalog := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{}}
	emptyCatalog.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", emptyCatalog.QueryGroups))
	emptySnapshot, _, err := repository.PublishCatalog(context.Background(), emptyCatalog)
	if err != nil {
		t.Fatal(err)
	}
	retired, err := reconciler.Ensure(context.Background(), emptySnapshot.Publication)
	if err != nil || len(retired.Draining) != 1 || retired.Draining[0].QueryGroup != queryGroup || retired.Draining[0].RetiredBoundary != 90 {
		t.Fatalf("retirement=(%#v,%v)", retired, err)
	}
	progress.byGroup[queryGroup] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: queryGroup}, NextSlot: 90, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull,
	}}

	reenabledCatalog := catalogWithSchedule(t, validCatalog(t, 82), 60, 0)
	if reenabledCatalog.QueryGroups[0].Identity != queryGroup {
		t.Fatalf("query identity changed across reactivation: old=%s new=%s", queryGroup, reenabledCatalog.QueryGroups[0].Identity)
	}
	reenabledSnapshot, _, err := repository.PublishCatalog(context.Background(), reenabledCatalog)
	if err != nil {
		t.Fatal(err)
	}
	active, err := reconciler.Ensure(context.Background(), reenabledSnapshot.Publication)
	if err != nil || active.RecordRevision != 3 || len(active.Draining) != 0 || len(active.Plans) != 1 {
		t.Fatalf("reactivation=(%#v,%v)", active, err)
	}
	if !active.Plans[0].Fact.Selected.ForceWarming ||
		active.Plans[0].Fact.Selected.StateApplyEpoch != execution.StateApplyEpoch(reenabledSnapshot.Publication.PublicationEpoch) {
		t.Fatalf("reactivated Plan activation=%#v", active.Plans[0])
	}

	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, retiredNow, err := runtime.ReadScheduleRetirement(context.Background(), queryGroup); err != nil || retiredNow {
		t.Fatalf("reactivated retirement=(%t,%v)", retiredNow, err)
	}
	newSchedule, err := runtime.ReadFrozenSchedule(context.Background(), queryGroup, 180)
	if err != nil || newSchedule.Segment.Start != 180 || newSchedule.Segment.Publication.SnapshotRevision != reenabledSnapshot.Publication.SnapshotRevision {
		t.Fatalf("reactivated Schedule=(%#v,%v)", newSchedule, err)
	}
	if _, err := runtime.ReadFrozenSchedule(context.Background(), queryGroup, 120); !errors.Is(err, controlplane.ErrScheduleUnavailable) {
		t.Fatalf("inactive tombstone interval error=%v", err)
	}
	next, err := runtime.NextSlotAfter(context.Background(), queryGroup, 60)
	if err != nil || next != 180 {
		t.Fatalf("same Progress successor=(%d,%v), want 180", next, err)
	}
}

func TestRedisCatalogRuntimePersistsInitialScheduleAndFreezesExactSlot(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:runtime", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	schedule := frozenSchedule(t, snapshot.Publication, catalog.QueryGroups[0], 60, nil)
	activation := activationState(t, 1, snapshot, schedule, nil)
	initial := execution.InitialScheduleActivationFact{Segment: schedule.Segment}
	if err := repository.CompareAndSetInitialScheduleActivation(
		context.Background(), controlplane.ActivationExpectation{}, activation, []execution.InitialScheduleActivationFact{initial},
	); err != nil {
		t.Fatal(err)
	}

	compiler, stateSemantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := runtime.ReadInitialFrozenSchedule(context.Background(), schedule.Segment.QueryGroup)
	if err != nil || loaded.Segment.Start != 60 || loaded.Segment.End != nil {
		t.Fatalf("initial schedule=(%#v, %v)", loaded, err)
	}
	request := execution.FreezeSlotContractRequest{
		QueryGroup: schedule.Segment.QueryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: 60, DuePlans: schedule.DuePlanRefs(60),
	}
	fact, err := runtime.FreezeSlotContract(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := fact.Validate(request); err != nil {
		t.Fatal(err)
	}
	if len(fact.DuePlans) != 1 || len(fact.Requirements) != 1 || len(fact.Requirements[0].Consumers) != 1 {
		t.Fatalf("frozen fact=%#v", fact)
	}
	if fact.Contract.SnapshotRevision != snapshot.Publication.SnapshotRevision ||
		fact.DuePlans[0].StateApplyEpoch != activation.Plans[0].Fact.Selected.StateApplyEpoch {
		t.Fatalf("frozen provenance=%#v activation=%#v", fact, activation)
	}

	// Recreate the adapter to prove the persisted Catalog/Segment/activation,
	// rather than process memory, is sufficient after restart.
	restarted, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.ReadFrozenSchedule(context.Background(), schedule.Segment.QueryGroup, 60); err != nil || got.Segment.Start != 60 {
		t.Fatalf("restart schedule=(%#v, %v)", got, err)
	}
}

func TestRedisCatalogRuntimeCutoverUsesOneHalfOpenTimelineAndNewGrid(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:cutover", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := validCatalog(t, 80)
	oldSnapshot, _, err := repository.PublishCatalog(context.Background(), oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	oldOpen := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, nil)
	oldActivation := activationState(t, 1, oldSnapshot, oldOpen, nil)
	if err := repository.CompareAndSetInitialScheduleActivation(context.Background(), controlplane.ActivationExpectation{}, oldActivation,
		[]execution.InitialScheduleActivationFact{{Segment: oldOpen.Segment}}); err != nil {
		t.Fatal(err)
	}

	newCatalog := catalogWithSchedule(t, oldCatalog, 120, 30)
	newSnapshot, _, err := repository.PublishCatalog(context.Background(), newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	boundary := execution.EvaluationTime(180)
	oldClosed := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, &boundary)
	newOpen := frozenSchedule(t, newSnapshot.Publication, newCatalog.QueryGroups[0], boundary, nil)
	newActivation := activationState(t, 2, newSnapshot, newOpen, nil)
	cutover := execution.ScheduleCutoverFact{OldSegment: oldClosed.Segment, NewSegment: newOpen.Segment}
	if err := repository.CompareAndSetScheduleCutover(context.Background(), controlplane.ActivationExpectation{
		RecordRevision: oldActivation.RecordRevision, Current: oldActivation.Current,
	}, newActivation, []execution.ScheduleCutoverFact{cutover}); err != nil {
		t.Fatal(err)
	}

	compiler, stateSemantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	oldLoaded, err := runtime.ReadFrozenSchedule(context.Background(), oldClosed.Segment.QueryGroup, 120)
	if err != nil || oldLoaded.Segment.End == nil || *oldLoaded.Segment.End != boundary {
		t.Fatalf("old schedule=(%#v, %v)", oldLoaded, err)
	}
	newLoaded, err := runtime.ReadFrozenSchedule(context.Background(), newOpen.Segment.QueryGroup, boundary)
	if err != nil || newLoaded.Segment.Start != boundary {
		t.Fatalf("new schedule=(%#v, %v)", newLoaded, err)
	}
	first, ok := newLoaded.FirstSlot()
	if !ok || first != 270 {
		t.Fatalf("new first Slot=(%d, %t), want 270", first, ok)
	}
	next, err := runtime.NextSlotAfter(context.Background(), oldClosed.Segment.QueryGroup, 120)
	if err != nil || next != 270 {
		t.Fatalf("next across cutover=(%d, %v), want 270", next, err)
	}
}

func TestRedisCatalogRepositoryRejectsInitialActivationWithoutScheduleForEveryPlan(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:initial-coverage", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	schedule := frozenSchedule(t, snapshot.Publication, catalog.QueryGroups[0], 60, nil)
	activation := activationState(t, 1, snapshot, schedule, nil)
	extra := activation.Plans[0]
	extra.Fact.Plan.StrategyID = "9999"
	extra.Fact.Selected.Identity = extra.Fact.Plan
	activation.Plans = append(activation.Plans, extra)

	err = repository.CompareAndSetInitialScheduleActivation(context.Background(), controlplane.ActivationExpectation{}, activation,
		[]execution.InitialScheduleActivationFact{{Segment: schedule.Segment}})
	if err == nil || !strings.Contains(err.Error(), "exactly cover") {
		t.Fatalf("incomplete initial Schedule coverage error=%v", err)
	}
}

func TestRedisCatalogRepositoryRejectsCutoverChangingPlanOutsideAffectedQueryGroup(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:cutover-coverage", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := twoQueryGroupCatalog(t)
	oldSnapshot, _, err := repository.PublishCatalog(context.Background(), oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	oldSchedules := []execution.FrozenQueryGroupSchedule{
		frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, nil),
		frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[1], 60, nil),
	}
	oldActivation := activationState(t, 1, oldSnapshot, oldSchedules[0], nil, oldSchedules[1:]...)
	initial := []execution.InitialScheduleActivationFact{{Segment: oldSchedules[0].Segment}, {Segment: oldSchedules[1].Segment}}
	if err := repository.CompareAndSetInitialScheduleActivation(context.Background(), controlplane.ActivationExpectation{}, oldActivation, initial); err != nil {
		t.Fatal(err)
	}

	newCatalog := catalogWithSchedule(t, oldCatalog, 120, 30)
	newSnapshot, _, err := repository.PublishCatalog(context.Background(), newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	boundary := execution.EvaluationTime(180)
	oldClosed := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, &boundary)
	newOpen := frozenSchedule(t, newSnapshot.Publication, newCatalog.QueryGroups[0], boundary, nil)
	newGroupActivation := activationState(t, 2, newSnapshot, newOpen, nil)
	newActivation := oldActivation
	newActivation.RecordRevision = 2
	newActivation.Pending = &newSnapshot.Publication
	newGroupActivation.Plans[0].Fact.Selection = execution.ActivationPending
	newActivation.Plans[0] = newGroupActivation.Plans[0]
	// This valid-looking mutation belongs to the other Query Group and must not
	// be smuggled into the same CAS without its own persisted Segment fact.
	newActivation.Plans[1].Fact.Selected.StateApplyEpoch++

	err = repository.CompareAndSetScheduleCutover(context.Background(), controlplane.ActivationExpectation{
		RecordRevision: oldActivation.RecordRevision, Current: oldActivation.Current,
	}, newActivation, []execution.ScheduleCutoverFact{{OldSegment: oldClosed.Segment, NewSegment: newOpen.Segment}})
	if err == nil || !strings.Contains(err.Error(), "outside affected Query Groups") {
		t.Fatalf("unrelated activation mutation error=%v", err)
	}
}

func validCatalog(t *testing.T, threshold int) controlplane.Catalog {
	t.Helper()
	document := realThresholdDocuments(t)[0]
	document = []byte(strings.Replace(string(document), `"threshold":80`, `"threshold":`+strconv.Itoa(threshold), 1))
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func sharedQueryGroupCatalog(t *testing.T, includeA, includeB bool) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	strategies := make([]controlplane.SourceStrategy, 0, 2)
	if includeA {
		strategies = append(strategies, controlplane.SourceStrategy{SourceID: "1001", Document: documents[0],
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}})
	}
	if includeB {
		strategies = append(strategies, controlplane.SourceStrategy{SourceID: "1002", Document: documents[1],
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}})
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: strategies,
		Planner:    &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 {
		t.Fatalf("shared Query Group catalog=%#v", catalog)
	}
	return catalog
}

func activationRecordByStrategy(
	t *testing.T,
	state controlplane.ActivationState,
	strategyID string,
) controlplane.PlanActivationRecord {
	t.Helper()
	for _, record := range state.Plans {
		if record.Fact.Plan.StrategyID == strategyID {
			return record
		}
	}
	t.Fatalf("missing activation for strategy %s: %#v", strategyID, state.Plans)
	return controlplane.PlanActivationRecord{}
}

func frozenSchedule(
	t *testing.T,
	publication controlplane.SnapshotPublicationRef,
	group controlplane.QueryGroup,
	start execution.EvaluationTime,
	end *execution.EvaluationTime,
) execution.FrozenQueryGroupSchedule {
	t.Helper()
	plans := make([]execution.FrozenPlanSchedule, len(group.Plans))
	for index, plan := range group.Plans {
		plans[index] = execution.FrozenPlanSchedule{
			Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec,
		}
	}
	return execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{
		Publication: execution.SnapshotPublicationRef{
			SnapshotRevision: publication.SnapshotRevision, PublicationEpoch: execution.PublicationEpoch(publication.PublicationEpoch),
		},
		QueryGroup: group.Identity, QueryRevision: group.QueryPlan.QueryRevision,
		ScheduleRevision: group.ScheduleRevision, Start: start, End: end,
	}, Plans: plans}
}

func activationState(
	t *testing.T,
	revision uint64,
	snapshot controlplane.PublishedSnapshot,
	firstSchedule execution.FrozenQueryGroupSchedule,
	pending *controlplane.SnapshotPublicationRef,
	additionalSchedules ...execution.FrozenQueryGroupSchedule,
) controlplane.ActivationState {
	t.Helper()
	publication := snapshot.Publication
	compiler, stateSemantics := runtimePlanCompiler(t)
	snapshotPlanByID := make(map[execution.PlanIdentity]controlplane.FrozenPlan)
	datasetByPlanID := make(map[execution.PlanIdentity]contract.DatasetContractV2)
	for _, group := range snapshot.QueryGroups {
		for _, plan := range group.Plans {
			snapshotPlanByID[plan.Identity] = plan
			datasetByPlanID[plan.Identity] = group.QueryPlan.Normalization.DatasetContract
		}
	}
	schedules := append([]execution.FrozenQueryGroupSchedule{firstSchedule}, additionalSchedules...)
	records := make([]controlplane.PlanActivationRecord, 0)
	for _, schedule := range schedules {
		for _, planSchedule := range schedule.Plans {
			plan := snapshotPlanByID[planSchedule.Identity]
			result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
				Plan: plan.Plan, DatasetContract: datasetByPlanID[planSchedule.Identity],
				StateSemantics: stateSemantics,
			})
			if err != nil {
				t.Fatal(err)
			}
			compiled, ok := result.Plan()
			if !ok {
				t.Fatalf("plan did not compile: terminal=%#v levels=%#v", result.PlanTerminal(), result.LevelTerminals())
			}
			fact := execution.PlanActivationFact{Plan: plan.Identity, Selection: execution.ActivationCurrent,
				Selected: execution.ActivatedPlan{Identity: plan.Identity,
					StateGeneration:  execution.StateGeneration(compiled.StateCompatibilityHash()),
					StateApplyEpoch:  execution.StateApplyEpoch(publication.PublicationEpoch),
					ScheduleRevision: plan.ScheduleRevision, RequiredFullSlots: 2}}
			records = append(records, controlplane.PlanActivationRecord{Fact: fact, Publication: publication})
		}
	}
	return controlplane.ActivationState{RecordRevision: revision, Current: publication, Pending: pending, Plans: records}
}

func twoQueryGroupCatalog(t *testing.T) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	var secondValue map[string]any
	if err := json.Unmarshal(documents[1], &secondValue); err != nil {
		t.Fatal(err)
	}
	secondValue["bk_biz_id"] = 3
	secondValue["space_uid"] = "bkcc__3"
	second, err := json.Marshal(secondValue)
	if err != nil {
		t.Fatal(err)
	}
	planner := queryPlannerFunc(func(_ context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
		return queryFactsFor(t, source.Identity.BusinessID, source.Identity.SpaceScope), nil
	})
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{
			{SourceID: "1001", Document: documents[0], Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
			{SourceID: "1002", Document: second, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}},
		},
		Planner: planner,
	})
	if err != nil || len(catalog.QueryGroups) != 2 {
		t.Fatalf("two Query Group catalog=(%#v, %v)", catalog, err)
	}
	return catalog
}

func catalogWithQueryTable(t *testing.T, tableID string) controlplane.Catalog {
	t.Helper()
	document := realThresholdDocuments(t)[0]
	planner := queryPlannerFunc(func(context.Context, controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
		facts := queryFacts(t)
		facts.QueryRevision = ""
		facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
		facts.QueryList[0].TableID = tableID
		return execution.BuildQueryPlanFacts(facts)
	})
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: planner,
	})
	if err != nil || len(catalog.QueryGroups) != 1 {
		t.Fatalf("query table catalog=(%#v,%v)", catalog, err)
	}
	return catalog
}

type queryPlannerFunc func(context.Context, controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error)

type activationProgressReader struct {
	byGroup map[execution.QueryGroupIdentity]execution.ProgressLoadResult
}

func (reader *activationProgressReader) LoadProgress(
	_ context.Context,
	identity execution.ProgressIdentity,
) (execution.ProgressLoadResult, error) {
	result, ok := reader.byGroup[identity.QueryGroup]
	if !ok {
		return execution.ProgressLoadResult{}, errors.New("missing activation Progress fixture")
	}
	return result, nil
}

func (planner queryPlannerFunc) CompilePrimaryQuery(ctx context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
	return planner(ctx, source)
}

func catalogWithSchedule(t *testing.T, source controlplane.Catalog, interval int64, alignment execution.EvaluationTime) controlplane.Catalog {
	t.Helper()
	result := source
	result.QueryGroups = append([]controlplane.QueryGroup(nil), source.QueryGroups...)
	group := result.QueryGroups[0]
	group.Plans = append([]controlplane.FrozenPlan(nil), group.Plans...)
	for index := range group.Plans {
		group.Plans[index].Plan.StrategyIR.ExecutionSemantics.EvaluationInterval = uint32(interval)
		group.Plans[index].PlanRevision = mustDigest(t, "alarmd-plan-semantics-v1", group.Plans[index].Plan)
		group.Plans[index].ScheduleSpec = execution.ScheduleSpec{
			EvaluationIntervalSeconds: interval, Alignment: alignment, Timezone: "UTC",
		}
		revision, err := execution.DerivePlanScheduleRevision(group.Plans[index].ScheduleSpec)
		if err != nil {
			t.Fatal(err)
		}
		group.Plans[index].ScheduleRevision = revision
	}
	schedules := make([]execution.FrozenPlanSchedule, len(group.Plans))
	for index, plan := range group.Plans {
		schedules[index] = execution.FrozenPlanSchedule{Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec}
	}
	group.ScheduleRevision, _ = execution.DeriveQueryGroupScheduleRevision(schedules)
	result.QueryGroups[0] = group
	result.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", result.QueryGroups))
	return result
}

func runtimePlanCompiler(t *testing.T) (*strategy.PlanCompiler, strategy.StateSemantics) {
	t.Helper()
	return runtimePlanCompilerWithLimits(t, 16, 4096)
}

func runtimePlanCompilerWithLimits(
	t *testing.T,
	maxLevels int,
	maxRecoveryWindows uint32,
) (*strategy.PlanCompiler, strategy.StateSemantics) {
	t.Helper()
	return runtimePlanCompilerWithBudgets(t, 64<<10, maxLevels, maxRecoveryWindows)
}

func runtimePlanCompilerWithBudgets(
	t *testing.T,
	maxPlanBytes int,
	maxLevels int,
	maxRecoveryWindows uint32,
) (*strategy.PlanCompiler, strategy.StateSemantics) {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: maxPlanBytes, MaxLevelsPerPlan: maxLevels, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: maxRecoveryWindows, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return compiler, strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1",
		IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1",
		HistoryCellSemanticsVersion: "detect-history-cell-v1"}
}

func runtimeCompileIsolationDocuments(t *testing.T) []json.RawMessage {
	t.Helper()
	documents := realThresholdDocuments(t)
	partial := addThresholdLevels(t, documents[0], []uint32{2}, []uint32{2})
	allLevelsTerminal := documents[1]
	planTerminal := addThresholdLevels(t, documents[0], []uint32{2, 3}, []uint32{1, 1})
	var planTerminalValue map[string]any
	if err := json.Unmarshal(planTerminal, &planTerminalValue); err != nil {
		t.Fatal(err)
	}
	planTerminalValue["id"] = float64(1003)
	planTerminalValue["bk_biz_id"] = float64(3)
	planTerminalValue["space_uid"] = "bkcc__3"
	items := planTerminalValue["items"].([]any)
	items[0].(map[string]any)["id"] = float64(13)
	planTerminal, err := json.Marshal(planTerminalValue)
	if err != nil {
		t.Fatal(err)
	}
	return []json.RawMessage{partial, allLevelsTerminal, planTerminal}
}

type revisionTerminalCompiler struct {
	normal        *strategy.PlanCompiler
	strict        *strategy.PlanCompiler
	invalid       map[string]struct{}
	unsupported   map[string]struct{}
	mixedLevels   map[string]struct{}
	mergedInvalid map[string]struct{}
}

func (compiler *revisionTerminalCompiler) Compile(
	ctx context.Context,
	request strategy.CompileRequest,
) (strategy.CompileResult, error) {
	revision := request.Plan.StrategyRef.Revision
	if _, invalid := compiler.invalid[revision]; invalid {
		request.Plan.StrategyIR.Schema.Name = "invalid-strategy-ir"
		return compiler.normal.Compile(ctx, request)
	}
	if _, unsupported := compiler.unsupported[revision]; unsupported {
		return compiler.strict.Compile(ctx, request)
	}
	if _, mixed := compiler.mixedLevels[revision]; mixed && len(request.Plan.StrategyIR.Levels) == 3 {
		for index := range request.Plan.StrategyIR.Levels {
			level := &request.Plan.StrategyIR.Levels[index]
			switch level.Definition.LevelID {
			case 2:
				level.TriggerPlan.Type = "INVALID_TRIGGER"
			case 3:
				level.DetectPlan.Algorithms[0].Type = "UnsupportedForTest"
			}
		}
	}
	if _, invalid := compiler.mergedInvalid[revision]; invalid && len(request.Plan.StrategyIR.Levels) == 2 {
		request.Plan.StrategyIR.Schema.Name = "invalid-strategy-ir"
	}
	return compiler.normal.Compile(ctx, request)
}

func withStrategyUpdateTime(t *testing.T, document json.RawMessage, updateTime int64) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	value["update_time"] = float64(updateTime)
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func withThresholdForLevel(t *testing.T, document json.RawMessage, levelID uint32, threshold int) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	algorithms := value["items"].([]any)[0].(map[string]any)["algorithms"].([]any)
	for _, raw := range algorithms {
		algorithm := raw.(map[string]any)
		if uint32(algorithm["level"].(float64)) != levelID {
			continue
		}
		groups := algorithm["config"].([]any)
		conditions := groups[0].([]any)
		conditions[0].(map[string]any)["threshold"] = float64(threshold)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func withBusinessScope(t *testing.T, document json.RawMessage, businessID int, spaceScope string) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	value["bk_biz_id"] = float64(businessID)
	value["space_uid"] = spaceScope
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func levelIRByID(plan contract.EvaluationPlanV2) map[uint32]contract.LevelIRV2 {
	result := make(map[uint32]contract.LevelIRV2, len(plan.StrategyIR.Levels))
	for _, level := range plan.StrategyIR.Levels {
		result[level.Definition.LevelID] = level
	}
	return result
}

func addThresholdLevels(
	t *testing.T,
	document json.RawMessage,
	levels []uint32,
	recoveryWindows []uint32,
) json.RawMessage {
	t.Helper()
	if len(levels) != len(recoveryWindows) {
		t.Fatal("level fixtures must have one recovery window per Level")
	}
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	item := value["items"].([]any)[0].(map[string]any)
	algorithms := item["algorithms"].([]any)
	detects := value["detects"].([]any)
	for index, levelID := range levels {
		algorithm := cloneJSONMap(t, algorithms[0])
		algorithm["level"] = float64(levelID)
		algorithms = append(algorithms, algorithm)
		detect := cloneJSONMap(t, detects[0])
		detect["level"] = float64(levelID)
		detect["recovery_config"] = map[string]any{"check_window": float64(recoveryWindows[index])}
		detects = append(detects, detect)
	}
	item["algorithms"] = algorithms
	value["detects"] = detects
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func cloneJSONMap(t *testing.T, source any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertNoAcceptedPlanDisposition(
	t *testing.T,
	repository *controlplane.RedisCatalogRepository,
	sourceID string,
) {
	t.Helper()
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range audit.Dispositions {
		if item.SourceID == sourceID && item.Scope == "PLAN" && item.Disposition == controlplane.DispositionAccepted {
			t.Fatalf("unexpected accepted Plan disposition source=%s: %#v", sourceID, audit)
		}
	}
}

func mustDigest(t *testing.T, domain string, value any) string {
	t.Helper()
	digest, err := contract.DeriveCanonicalDigestV2(domain, value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func newRedisStrategySource(t *testing.T, client redis.Cmdable) *controlplane.LegacyRedisStrategySource {
	t.Helper()
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func plansByStrategy(snapshot controlplane.PublishedSnapshot) map[string]controlplane.FrozenPlan {
	result := make(map[string]controlplane.FrozenPlan)
	for _, group := range snapshot.QueryGroups {
		for _, plan := range group.Plans {
			result[plan.Identity.StrategyID] = plan
		}
	}
	return result
}

func assertAuditDisposition(
	t *testing.T,
	repository *controlplane.RedisCatalogRepository,
	sourceID string,
	disposition controlplane.Disposition,
	reason string,
) {
	t.Helper()
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range audit.Dispositions {
		if item.SourceID == sourceID && item.Disposition == disposition && item.Reason == reason {
			return
		}
	}
	t.Fatalf("missing audit disposition source=%s disposition=%s reason=%s: %#v", sourceID, disposition, reason, audit)
}

func assertAuditDispositionExact(
	t *testing.T,
	repository *controlplane.RedisCatalogRepository,
	sourceID string,
	scope string,
	levelID uint32,
	disposition controlplane.Disposition,
	reason string,
) {
	t.Helper()
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range audit.Dispositions {
		if item.SourceID == sourceID && item.Scope == scope && item.LevelID == levelID &&
			item.Disposition == disposition && item.Reason == reason {
			return
		}
	}
	t.Fatalf("missing exact audit disposition source=%s scope=%s level=%d disposition=%s reason=%s: %#v",
		sourceID, scope, levelID, disposition, reason, audit)
}

func assertNoAuditDisposition(
	t *testing.T,
	repository *controlplane.RedisCatalogRepository,
	sourceID string,
	disposition controlplane.Disposition,
) {
	t.Helper()
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range audit.Dispositions {
		if item.SourceID == sourceID && item.Disposition == disposition {
			t.Fatalf("unexpected audit disposition source=%s disposition=%s: %#v", sourceID, disposition, audit)
		}
	}
}

func newControlplaneRedis(t *testing.T) *redis.Client {
	t.Helper()
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "--bind", "127.0.0.1", "--port", portText,
		"--save", "", "--appendonly", "no", "--dir", t.TempDir(), "--daemonize", "no", "--loglevel", "warning")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() {
		_ = client.Close()
		if command.ProcessState == nil || !command.ProcessState.Exited() {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if client.Ping(context.Background()).Err() == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("redis-server did not become ready: %s", output.String())
	return nil
}
