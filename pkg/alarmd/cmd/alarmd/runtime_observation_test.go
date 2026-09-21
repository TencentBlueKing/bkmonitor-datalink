// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func TestObservationRegistrationUsesOwnedCurrentGroupsAndRealVersions(t *testing.T) {
	s := controlplane.StrategyDirectorySnapshot{Complete: true}
	for _, qg := range []execution.QueryGroupIdentity{"ours", "others"} {
		s.Rows = append(s.Rows, controlplane.StrategyDirectoryRow{Identity: execution.PlanIdentity{TenantID: "t", BusinessID: "b", StrategyID: string(qg)}, QueryGroup: qg, Role: string(execution.ActivationCurrent), QueryRevision: "query", ScheduleRevision: "schedule", Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot", PublicationEpoch: 1}})
	}
	g, complete := observationCostGroups(s, []execution.QueryGroupIdentity{"ours"})
	if !complete || len(g) != 1 || g[0].QueryGroupKey != "ours" || g[0].QueryRevision != "query" || g[0].ScheduleRevision != "schedule" || g[0].SnapshotRevision != "snapshot" || g[0].Members[0].StrategyID != "ours" {
		t.Fatalf("registration %+v complete=%v", g, complete)
	}
	if _, complete = observationCostGroups(s, []execution.QueryGroupIdentity{"ours", "unknown"}); complete {
		t.Fatal("unmapped owner claimed complete")
	}
	s.Rows[0].Role = "PUBLISHED"
	if g, complete = observationCostGroups(s, []execution.QueryGroupIdentity{"ours"}); complete || len(g) != 0 {
		t.Fatalf("published plan charged %+v", g)
	}
}

func TestObservationRegistryAndCostPaginationCannotStarveReplicas(t *testing.T) {
	client := windowRedis(t)
	ctx := context.Background()
	at := time.Now()
	registry, err := ownership.NewRedisStoreWithClient(client, "test:ob:registry")
	if err != nil {
		t.Fatal(err)
	}
	limits := fleet.CostProjectionLimits{PublishBytes: 16 << 10, ReadBytes: 64 << 10, ReadCommands: 1, Timeout: time.Second, FreshFor: time.Minute, TTL: 2 * time.Minute}
	projection, err := fleet.NewCostProjectionStore(client, "test:ob:cost", limits)
	if err != nil {
		t.Fatal(err)
	}
	cost := observability.CostSnapshot{Enabled: true, ProcessID: "process", Scope: "process_observed_candidates", GeneratedAt: at}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err = registry.RegisterWorker(ctx, ownership.WorkerRegistration{WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy, DeploymentProfile: "profile", CapabilitiesDigest: "capabilities", ExpiresAt: at.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, err = projection.Publish(ctx, id, at, cost); err != nil {
			t.Fatal(err)
		}
	}
	r := &observationCostRefresh{store: projection, registry: registry, reader: client, cache: fleet.NewCostCandidatesCache(func() time.Time { return at }, time.Minute), limits: limits, registryLimits: ownership.ObservationRegistryLimits{Bytes: 8 << 10, Commands: 4, Rows: 2, Timeout: time.Second}, replica: "a", interval: time.Second}
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		at = at.Add(time.Second)
		r.publish(ctx, at, cost)
		for _, snapshot := range r.observation.Projection.Snapshots {
			seen[snapshot.Replica] = true
		}
		if r.observation.Projection.Complete {
			t.Fatal("partial projection claimed full fleet")
		}
	}
	if len(seen) != 4 {
		t.Fatalf("independent cursors starved replicas: %v", seen)
	}
}

func TestObservationCapacityCannotBlockExecutionAtSmallOrLargeResources(t *testing.T) {
	for _, resources := range []config.CapacityInputs{{}, {CPUBudget: 1, MemoryLimitBytes: 2 << 20, MemorySource: "cgroup"}, {CPUBudget: 1, MemoryLimitBytes: 1 << 30, MemorySource: "cgroup"}, {CPUBudget: 64, MemoryLimitBytes: 64 << 30, MemorySource: "cgroup"}} {
		capacity := config.DeriveObservationCapacity(resources, config.PhaseTwoObservationConfig{MemoryPercent: 3})
		limits, enabled := observationSampleLimits(capacity)
		if enabled {
			if _, err := observability.NewSeriesSampler(limits); err != nil {
				t.Fatalf("resource-derived sample rejected %+v: %v", resources, err)
			}
		}
		o := observationCostOptions(capacity, "process", time.Now)
		if got := observability.CostSummaryCapacityBytes(o); got > int64(capacity.CostBytes/2) {
			t.Fatalf("collector reservation %d > half budget %d", got, capacity.CostBytes/2)
		}
		if resources.MemoryLimitBytes == 1<<30 {
			t.Logf("1GiB/1CPU: capacity=%+v directory_entry_reservation=%d cost_groups=%d cost_plans=%d cost_top_n=%d cost_reservation=%d sample=%+v projection=%+v", capacity, controlplane.DirectoryEntryReservationBytes(), o.GroupCapacity, o.PlanCapacity, o.TopN, observability.CostSummaryCapacityBytes(o), limits, observationProjectionLimits(capacity, 30*time.Second))
		}
		if resources.MemoryLimitBytes <= 2<<20 && enabled {
			t.Fatalf("enabled without one complete buffer %+v", limits)
		}
	}
}

func TestObservationRedisHasIndependentPoolAndNoHiddenRetries(t *testing.T) {
	o := observationRedisOptions(config.RedisConnectionConfig{PoolSize: 100, ReadTimeout: config.Duration(5 * time.Second), WriteTimeout: config.Duration(5 * time.Second)})
	if o.MaxRetries != -1 || o.PoolSize != phaseTwoDiagnosticsPoolSize || o.ReadTimeout > time.Second || o.WriteTimeout > time.Second || o.PoolTimeout > time.Second {
		t.Fatalf("diagnostics can consume unbounded attempts or execution pool %+v", o)
	}
}
