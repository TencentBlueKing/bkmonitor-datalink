// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

type phaseTwoScanCountingHook struct {
	count atomic.Int64
}

func (hook *phaseTwoScanCountingHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() == "scan" {
		hook.count.Add(1)
	}
	return ctx, nil
}

func (*phaseTwoScanCountingHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (*phaseTwoScanCountingHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (*phaseTwoScanCountingHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func TestProductionPhaseTwoBundleUsesCanonicalSourceAndRuntimeRedisOverride(t *testing.T) {
	sourceAddress, sourceClient := startPhaseTwoRedis(t)
	runtimeAddress, runtimeClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := sourceClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = sourceAddress
	cfg.Redis.StatePrefix = "alarmd:phase-two:g1:v1"
	runtimeRedis := cfg.Redis.Connection()
	runtimeRedis.Address = runtimeAddress
	cfg.PhaseTwo.RuntimeRedis = &runtimeRedis
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL

	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: uqServer.Client(),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	if len(bundle.queryGroups) != 1 {
		t.Fatalf("selected strategies compiled to Query Groups = %v, want exactly one", bundle.queryGroups)
	}
	sourceKeys, err := sourceClient.Keys(ctx, cfg.Redis.StatePrefix+"*").Result()
	if err != nil || len(sourceKeys) != 0 {
		t.Fatalf("top-level StrategySource Redis contains runtime keys %v, error=%v", sourceKeys, err)
	}
	runtimeKeys, err := runtimeClient.Keys(ctx, cfg.Redis.StatePrefix+"*").Result()
	if err != nil || len(runtimeKeys) == 0 {
		t.Fatalf("runtime Redis keys = %v, error=%v, want Go-owned facts", runtimeKeys, err)
	}
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatalf("phase-two production Shutdown() error = %v", err)
	}
}

func TestProductionPhaseTwoBundleStartsIdleWithEmptyCatalogThenActivatesQueryGroup(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd:phase-two:empty-catalog"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL

	var observationsMu sync.Mutex
	var observations []observability.Observation
	health := newPhaseTwoApplicationHealth()
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), health,
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observationsMu.Lock()
				defer observationsMu.Unlock()
				observations = append(observations, observation)
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production empty Catalog Start() error = %v", err)
	}
	if len(bundle.queryGroups) != 0 || len(bundle.runners) != 0 {
		t.Fatalf("empty Catalog runtime Query Groups/runners = %v/%d, want healthy idle", bundle.queryGroups, len(bundle.runners))
	}
	productionControl := bundle.dependencies.Control.(*productionPhaseTwoControl)
	activation, err := productionControl.dependencies.Repository.LoadActivation(ctx)
	if err != nil || activation.RecordRevision != 1 || len(activation.Plans) != 0 {
		t.Fatalf("empty Catalog activation = %+v, %v", activation, err)
	}
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("empty Catalog health = %+v, want ready idle Worker", snapshot)
	}

	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("first non-empty refresh error = %v", err)
	}
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("confirmed non-empty refresh error = %v", err)
	}
	if len(bundle.queryGroups) != 1 || len(bundle.runners) != 1 {
		t.Fatalf("activated Query Groups/runners = %v/%d, want one", bundle.queryGroups, len(bundle.runners))
	}
	activation, err = productionControl.dependencies.Repository.LoadActivation(ctx)
	if err != nil || activation.RecordRevision != 2 || len(activation.Plans) != 1 {
		t.Fatalf("non-empty Catalog activation = %+v, %v", activation, err)
	}
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("post-activation health = %+v, want ready", snapshot)
	}
	observationsMu.Lock()
	for _, observation := range observations {
		if observation.Stage == observability.Stage(observability.StageFatal) {
			observationsMu.Unlock()
			t.Fatalf("legal empty Catalog became process fatal: %+v", observation)
		}
	}
	observationsMu.Unlock()
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatalf("phase-two production Shutdown() error = %v", err)
	}
}

func TestProductionPhaseTwoBundleRebuildsExpiredSnapshotReferencedByPersistentActivation(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd:phase-two:expired-snapshot"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	openBundle := func(health *phaseTwoApplicationHealth) *phaseTwoWorkerBundle {
		bundle, openErr := openProductionPhaseTwoBundleWithDependencies(
			ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), health,
			func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
				return controlplane.NewLegacyRedisStrategySource(client, prefix)
			},
			phaseTwoProductionExternalDependencies{
				Now: time.Now, HTTPClient: uqServer.Client(),
				OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
					return &recordingPhaseTwoEventSink{}, nil
				},
			},
		)
		if openErr != nil {
			t.Fatalf("open production bundle: %v", openErr)
		}
		return bundle
	}

	first := openBundle(newPhaseTwoApplicationHealth())
	if err := first.Start(ctx); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	firstControl := first.dependencies.Control.(*productionPhaseTwoControl)
	oldActivation, err := firstControl.dependencies.Repository.LoadActivation(ctx)
	if err != nil || oldActivation.Current.SnapshotRevision == "" {
		t.Fatalf("first activation = %+v, %v", oldActivation, err)
	}
	catalogPrefix := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog")
	activationKey := catalogPrefix + ":activation"
	scanHook := &phaseTwoScanCountingHook{}
	redisClient.AddHook(scanHook)
	if groups, loadErr := firstControl.LoadActive(ctx); loadErr != nil || len(groups.QueryGroups) != 1 {
		t.Fatalf("v2 follower LoadActive()=(%+v,%v)", groups, loadErr)
	}
	originalActivation, err := redisClient.Get(ctx, activationKey).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	legacyActivation := oldActivation
	legacyActivation.SchemaVersion = "alarmd-control-activation-v1"
	legacyActivation.ActiveQGSetRef = controlplane.ActiveQueryGroupSetRef{}
	legacyPayload, err := json.Marshal(legacyActivation)
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, activationKey, legacyPayload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if groups, loadErr := firstControl.LoadActive(ctx); loadErr != nil || len(groups.QueryGroups) != 1 {
		t.Fatalf("v1 follower LoadActive()=(%+v,%v)", groups, loadErr)
	}
	if got := scanHook.count.Load(); got != 0 {
		t.Fatalf("v1/v2 follower LoadActive SCAN calls=%d, want 0", got)
	}
	if err := redisClient.Set(ctx, activationKey, originalActivation, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown() error = %v", err)
	}

	catalogKeys, err := redisClient.Keys(ctx, catalogPrefix+":*").Result()
	if err != nil {
		t.Fatal(err)
	}
	activationHeaderKey := catalogPrefix + ":activation_header"
	publicationEpochKey := catalogPrefix + ":publication_epoch"
	revision := string(oldActivation.Current.SnapshotRevision)
	snapshotKey := catalogPrefix + ":snapshot:" + revision
	snapshotEpochKey := catalogPrefix + ":snapshot_epoch:" + revision
	latestPublicationKey := catalogPrefix + ":latest_publication"
	occurrenceKey := catalogPrefix + ":publication:" + strconv.FormatUint(oldActivation.Current.PublicationEpoch, 10)
	activeQGSetKey := catalogPrefix + ":active_qg_set:" + oldActivation.ActiveQGSetRef.Digest
	for _, key := range catalogKeys {
		if key == activationKey || key == activationHeaderKey || key == publicationEpochKey ||
			key == snapshotEpochKey || key == latestPublicationKey || key == occurrenceKey || key == activeQGSetKey {
			continue
		}
		if err := redisClient.Del(ctx, key).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if exists, err := redisClient.Exists(ctx, activationKey).Result(); err != nil || exists != 1 {
		t.Fatalf("persistent activation exists=%d, error=%v", exists, err)
	}
	ownershipKeys, err := redisClient.Keys(ctx, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership")+":*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(ownershipKeys) > 0 {
		if err := redisClient.Del(ctx, ownershipKeys...).Err(); err != nil {
			t.Fatal(err)
		}
	}

	recoveredHealth := newPhaseTwoApplicationHealth()
	recovered := openBundle(recoveredHealth)
	if err := recovered.Start(ctx); err != nil {
		t.Fatalf("recovered Start() error = %v", err)
	}
	defer func() { _ = recovered.Shutdown(ctx) }()
	if snapshot := recoveredHealth.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("expired Snapshot bootstrap health=%+v, want ready after bounded rebuild", snapshot)
	}
	recovered.mu.RLock()
	recoveredLeader := recovered.controlLeader
	recovered.mu.RUnlock()
	if !recoveredLeader {
		t.Fatal("recovered Worker did not acquire Control Leader after expired ownership facts were removed")
	}
	if err := recovered.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("refreshAndReconcile(rebuild expired Snapshot) error = %v", err)
	}
	recoveredControl := recovered.dependencies.Control.(*productionPhaseTwoControl)
	newActivation, err := recoveredControl.dependencies.Repository.LoadActivation(ctx)
	if err != nil {
		t.Fatalf("recovered activation error = %v", err)
	}
	if newActivation.Current != oldActivation.Current || newActivation.RecordRevision != oldActivation.RecordRevision {
		t.Fatalf("expired publication restoration mutated frozen Activation: old=%+v new=%+v",
			oldActivation, newActivation)
	}
	if _, err := recoveredControl.dependencies.Repository.LoadPublishedSnapshot(ctx, newActivation.Current); err != nil {
		t.Fatalf("recovered active Snapshot is unreadable: %v", err)
	}
	if len(recovered.queryGroups) != 1 || len(recovered.runners) != 1 {
		t.Fatalf("recovered Query Groups/runners=%v/%d, want one", recovered.queryGroups, len(recovered.runners))
	}
	if snapshot := recoveredHealth.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("recovered health=%+v, want ready", snapshot)
	}

	activationBeforePublishCollision, err := redisClient.Get(ctx, activationKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	latestBeforePublishCollision, err := redisClient.Get(ctx, latestPublicationKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	snapshotEpochBeforePublishCollision, err := redisClient.Get(ctx, snapshotEpochKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	publicationEpochBeforePublishCollision, err := redisClient.Get(ctx, publicationEpochKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Del(ctx, snapshotKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, occurrenceKey, "another-snapshot-revision", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	refreshSource := func() (controlplane.SourceRefreshResult, error) {
		return recoveredControl.dependencies.Reconciler.Refresh(ctx,
			recoveredControl.dependencies.Source, recoveredControl.dependencies.Planner)
	}
	if _, err := refreshSource(); !errors.Is(err, controlplane.ErrPublicationOccurrenceCollision) {
		t.Fatalf("latest occurrence collision error = %v, want collision", err)
	}
	if exists, err := redisClient.Exists(ctx, snapshotKey).Result(); err != nil || exists != 0 {
		t.Fatalf("latest occurrence collision recreated Snapshot: exists=%d error=%v", exists, err)
	}
	for key, want := range map[string]string{
		latestPublicationKey: latestBeforePublishCollision,
		snapshotEpochKey:     snapshotEpochBeforePublishCollision,
		occurrenceKey:        "another-snapshot-revision",
		activationKey:        activationBeforePublishCollision,
		publicationEpochKey:  publicationEpochBeforePublishCollision,
	} {
		if got, err := redisClient.Get(ctx, key).Result(); err != nil || got != want {
			t.Fatalf("latest occurrence collision mutated %s: got=%q want=%q error=%v", key, got, want, err)
		}
	}
	if err := redisClient.Set(ctx, occurrenceKey, revision, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := recoveredControl.Refresh(ctx); err != nil {
		t.Fatalf("restore after latest occurrence collision: %v", err)
	}

	activationBeforeCollision, err := redisClient.Get(ctx, activationKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	catalogKeys, err = redisClient.Keys(ctx, catalogPrefix+":*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range catalogKeys {
		if key == activationKey || key == activationHeaderKey || key == publicationEpochKey {
			continue
		}
		if err := redisClient.Del(ctx, key).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := redisClient.Set(ctx, occurrenceKey, "another-snapshot-revision", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshSource(); !errors.Is(err, controlplane.ErrPublicationOccurrenceCollision) {
		t.Fatalf("conflicting occurrence recovery error = %v, want collision", err)
	}
	if exists, err := redisClient.Exists(ctx, snapshotKey, latestPublicationKey, snapshotEpochKey).Result(); err != nil || exists != 0 {
		t.Fatalf("collision mutated Snapshot/latest/revision epoch facts: exists=%d error=%v", exists, err)
	}
	if occurrence, err := redisClient.Get(ctx, occurrenceKey).Result(); err != nil || occurrence != "another-snapshot-revision" {
		t.Fatalf("collision mutated immutable occurrence=%q error=%v", occurrence, err)
	}
	if activationAfterCollision, err := redisClient.Get(ctx, activationKey).Result(); err != nil ||
		activationAfterCollision != activationBeforeCollision {
		t.Fatalf("collision mutated Activation: changed=%t error=%v",
			activationAfterCollision != activationBeforeCollision, err)
	}
}

func TestProductionPhaseTwoBundleActivatesLatestSnapshotWhenPreviousPayloadExpired(t *testing.T) {
	tests := []struct {
		name                 string
		oldValue             string
		newValue             string
		incompleteActivation bool
		wantRecovery         bool
		revertToOld          bool
	}{
		{name: "same Query Group exact activation coverage", oldValue: `"threshold": 80`, newValue: `"threshold": 81`, wantRecovery: true},
		{name: "Query Group identity changed", oldValue: `"result_table_id": "system.cpu"`, newValue: `"result_table_id": "system.cpu.changed"`, wantRecovery: true},
		{name: "activation Plan coverage incomplete", oldValue: `"threshold": 80`, newValue: `"threshold": 81`, incompleteActivation: true},
		{name: "live source returns to active revision while latest differs", oldValue: `"threshold": 80`, newValue: `"threshold": 81`, wantRecovery: true, revertToOld: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testProductionPhaseTwoStrandedLatest(t, test.oldValue, test.newValue, test.incompleteActivation, test.wantRecovery, test.revertToOld)
		})
	}
}

func testProductionPhaseTwoStrandedLatest(
	t *testing.T,
	oldValue string,
	newValue string,
	incompleteActivation bool,
	wantRecovery bool,
	revertToOld bool,
) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd:phase-two:stranded-latest"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	var nowUnix atomic.Int64
	nowUnix.Store(time.Now().Unix())
	openBundle := func(health *phaseTwoApplicationHealth) *phaseTwoWorkerBundle {
		bundle, openErr := openProductionPhaseTwoBundleWithDependencies(
			ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), health,
			func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
				return controlplane.NewLegacyRedisStrategySource(client, prefix)
			},
			phaseTwoProductionExternalDependencies{
				Now: func() time.Time { return time.Unix(nowUnix.Load(), 0) }, HTTPClient: uqServer.Client(),
				OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
					return &recordingPhaseTwoEventSink{}, nil
				},
			},
		)
		if openErr != nil {
			t.Fatalf("open production bundle: %v", openErr)
		}
		return bundle
	}

	first := openBundle(newPhaseTwoApplicationHealth())
	if err := first.Start(ctx); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	firstControl := first.dependencies.Control.(*productionPhaseTwoControl)
	oldActivation, err := firstControl.dependencies.Repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(strategyDocument, []byte(oldValue), []byte(newValue), 1)
	if bytes.Equal(changed, strategyDocument) {
		t.Fatal("test strategy threshold did not change")
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", changed, 0).Err(); err != nil {
		t.Fatal(err)
	}
	refreshSource := func() controlplane.SourceRefreshResult {
		result, refreshErr := firstControl.dependencies.Reconciler.Refresh(
			ctx, firstControl.dependencies.Source, firstControl.dependencies.Planner,
		)
		if refreshErr != nil {
			t.Fatalf("source refresh error = %v", refreshErr)
		}
		return result
	}
	if result := refreshSource(); result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("first changed source refresh = %+v, want pending", result)
	}
	latest := refreshSource()
	if latest.Status != controlplane.SourceRefreshPublished || latest.Publication == oldActivation.Current {
		t.Fatalf("confirmed changed source refresh = %+v, want a new publication", latest)
	}
	defer func() { _ = first.Shutdown(ctx) }()
	nowUnix.Add(2)
	if revertToOld {
		if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}

	catalogPrefix := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog")
	oldSnapshot, err := firstControl.dependencies.Repository.LoadPublishedSnapshot(ctx, oldActivation.Current)
	if err != nil || len(oldSnapshot.QueryGroups) != 1 {
		t.Fatalf("old Snapshot=(%+v,%v), want one Query Group", oldSnapshot, err)
	}
	latestSnapshot, err := firstControl.dependencies.Repository.LoadPublishedSnapshot(ctx, latest.Publication)
	if err != nil || len(latestSnapshot.QueryGroups) != 1 {
		t.Fatalf("latest Snapshot=(%+v,%v), want one Query Group", latestSnapshot, err)
	}
	if !wantRecovery && !incompleteActivation && oldSnapshot.QueryGroups[0].Identity == latestSnapshot.QueryGroups[0].Identity {
		t.Fatalf("negative identity case kept Query Group identity %q", oldSnapshot.QueryGroups[0].Identity)
	}
	if incompleteActivation {
		incomplete := oldActivation
		extra := incomplete.Plans[0]
		extra.Fact.Plan.StrategyID = "2002"
		extra.Fact.Selected.Identity = extra.Fact.Plan
		incomplete.Plans = append(incomplete.Plans, extra)
		payload, marshalErr := json.Marshal(incomplete)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := redisClient.Set(ctx, catalogPrefix+":activation", payload, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	var activationBefore controlplane.ActivationState
	var scheduleBefore string
	scheduleKey := catalogPrefix + ":schedule_timeline:" + string(oldSnapshot.QueryGroups[0].Identity)
	if !wantRecovery {
		activationBefore, err = firstControl.dependencies.Repository.LoadActivation(ctx)
		if err != nil {
			t.Fatal(err)
		}
		scheduleBefore, err = redisClient.Get(ctx, scheduleKey).Result()
		if err != nil {
			t.Fatal(err)
		}
	}
	oldSnapshotKey := catalogPrefix + ":snapshot:" + string(oldActivation.Current.SnapshotRevision)
	if err := redisClient.Del(ctx, oldSnapshotKey).Err(); err != nil {
		t.Fatal(err)
	}
	result, refreshErr := firstControl.Refresh(ctx)
	if wantRecovery {
		if refreshErr != nil || result.Status != phaseTwoControlHealthy || len(result.QueryGroups) != 1 {
			t.Fatalf("recovered Refresh()=(%+v,%v), want healthy latest activation", result, refreshErr)
		}
	} else if refreshErr != nil || result.Status != phaseTwoControlDegradedLastGood ||
		!errors.Is(result.Cause, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("rejected Refresh()=(%+v,%v), want Snapshot unavailable without activation", result, refreshErr)
	}
	activation, err := firstControl.dependencies.Repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if wantRecovery {
		wantPublication, wantRevision := latest.Publication, oldActivation.RecordRevision+1
		if revertToOld {
			wantPublication, wantRevision = oldActivation.Current, oldActivation.RecordRevision
		}
		if activation.Current != wantPublication || activation.RecordRevision != wantRevision {
			t.Fatalf("recovered activation = %+v, want publication %+v revision %d", activation, wantPublication, wantRevision)
		}
		if _, err := firstControl.dependencies.Repository.LoadPublishedSnapshot(ctx, activation.Current); err != nil {
			t.Fatalf("recovered current Snapshot is unreadable: %v", err)
		}
		if revertToOld {
			publication, err := redisClient.Get(ctx, catalogPrefix+":latest_publication").Result()
			want := fmt.Sprintf("%d\n%s", oldActivation.Current.PublicationEpoch, oldActivation.Current.SnapshotRevision)
			if err != nil || publication != want {
				t.Fatalf("recovered source publication=(%q,%v), want active occurrence %q", publication, err, want)
			}
		}
		if err := firstControl.dependencies.Repository.RenewCurrentActivationObjects(ctx); err != nil {
			t.Fatalf("recovered current objects cannot renew: %v", err)
		}
		if revertToOld {
			stable, stableErr := firstControl.Refresh(ctx)
			if stableErr != nil || stable.Status != phaseTwoControlHealthy || len(stable.QueryGroups) != 1 {
				t.Fatalf("recovered current objects did not remain healthy: result=%+v error=%v", stable, stableErr)
			}
		}
	} else {
		if !reflect.DeepEqual(activation, activationBefore) {
			t.Fatalf("rejected recovery mutated activation: got=%+v want=%+v", activation, activationBefore)
		}
		scheduleAfter, loadErr := redisClient.Get(ctx, scheduleKey).Result()
		if loadErr != nil || scheduleAfter != scheduleBefore {
			t.Fatalf("rejected recovery mutated Schedule timeline: before=%q after=%q error=%v",
				scheduleBefore, scheduleAfter, loadErr)
		}
	}
}

func TestProductionPhaseTwoBundleExecutesFullNonEmptySlotEndToEnd(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}

	base := time.Now().Unix()
	base -= base % 300
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	var uqCalls atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		uqCalls.Add(1)
		if request.URL.Path != "/query/ts" || request.Header.Get("Bk-Query-Source") != "alarmd" ||
			request.Header.Get("X-Bk-Tenant-Id") != "tenant-a" || request.Header.Get("X-Bk-Scope-Space-Uid") != "bkcc__2" {
			t.Errorf("unexpected UQ wire coordinates: path=%s headers=%v", request.URL.Path, request.Header)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode UQ request: %v", err)
		}
		if len(payload["query_list"].([]any)) != 1 {
			t.Errorf("UQ request query_list = %#v", payload["query_list"])
		}
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` +
			strconv.FormatInt((base-1)*1000, 10) + `,95]]}],"status":null,"trace_id":"g1-full-uq","is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g1-full"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	events := &recordingPhaseTwoEventSink{}
	var observationsMu sync.Mutex
	var observations []observability.Observation
	additionalObserver := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observationsMu.Lock()
		defer observationsMu.Unlock()
		observations = append(observations, observation)
	})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(), AdditionalObserver: additionalObserver,
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return events, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	clock.Store(base + 1)
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("phase-two production Slot error = %v", err)
	}
	if uqCalls.Load() != 1 {
		t.Fatalf("real UQ wire calls = %d, want 1", uqCalls.Load())
	}
	written := events.snapshot()
	if len(written) != 1 || written[0].EventKind != "ABNORMAL" || written[0].PlanRef.StrategyID != "1001" {
		t.Fatalf("TriggerEvent ACK fixture = %+v", written)
	}

	if len(bundle.queryGroups) != 1 {
		t.Fatalf("assigned Query Groups = %v", bundle.queryGroups)
	}
	queryGroup := bundle.queryGroups[0]
	productionOwnership, ok := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	if !ok {
		t.Fatalf("production ownership type = %T", bundle.dependencies.Ownership)
	}
	progress, err := productionOwnership.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: queryGroup})
	if err != nil || progress.Status != execution.ProgressFound || progress.Progress == nil ||
		progress.Progress.LastFullSlot != execution.EvaluationTime(base) ||
		progress.Progress.NextSlot != execution.EvaluationTime(base+300) {
		t.Fatalf("Redis Progress = %+v, error=%v", progress, err)
	}

	schedule, err := productionOwnership.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
	if err != nil {
		t.Fatal(err)
	}
	firstSlot, ok := schedule.FirstSlot()
	if !ok || firstSlot != execution.EvaluationTime(base) {
		t.Fatalf("initial frozen Slot = %d, %v", firstSlot, ok)
	}
	frozen, err := productionOwnership.dependencies.Catalog.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: queryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: firstSlot,
		DuePlans: schedule.DuePlanRefs(firstSlot),
	})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := state.NewRedisBackend(cfg.RedisBackendOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	router, err := state.NewFixedRouter("phase-two-primary", backend)
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := state.NewExecutionStore(state.ExecutionStoreOptions{
		Prefix: cfg.Redis.StatePrefix, Router: router, MaxValueBytes: cfg.Limits.Codec.MaxEncodedBytes,
		MaxItemsPerCall: cfg.Limits.Store.MaxKeysPerBatch, RuntimeTTL: cfg.Redis.MaxTTL.Duration(),
	})
	if err != nil {
		t.Fatal(err)
	}
	applyVersion, err := execution.BuildApplyVersion(frozen.Contract, 1)
	if err != nil {
		t.Fatal(err)
	}
	stateIdentity := execution.StateKeyIdentity{
		Plan: execution.PlanIdentity{
			TenantID: written[0].TenantID, BusinessID: written[0].BusinessID, StrategyID: written[0].PlanRef.StrategyID,
		},
		StateGeneration:      execution.StateGeneration(written[0].PlanRef.StateCompatibilityHash),
		SeriesIdentityDigest: execution.SeriesIdentityDigest(written[0].RecordRef.DimensionIdentityDigest),
	}
	stateResult, err := stateStore.LoadRuntime(ctx, execution.StatePreflightRequest{
		Contract: frozen.Contract, Items: []execution.StatePreflightItem{{Identity: stateIdentity, ApplyVersion: applyVersion}},
	})
	if err != nil || len(stateResult.Items) != 1 || stateResult.Items[0].BlobRevision == 0 ||
		(stateResult.Items[0].Status != execution.StateFoundReady && stateResult.Items[0].Status != execution.StateFoundWarming) {
		t.Fatalf("Redis Runtime State = %+v, error=%v", stateResult, err)
	}

	observationsMu.Lock()
	stages := observedStages(observations)
	observationsMu.Unlock()
	assertObservedOrder(t, stages, []observability.Stage{
		observability.StageSnapshotRefreshed, observability.StageAssignmentAcquired,
		observability.StageScheduleDue, observability.StageSlotStarted, observability.StageEvaluationCompleted,
		observability.StageQueryCompleted, observability.StageSideEffectAdmission,
		observability.StageEventACKed, observability.StageStateApplied,
		observability.StageProgressCommitted, observability.StageSlotCompleted,
	})
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatalf("phase-two production Shutdown() error = %v", err)
	}
	if !events.isClosed() {
		t.Fatal("production shutdown did not close TriggerEvent sink")
	}
}

func TestProductionPhaseTwoBundleKeepsHealthyQueryGroupWhenSiblingEventACKIsRetryable(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	failedDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var healthyStrategy map[string]any
	if err := json.Unmarshal(failedDocument, &healthyStrategy); err != nil {
		t.Fatal(err)
	}
	healthyStrategy["id"] = 1002
	item := healthyStrategy["items"].([]any)[0].(map[string]any)
	item["id"] = 12
	item["query_md5"] = "g3b-healthy-query-md5"
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["result_table_id"] = "system.mem"
	healthyDocument, err := json.Marshal(healthyStrategy)
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", failedDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1002", healthyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}

	base := time.Now().Unix()
	base -= base % 300
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	var uqCalls atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		uqCalls.Add(1)
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` +
			strconv.FormatInt((base-1)*1000, 10) + `,95]]}],"status":null,"trace_id":"g3b-query","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g3b-qg-isolation"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	events := &selectiveRetryablePhaseTwoEventSink{failedStrategyID: "1001"}
	var observationsMu sync.Mutex
	var observations []observability.Observation
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observationsMu.Lock()
				defer observationsMu.Unlock()
				observations = append(observations, observation)
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return events, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	if len(bundle.queryGroups) != 2 || len(bundle.runners) != 2 {
		t.Fatalf("initial Query Groups/runners = %v/%d, want two", bundle.queryGroups, len(bundle.runners))
	}
	runResults := make(map[execution.QueryGroupIdentity]*recordingPhaseTwoQueryGroupRuntime, len(bundle.runners))
	bundle.mu.Lock()
	for queryGroup, lifecycle := range bundle.runners {
		recording := &recordingPhaseTwoQueryGroupRuntime{next: lifecycle.runner}
		lifecycle.runner = recording
		runResults[queryGroup] = recording
	}
	bundle.mu.Unlock()
	clock.Store(base + 1)
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("runScheduledOnce(retryable sibling) error = %v", err)
	}
	if uqCalls.Load() != 2 {
		t.Fatalf("real UQ wire calls = %d, want two independent Query Groups", uqCalls.Load())
	}
	if len(bundle.runners) != 2 {
		t.Fatalf("retryable Event ACK migrated a Query Group: runners=%d", len(bundle.runners))
	}

	attempted, acknowledged := events.snapshot()
	if attempted["1001"] != 1 || attempted["1002"] != 1 || acknowledged["1001"] != 0 || acknowledged["1002"] != 1 {
		t.Fatalf("Event attempts/ACKs = %v/%v, want failed retryable sibling and healthy ACK", attempted, acknowledged)
	}
	productionOwnership := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	queryGroupsByStrategy := make(map[string]execution.QueryGroupIdentity, 2)
	for _, queryGroup := range bundle.queryGroups {
		schedule, scheduleErr := productionOwnership.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
		if scheduleErr != nil || len(schedule.Plans) != 1 {
			t.Fatalf("Query Group %s schedule=%+v error=%v", queryGroup, schedule, scheduleErr)
		}
		queryGroupsByStrategy[schedule.Plans[0].Identity.StrategyID] = queryGroup
	}
	failedProgress, err := productionOwnership.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{
		QueryGroup: queryGroupsByStrategy["1001"],
	})
	if err != nil || failedProgress.Status != execution.ProgressFound || failedProgress.Progress == nil ||
		failedProgress.Progress.UnfinishedSlot == nil || failedProgress.Progress.NextSlot != execution.EvaluationTime(base) {
		t.Fatalf("failed Query Group Progress=%+v error=%v, want durable unfinished projection", failedProgress, err)
	}
	healthyProgress, err := productionOwnership.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{
		QueryGroup: queryGroupsByStrategy["1002"],
	})
	if err != nil || healthyProgress.Status != execution.ProgressFound || healthyProgress.Progress == nil ||
		healthyProgress.Progress.LastFullSlot != execution.EvaluationTime(base) {
		t.Fatalf("healthy Query Group Progress=%+v error=%v", healthyProgress, err)
	}
	failedResult, failedAttempted, failedErr := runResults[queryGroupsByStrategy["1001"]].snapshot()
	if failedErr != nil || !failedAttempted || failedResult.Completed ||
		failedResult.Result != observability.ResultRetrying ||
		failedResult.ReasonCode != execution.ReasonCode(contract.ReasonOutputACKUnknown) {
		t.Fatalf("failed scheduler Runner result=%+v attempted=%t error=%v", failedResult, failedAttempted, failedErr)
	}
	healthyResult, healthyAttempted, healthyErr := runResults[queryGroupsByStrategy["1002"]].snapshot()
	if healthyErr != nil || !healthyAttempted || !healthyResult.Completed {
		t.Fatalf("healthy scheduler Runner result=%+v attempted=%t error=%v", healthyResult, healthyAttempted, healthyErr)
	}
	bundle.mu.RLock()
	failedRunnerStillOwned := bundle.runners[queryGroupsByStrategy["1001"]].runner == runResults[queryGroupsByStrategy["1001"]]
	bundle.mu.RUnlock()
	if !failedRunnerStillOwned {
		t.Fatal("retryable Event ACK migrated the failed Query Group")
	}

	observationsMu.Lock()
	var retryingQG execution.QueryGroupIdentity
	for _, observation := range observations {
		if observation.Stage == observability.Stage(observability.StageFatal) {
			observationsMu.Unlock()
			t.Fatalf("retryable Query Group failure became Worker fatal: %+v", observation)
		}
		if observation.Stage == observability.StageSlotCompleted &&
			observation.Result == observability.ResultRetrying &&
			observation.ReasonCode == execution.ReasonCode(contract.ReasonOutputACKUnknown) {
			retryingQG = execution.QueryGroupIdentity(observation.Trace.QueryGroupKey)
		}
	}
	observationsMu.Unlock()
	if retryingQG != queryGroupsByStrategy["1001"] {
		t.Fatalf("retrying Slot observation Query Group=%s, want failed Query Group %s", retryingQG, queryGroupsByStrategy["1001"])
	}
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatalf("phase-two production Shutdown() error = %v", err)
	}
}

func TestProductionPhaseTwoBundleKeepsHealthyQueryGroupWhenSiblingInitialFreezeLosesSnapshot(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, redisClient)

	base := time.Now().Unix()
	var clock atomic.Int64
	clock.Store(base * 1000)
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	var uqCalls atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		uqCalls.Add(1)
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"g3b-snapshot-isolation","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g3b-snapshot-qg-isolation"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(time.Hour)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	events := &recordingPhaseTwoEventSink{}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	defer func() {
		if shutdownErr := bundle.Shutdown(ctx); shutdownErr != nil {
			t.Errorf("phase-two production Shutdown() error = %v", shutdownErr)
		}
	}()
	if len(bundle.queryGroups) != 2 || len(bundle.runners) != 2 {
		t.Fatalf("initial Query Groups/runners = %v/%d, want two", bundle.queryGroups, len(bundle.runners))
	}

	production := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	queryGroupsByStrategy := make(map[string]execution.QueryGroupIdentity, 2)
	var snapshotRevision execution.SnapshotRevision
	for _, queryGroup := range bundle.queryGroups {
		schedule, scheduleErr := production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
		if scheduleErr != nil || len(schedule.Plans) != 1 {
			t.Fatalf("Query Group %s schedule=%+v error=%v", queryGroup, schedule, scheduleErr)
		}
		queryGroupsByStrategy[schedule.Plans[0].Identity.StrategyID] = queryGroup
		snapshotRevision = schedule.Segment.Publication.SnapshotRevision
	}
	clock.Store((base + 1) * 1000)
	healthy := queryGroupsByStrategy["1002"]
	failed := queryGroupsByStrategy["1001"]
	healthyResult, healthyAttempted, err := bundle.runners[healthy].runner.RunOne(ctx)
	if err != nil || !healthyAttempted || !healthyResult.Completed {
		t.Fatalf("healthy RunOne() = (%+v, %t, %v)", healthyResult, healthyAttempted, err)
	}
	queryCallsBeforeFailure := uqCalls.Load()
	eventsBeforeFailure := len(events.snapshot())
	snapshotKey := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog") + ":snapshot:" + string(snapshotRevision)
	snapshotPayload, err := redisClient.Get(ctx, snapshotKey).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Del(ctx, snapshotKey).Err(); err != nil {
		t.Fatal(err)
	}
	failedResult, failedAttempted, failedErr := bundle.runners[failed].runner.RunOne(ctx)
	if failedErr != nil || !failedAttempted || failedResult.Completed || failedResult.Result != observability.ResultRetrying ||
		failedResult.ReasonCode != execution.ReasonCode(contract.ReasonProviderUnavailable) {
		t.Fatalf("failed RunOne() = (%+v, %t, %v)", failedResult, failedAttempted, failedErr)
	}
	if uqCalls.Load() != queryCallsBeforeFailure || len(events.snapshot()) != eventsBeforeFailure {
		t.Fatalf("failed initial Freeze produced Query/Event: query=%d->%d event=%d->%d",
			queryCallsBeforeFailure, uqCalls.Load(), eventsBeforeFailure, len(events.snapshot()))
	}
	failedProgress, err := production.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: failed})
	if err != nil || failedProgress.Status != execution.ProgressMissing {
		t.Fatalf("failed Query Group Progress=%+v error=%v, want unchanged", failedProgress, err)
	}
	healthyProgress, err := production.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: healthy})
	if err != nil || healthyProgress.Status != execution.ProgressFound || healthyProgress.Progress == nil {
		t.Fatalf("healthy Query Group Progress=%+v error=%v", healthyProgress, err)
	}
	if len(bundle.runners) != 2 || !bundle.dependencies.Health.HealthSnapshot().Ready {
		t.Fatalf("snapshot loss stopped Worker: runners=%d health=%+v", len(bundle.runners), bundle.dependencies.Health.HealthSnapshot())
	}

	// Once the same immutable Snapshot fact returns after recovery_until, the
	// coordinator first rebuilds the missing projection with BeginSlot and then
	// finalizes query-free. A recovered Snapshot must not reopen Query.
	if err := redisClient.Set(ctx, snapshotKey, snapshotPayload, cfg.PhaseTwo.Control.CatalogTTL.Duration()).Err(); err != nil {
		t.Fatal(err)
	}
	clock.Store((base + int64((30*time.Minute)/time.Second)) * 1000)
	recoveredResult, recoveredAttempted, err := bundle.runners[failed].runner.RunOne(ctx)
	if err != nil || !recoveredAttempted || !recoveredResult.Completed ||
		recoveredResult.ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) {
		t.Fatalf("RunOne(recovered after deadline) = (%+v, %t, %v)", recoveredResult, recoveredAttempted, err)
	}
	if uqCalls.Load() != queryCallsBeforeFailure || len(events.snapshot()) != eventsBeforeFailure {
		t.Fatalf("expired recovered Snapshot produced Query/Event: query=%d->%d event=%d->%d",
			queryCallsBeforeFailure, uqCalls.Load(), eventsBeforeFailure, len(events.snapshot()))
	}
	completedProgress, err := production.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: failed})
	if err != nil || completedProgress.Progress == nil || completedProgress.Progress.UnfinishedSlot != nil ||
		completedProgress.Progress.CurrentOrRecentGap == nil ||
		completedProgress.Progress.CurrentOrRecentGap.Kind != execution.CompletionSnapshotUnavailable {
		t.Fatalf("recovered query-free Progress=%+v error=%v", completedProgress, err)
	}
}

func TestProductionPhaseTwoBundleDrainsExpiredRetiredBacklogWithoutProjection(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, redisClient)

	base := time.Now().Unix()
	base -= base % 60
	var clock atomic.Int64
	clock.Store(base * 1000)
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	var uqCalls atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		uqCalls.Add(1)
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unexpected-retired-query","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g4-retired-backlog-projection"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(time.Hour)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	defer func() {
		if shutdownErr := bundle.Shutdown(ctx); shutdownErr != nil {
			t.Errorf("phase-two production Shutdown() error = %v", shutdownErr)
		}
	}()

	production := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	queryGroupsByStrategy := make(map[string]execution.QueryGroupIdentity, 2)
	for _, queryGroup := range bundle.queryGroups {
		schedule, scheduleErr := production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
		if scheduleErr != nil || len(schedule.Plans) != 1 {
			t.Fatalf("Query Group %s schedule=%+v error=%v", queryGroup, schedule, scheduleErr)
		}
		queryGroupsByStrategy[schedule.Plans[0].Identity.StrategyID] = queryGroup
	}
	retired := queryGroupsByStrategy["1001"]
	if retired == "" {
		t.Fatalf("strategy Query Groups = %v, want strategy 1001", queryGroupsByStrategy)
	}
	initialProgress, err := production.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: retired})
	if err != nil || initialProgress.Status != execution.ProgressMissing {
		t.Fatalf("retired candidate Progress=%+v error=%v, want missing projection", initialProgress, err)
	}

	strategyPayload, err := redisClient.Get(ctx, "alarm-config.strategy_1001").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var changedStrategy map[string]any
	if err := json.Unmarshal(strategyPayload, &changedStrategy); err != nil {
		t.Fatal(err)
	}
	changedItem := changedStrategy["items"].([]any)[0].(map[string]any)
	changedItem["query_md5"] = "g4-retired-backlog-query-md5"
	changedItem["query_configs"].([]any)[0].(map[string]any)["result_table_id"] = "system.disk"
	strategyPayload, err = json.Marshal(changedStrategy)
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyPayload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	clock.Store((base + 1) * 1000)
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("refreshAndReconcile(observe strategy 1001 retirement) error = %v", err)
	}
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("refreshAndReconcile(confirm strategy 1001 retirement) error = %v", err)
	}
	productionControl := bundle.dependencies.Control.(*productionPhaseTwoControl)
	activation, err := productionControl.dependencies.Repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var retiredBoundary execution.EvaluationTime
	for _, draining := range activation.Draining {
		if draining.QueryGroup == retired {
			retiredBoundary = draining.RetiredBoundary
			break
		}
	}
	if retiredBoundary == 0 {
		t.Fatalf("activation Draining=%+v, want retired Query Group %s", activation.Draining, retired)
	}
	if _, stillOwned := bundle.runners[retired]; !stillOwned {
		t.Fatalf("retired Query Group %s was removed before Progress reached %d", retired, retiredBoundary)
	}

	clock.Store((int64(retiredBoundary) + int64((30*time.Minute)/time.Second)) * 1000)
	result, attempted, err := bundle.runners[retired].runner.RunOne(ctx)
	if err != nil || !attempted || !result.Completed ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) {
		t.Fatalf("RunOne(expired retired backlog) = (%+v, %t, %v)", result, attempted, err)
	}
	if uqCalls.Load() != 0 {
		t.Fatalf("expired retired backlog issued %d UQ calls, want query-free finalization", uqCalls.Load())
	}
	completed, err := production.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: retired})
	if err != nil || completed.Status != execution.ProgressFound || completed.Progress == nil ||
		completed.Progress.NextSlot != retiredBoundary || completed.Progress.UnfinishedSlot != nil ||
		completed.Progress.CurrentOrRecentGap == nil ||
		completed.Progress.CurrentOrRecentGap.Kind != execution.CompletionSnapshotUnavailable {
		t.Fatalf("retired Progress=%+v error=%v, want query-free completion at boundary %d", completed, err, retiredBoundary)
	}
}

func TestProductionPhaseTwoBundleSharesOneProcessRecoveryPermitBudgetAcrossOwnedQueryGroups(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, redisClient)

	base := time.Now().Unix()
	var clock atomic.Int64
	clock.Store(time.Unix(base, 0).UnixMilli())
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	var uqCalls atomic.Int64
	var inflight atomic.Int64
	var maxInflight atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		call := uqCalls.Add(1)
		current := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			maximum := maxInflight.Load()
			if current <= maximum || maxInflight.CompareAndSwap(maximum, current) {
				break
			}
		}
		switch call {
		case 1:
			close(firstEntered)
			<-releaseFirst
		case 2:
			close(secondEntered)
		}
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"shared-permit","is_partial":false,"result_table_id":[]}`))
	}))
	defer func() {
		release()
		uqServer.Close()
	}()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g3b-shared-query-permits"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(500 * time.Millisecond)
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 2
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 1
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	queued := make(chan struct{}, 1)
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if observation.Stage == observability.StageQueryAdmission && observation.Result == observability.ResultStarted &&
					observation.Operation == observability.OperationReplay && observation.QueryPermit != nil &&
					observation.QueryPermit.QueueKind == observability.QueryQueueRecovery && observation.QueryPermit.RecoveryWaiting > 0 {
					select {
					case queued <- struct{}{}:
					default:
					}
				}
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	defer func() {
		if err := bundle.Shutdown(ctx); err != nil {
			t.Errorf("phase-two production Shutdown() error = %v", err)
		}
	}()
	defer release()
	productionOwnership := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	if productionOwnership.flights == nil {
		t.Fatal("production ownership has no process-wide FlightCoordinator")
	}
	bundle.mu.RLock()
	runnerCount := len(bundle.runners)
	bundle.mu.RUnlock()
	if runnerCount != 2 {
		t.Fatalf("production runners = %d, want two owned Query Groups", runnerCount)
	}
	clock.Store(time.Unix(base, 0).Add(500 * time.Millisecond).UnixMilli())

	tickDone := make(chan error, 1)
	go func() { tickDone <- bundle.runScheduledOnce(ctx) }()
	select {
	case <-firstEntered:
	case err := <-tickDone:
		t.Fatalf("production tick returned before first UQ: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("first owned Query Group did not enter UQ")
	}
	select {
	case <-queued:
		t.Fatal("replay sibling reached the inner query-permit queue instead of waiting for the next scheduler tick")
	case <-secondEntered:
		t.Fatal("owned Query Groups used copied recovery permit budgets or bypassed recovery-aware SlotSource")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	if err := <-tickDone; err != nil {
		t.Fatalf("production tick error = %v", err)
	}
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("second production tick error = %v", err)
	}
	if uqCalls.Load() != 2 || maxInflight.Load() != 1 {
		t.Fatalf("process UQ calls/max inflight = %d/%d, want 2/1", uqCalls.Load(), maxInflight.Load())
	}
}

func TestProductionPhaseTwoBundleCommitsBudgetExhaustedRecoveryCompletionWithoutUQ(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, redisClient)

	base := time.Now().Unix()
	var clock atomic.Int64
	clock.Store(time.Unix(base, 0).UnixMilli())
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	var uqCalls atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		uqCalls.Add(1)
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unexpected","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g3b-budget-completion"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(900 * time.Millisecond)
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 2
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 1
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	var observationsMu sync.Mutex
	var observations []observability.Observation
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observationsMu.Lock()
				defer observationsMu.Unlock()
				observations = append(observations, observation)
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	productionOwnership := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	holder, err := productionOwnership.flights.AcquireQueryPermit(ctx,
		execution.SlotIdentity{QueryGroup: "recovery-holder", EvaluationTime: execution.EvaluationTime(base)},
		execution.OperationReplay, now().Add(time.Minute))
	if err != nil {
		t.Fatalf("hold process recovery permit: %v", err)
	}
	defer func() {
		holder.Release()
		if err := bundle.Shutdown(ctx); err != nil {
			t.Errorf("phase-two production Shutdown() error = %v", err)
		}
	}()

	clock.Store(time.Unix(base, 0).Add(500 * time.Millisecond).UnixMilli())
	for tick := 1; tick <= 2; tick++ {
		if err := bundle.runScheduledOnce(ctx); err != nil {
			t.Fatalf("budget-exhausted production tick %d error = %v", tick, err)
		}
	}
	if uqCalls.Load() != 0 {
		t.Fatalf("budget-exhausted completion issued %d UQ calls", uqCalls.Load())
	}
	for _, queryGroup := range bundle.queryGroups {
		progress := loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroup)
		if progress.LastCompletionKind != execution.CompletionUnavailable || progress.LastFullSlot != 0 ||
			progress.NextSlot != execution.EvaluationTime(base+1) || progress.CurrentOrRecentGap == nil ||
			progress.CurrentOrRecentGap.ReasonCode != execution.ReasonCode(contract.ReasonExecutionBudgetExhausted) {
			t.Fatalf("budget-exhausted Query Group %s Progress=%+v", queryGroup, progress)
		}
	}
	observationsMu.Lock()
	defer observationsMu.Unlock()
	queryCompleted, slotCompleted := 0, 0
	for _, observation := range observations {
		if observation.ReasonCode != observability.ReasonCode(contract.ReasonExecutionBudgetExhausted) {
			continue
		}
		switch observation.Stage {
		case observability.StageQueryCompleted:
			queryCompleted++
		case observability.StageSlotCompleted:
			slotCompleted++
		}
	}
	if queryCompleted == 0 || slotCompleted == 0 {
		t.Fatalf("budget completion observations query/slot=%d/%d", queryCompleted, slotCompleted)
	}
}

func TestProductionPhaseTwoBundleLetsNormalUseRemainingProcessPermitDuringRecovery(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, redisClient)

	base := time.Now().Unix()
	var clock atomic.Int64
	// Keep the configured one-millisecond readiness boundary due without
	// changing the integer-second Slot identities exercised below.
	clock.Store(time.Unix(base, 0).Add(2 * time.Millisecond).UnixMilli())
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	var setup atomic.Bool
	setup.Store(true)
	recoveryEntered := make(chan struct{})
	normalEntered := make(chan struct{})
	releaseQueries := make(chan struct{})
	var recoveryOnce, normalOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseQueries) }) }
	var recoveryCalls, normalCalls atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		memoryQuery := bytes.Contains(body, []byte("system.mem"))
		if !setup.Load() {
			if memoryQuery {
				normalCalls.Add(1)
				normalOnce.Do(func() { close(normalEntered) })
				select {
				case <-recoveryEntered:
				case <-releaseQueries:
				}
			} else {
				recoveryCalls.Add(1)
				recoveryOnce.Do(func() { close(recoveryEntered) })
				<-releaseQueries
			}
		}
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"normal-recovery-isolation","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g3b-normal-recovery-isolation"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 2
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 1
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	var observationsMu sync.Mutex
	var observations []observability.Observation
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observationsMu.Lock()
				defer observationsMu.Unlock()
				observations = append(observations, observation)
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	defer func() {
		release()
		if err := bundle.Shutdown(ctx); err != nil {
			t.Errorf("phase-two production Shutdown() error = %v", err)
		}
	}()

	productionOwnership := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	queryGroupsByStrategy := make(map[string]execution.QueryGroupIdentity, 2)
	for _, queryGroup := range bundle.queryGroups {
		schedule, scheduleErr := productionOwnership.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
		if scheduleErr != nil || len(schedule.Plans) != 1 {
			t.Fatalf("Query Group %s schedule=%+v error=%v", queryGroup, schedule, scheduleErr)
		}
		queryGroupsByStrategy[schedule.Plans[0].Identity.StrategyID] = queryGroup
	}
	bundle.mu.RLock()
	normalRunner := bundle.runners[queryGroupsByStrategy["1002"]].runner
	bundle.mu.RUnlock()
	if _, attempted, runErr := normalRunner.RunOne(ctx); runErr != nil || !attempted {
		t.Fatalf("prepare normal Query Group attempted=%t error=%v", attempted, runErr)
	}
	setup.Store(false)
	observationsMu.Lock()
	observations = nil
	observationsMu.Unlock()
	clock.Store(time.Unix(base+1, 0).Add(2 * time.Millisecond).UnixMilli())

	tickDone := make(chan error, 1)
	go func() { tickDone <- bundle.runScheduledOnce(ctx) }()
	waitSignal(t, recoveryEntered, "recovery Query Group UQ")
	waitSignal(t, normalEntered, "normal Query Group UQ while recovery is inflight")
	release()
	if err := <-tickDone; err != nil {
		t.Fatalf("production tick error = %v", err)
	}
	if recoveryCalls.Load() != 1 || normalCalls.Load() != 1 {
		t.Fatalf("single production tick recovery/normal UQ calls = %d/%d, want 1/1",
			recoveryCalls.Load(), normalCalls.Load())
	}
	observationsMu.Lock()
	defer observationsMu.Unlock()
	admitted := make(map[observability.Operation]int)
	for _, observation := range observations {
		if observation.Stage == observability.StageQueryAdmission && observation.Result == observability.ResultSuccess {
			admitted[observation.Operation]++
		}
	}
	if admitted[observability.OperationReplay] == 0 || admitted[observability.OperationNormal] == 0 {
		t.Fatalf("query permit admissions by operation = %v, want replay and normal", admitted)
	}
}

func TestProductionPhaseTwoBundleCompletesIncompleteAccessWithoutStoppingHealthyQueryGroupAndRecovers(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, redisClient)

	base := time.Now().Add(2 * time.Second).Unix()
	var clock atomic.Int64
	clock.Store(base)
	// Keep the one-millisecond readiness boundary due while retaining the same
	// integer-second Slot identity used by this recovery test.
	now := func() time.Time { return time.Unix(clock.Load(), 0).Add(2 * time.Millisecond) }
	var cpuAttempts atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			QueryList []struct {
				TableID string `json:"table_id"`
			} `json:"query_list"`
			EndTime string `json:"end_time"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || len(payload.QueryList) != 1 {
			t.Errorf("decode UQ request: payload=%+v error=%v", payload, err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil {
			t.Errorf("parse end_time %q: %v", payload.EndTime, err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		body := `{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` +
			strconv.FormatInt((end-1)*1000, 10) + `,95]]}],"status":null,"trace_id":"g3b-recovered","is_partial":false,"result_table_id":[]}`
		if payload.QueryList[0].TableID == "system.cpu" {
			switch cpuAttempts.Add(1) {
			case 1:
				body = `{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` +
					strconv.FormatInt((end-1)*1000, 10) + `,95]]}],"status":{"code":"QUERY_TS_PARTIAL","message":"one route failed"},"trace_id":"g3b-partial","is_partial":false,"result_table_id":[]}`
			case 2:
				writer.WriteHeader(http.StatusBadGateway)
				return
			}
		}
		_, _ = writer.Write([]byte(body))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g3b-access-recovery"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	events := &recordingPhaseTwoEventSink{}
	var fatal atomic.Bool
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if observation.Stage == observability.Stage(observability.StageFatal) {
					fatal.Store(true)
				}
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	defer func() {
		if err := bundle.Shutdown(ctx); err != nil {
			t.Errorf("phase-two production Shutdown() error = %v", err)
		}
	}()

	productionOwnership := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	queryGroupsByStrategy := make(map[string]execution.QueryGroupIdentity, 2)
	for _, queryGroup := range bundle.queryGroups {
		schedule, scheduleErr := productionOwnership.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
		if scheduleErr != nil || len(schedule.Plans) != 1 {
			t.Fatalf("Query Group %s schedule=%+v error=%v", queryGroup, schedule, scheduleErr)
		}
		queryGroupsByStrategy[schedule.Plans[0].Identity.StrategyID] = queryGroup
	}

	clock.Store(base)
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("runScheduledOnce(partial sibling) error = %v", err)
	}
	failed := loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroupsByStrategy["1001"])
	healthy := loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroupsByStrategy["1002"])
	if failed.LastCompletionKind != execution.CompletionPartialGap || failed.LastFullSlot != 0 ||
		failed.CurrentOrRecentGap == nil || failed.CurrentOrRecentGap.ReasonCode != execution.ReasonCode(contract.ReasonQueryPartial) {
		t.Fatalf("partial Query Group Progress=%+v gap=%+v", failed, failed.CurrentOrRecentGap)
	}
	if healthy.LastFullSlot != execution.EvaluationTime(base) || healthy.LastCompletionKind != execution.CompletionFull {
		t.Fatalf("healthy Query Group Progress=%+v", healthy)
	}
	gapKeys, err := redisClient.Keys(ctx, cfg.Redis.StatePrefix+":gap:v2:*:*:1001:*").Result()
	if err != nil || len(gapKeys) != 1 {
		t.Fatalf("partial Query Group Guard keys=%v error=%v", gapKeys, err)
	}
	var guard struct {
		Scopes []execution.GapScopeState `json:"scopes"`
	}
	if err := json.Unmarshal([]byte(redisClient.Get(ctx, gapKeys[0]).Val()), &guard); err != nil || len(guard.Scopes) != 1 ||
		guard.Scopes[0].Status != execution.GapStatusGapped ||
		guard.Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonQueryPartial) {
		t.Fatalf("partial Query Group Guard=%+v error=%v", guard, err)
	}
	for _, event := range events.snapshot() {
		if event.PlanRef.StrategyID == "1001" {
			t.Fatalf("PARTIAL emitted an unproven event: %+v", event)
		}
	}

	clock.Store(base + 1)
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("runScheduledOnce(unavailable sibling) error = %v", err)
	}
	failed = loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroupsByStrategy["1001"])
	healthy = loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroupsByStrategy["1002"])
	if failed.LastCompletionKind != execution.CompletionUnavailable || failed.LastFullSlot != 0 ||
		failed.CurrentOrRecentGap == nil || failed.CurrentOrRecentGap.ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) {
		t.Fatalf("unavailable Query Group Progress=%+v gap=%+v", failed, failed.CurrentOrRecentGap)
	}
	if healthy.LastFullSlot != execution.EvaluationTime(base+1) || healthy.LastCompletionKind != execution.CompletionFull {
		t.Fatalf("healthy Query Group Progress after unavailable sibling=%+v", healthy)
	}

	clock.Store(base + 2)
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("runScheduledOnce(recovered sibling) error = %v", err)
	}
	recovered := loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroupsByStrategy["1001"])
	if recovered.LastFullSlot != execution.EvaluationTime(base+2) ||
		recovered.LastCompletionKind != execution.CompletionFull || recovered.NextSlot != execution.EvaluationTime(base+3) {
		t.Fatalf("recovered Query Group Progress=%+v", recovered)
	}
	foundRecoveredEvent := false
	for _, event := range events.snapshot() {
		foundRecoveredEvent = foundRecoveredEvent || event.PlanRef.StrategyID == "1001"
	}
	if !foundRecoveredEvent || fatal.Load() || len(bundle.runners) != 2 {
		t.Fatalf("recovery event=%t fatal=%t runners=%d", foundRecoveredEvent, fatal.Load(), len(bundle.runners))
	}
}

func installTwoPhaseTwoStrategies(t *testing.T, ctx context.Context, redisClient *redis.Client) {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var firstStrategy map[string]any
	if err := json.Unmarshal(raw, &firstStrategy); err != nil {
		t.Fatal(err)
	}
	firstItem := firstStrategy["items"].([]any)[0].(map[string]any)
	firstItem["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 1
	first, err := json.Marshal(firstStrategy)
	if err != nil {
		t.Fatal(err)
	}
	var second map[string]any
	if err := json.Unmarshal(first, &second); err != nil {
		t.Fatal(err)
	}
	second["id"] = 1002
	item := second["items"].([]any)[0].(map[string]any)
	item["id"], item["query_md5"] = 12, "g3b-access-healthy-query-md5"
	item["query_configs"].([]any)[0].(map[string]any)["result_table_id"] = "system.mem"
	encoded, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":  `[1001,1002]`,
		"alarm-config.strategy_1001": first,
		"alarm-config.strategy_1002": encoded,
	} {
		if err := redisClient.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func loadPhaseTwoProgress(
	t *testing.T,
	ctx context.Context,
	production *productionPhaseTwoOwnership,
	queryGroup execution.QueryGroupIdentity,
) execution.ScheduleProgress {
	t.Helper()
	loaded, err := production.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: queryGroup})
	if err != nil || loaded.Status != execution.ProgressFound || loaded.Progress == nil {
		t.Fatalf("Query Group %s Progress=%+v error=%v", queryGroup, loaded, err)
	}
	return *loaded.Progress
}

type recordingPhaseTwoEventSink struct {
	mu     sync.Mutex
	events []contract.TriggerEventV1
	closed bool
}

type selectiveRetryablePhaseTwoEventSink struct {
	mu               sync.Mutex
	failedStrategyID string
	attempted        map[string]int
	acknowledged     map[string]int
	closed           bool
}

func (sink *selectiveRetryablePhaseTwoEventSink) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.attempted == nil {
		sink.attempted = make(map[string]int)
		sink.acknowledged = make(map[string]int)
	}
	for index := range events {
		if _, err := contract.EncodeTriggerEventV1(&events[index]); err != nil {
			return err
		}
		strategyID := events[index].PlanRef.StrategyID
		sink.attempted[strategyID]++
		if strategyID == sink.failedStrategyID {
			return &retryablePhaseTwoEventError{err: errors.New("broker ACK unavailable")}
		}
	}
	for index := range events {
		sink.acknowledged[events[index].PlanRef.StrategyID]++
	}
	return nil
}

func (sink *selectiveRetryablePhaseTwoEventSink) Shutdown(context.Context) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.closed = true
	return nil
}

func (sink *selectiveRetryablePhaseTwoEventSink) Close() error {
	return sink.Shutdown(context.Background())
}

func (sink *selectiveRetryablePhaseTwoEventSink) snapshot() (map[string]int, map[string]int) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	attempted := make(map[string]int, len(sink.attempted))
	acknowledged := make(map[string]int, len(sink.acknowledged))
	for strategyID, count := range sink.attempted {
		attempted[strategyID] = count
	}
	for strategyID, count := range sink.acknowledged {
		acknowledged[strategyID] = count
	}
	return attempted, acknowledged
}

type retryablePhaseTwoEventError struct{ err error }

func (err *retryablePhaseTwoEventError) Error() string              { return err.err.Error() }
func (err *retryablePhaseTwoEventError) Unwrap() error              { return err.err }
func (err *retryablePhaseTwoEventError) RetryableOutputDependency() {}

type recordingPhaseTwoQueryGroupRuntime struct {
	next phaseTwoQueryGroupRuntime
	mu   sync.Mutex

	result    execution.SlotExecutionResult
	attempted bool
	err       error
}

func (runtime *recordingPhaseTwoQueryGroupRuntime) RunOne(ctx context.Context) (execution.SlotExecutionResult, bool, error) {
	result, attempted, err := runtime.next.RunOne(ctx)
	runtime.mu.Lock()
	runtime.result = result
	runtime.attempted = attempted
	runtime.err = err
	runtime.mu.Unlock()
	return result, attempted, err
}

func (runtime *recordingPhaseTwoQueryGroupRuntime) RunOneAdmitted(
	ctx context.Context,
	admission scheduler.ExecutionAdmission,
) (execution.SlotExecutionResult, bool, bool, error) {
	result, attempted, admissionDenied, err := runtime.next.RunOneAdmitted(ctx, admission)
	runtime.mu.Lock()
	runtime.result = result
	runtime.attempted = attempted
	runtime.err = err
	runtime.mu.Unlock()
	return result, attempted, admissionDenied, err
}

func (runtime *recordingPhaseTwoQueryGroupRuntime) NextReadyAt() time.Time {
	return runtime.next.NextReadyAt()
}

func (runtime *recordingPhaseTwoQueryGroupRuntime) MaintainLease(ctx context.Context, interval, ttl time.Duration) error {
	return runtime.next.MaintainLease(ctx, interval, ttl)
}

func (runtime *recordingPhaseTwoQueryGroupRuntime) Release(ctx context.Context) error {
	return runtime.next.Release(ctx)
}

func (runtime *recordingPhaseTwoQueryGroupRuntime) snapshot() (execution.SlotExecutionResult, bool, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.result, runtime.attempted, runtime.err
}

func (sink *recordingPhaseTwoEventSink) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	for index := range events {
		if _, err := contract.EncodeTriggerEventV1(&events[index]); err != nil {
			return err
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, events...)
	return nil
}

func (sink *recordingPhaseTwoEventSink) Shutdown(context.Context) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.closed = true
	return nil
}

func (sink *recordingPhaseTwoEventSink) Close() error { return sink.Shutdown(context.Background()) }

func (sink *recordingPhaseTwoEventSink) snapshot() []contract.TriggerEventV1 {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]contract.TriggerEventV1(nil), sink.events...)
}

func (sink *recordingPhaseTwoEventSink) isClosed() bool {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.closed
}

func assertObservedOrder(t *testing.T, got, want []observability.Stage) {
	t.Helper()
	index := 0
	for _, stage := range got {
		if index < len(want) && stage == want[index] {
			index++
		}
	}
	if index != len(want) {
		t.Fatalf("observation order = %v, missing ordered suffix %v", got, want[index:])
	}
}

func startPhaseTwoRedis(t *testing.T) (string, *redis.Client) {
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
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "--bind", "127.0.0.1", "--port", port,
		"--save", "", "--appendonly", "no", "--dir", t.TempDir(), "--daemonize", "no", "--loglevel", "warning")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{
		Addr: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
	})
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
			return address, client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("redis-server did not become ready: %s", output.String())
	return "", nil
}
