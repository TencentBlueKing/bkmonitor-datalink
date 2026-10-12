// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// redisNoDataStore is the store production wires, on a redis-server started
// for the test, and a client of the test's own on the same server.
//
// The doubles the other no-data tests in this package use answer with a status
// the test chose. These tests are about what the worker does with what the
// store actually answers, so the store is the real one and so is the server.
func redisNoDataStore(t *testing.T, extra ...string) (*state.ExecutionStore, *redis.Client) {
	t.Helper()
	server := redistest.Start(t, extra...)
	backend, err := state.NewRedisBackend(state.RedisBackendOptions{
		Address: server.Addr, DialTimeout: time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, PoolSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	router, err := state.NewFixedRouter("redis", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.NewExecutionStore(state.ExecutionStoreOptions{
		Prefix: "alarmd", Router: router, MaxValueBytes: 512 << 10, MaxItemsPerCall: 64,
		MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second})
	t.Cleanup(func() { _ = client.Close() })
	return store, client
}

// observingCoordinator is a coordinator whose no-data port is the given store
// and whose observations land in the returned slice.
func observingCoordinator(store execution.PlanNoDataStore) (*SlotExecutionCoordinator, *[]observability.Observation) {
	observed := make([]observability.Observation, 0, 4)
	return &SlotExecutionCoordinator{ports: Ports{
		NoData: store, Hosts: SharedHostBusiness,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observed = append(observed, observability.NormalizeObservation(observation))
		}),
	}}, &observed
}

// memoryOf is one statement about the refused-memory fixture Plan's memory,
// derived from no record, for the given round.
func memoryOf(t *testing.T, evaluationTime int64, groups []execution.NoDataGroupMemory) execution.PlanNoDataMutation {
	t.Helper()
	mutation, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationNone,
		Identity: execution.PlanNoDataIdentity{
			Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "10", StrategyID: "856"},
			StateGeneration: "generation",
		},
		ApplyVersion: execution.ApplyVersion{
			StateApplyEpoch: 1, EvaluationTime: execution.EvaluationTime(evaluationTime), SlotDigest: "slot",
		},
		ScheduleRevision: "plan-r1", RosterVersion: "HISTORY/1", PresentAsOf: 1000, Memory: groups,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

// stagesOf is the observations of one stage.
func stagesOf(observed []observability.Observation, stage observability.Stage) []observability.Observation {
	var matched []observability.Observation
	for _, observation := range observed {
		if observation.Stage == stage {
			matched = append(matched, observation)
		}
	}
	return matched
}

// A late write the store answers STALE_VERSION is reported as a write that did
// not keep the memory, and the Slot goes on, against a real server.
//
// decision-008 section 7.5 item 5: STALE_VERSION and CONFLICT should sit near
// zero and are read as "two writers of one Plan"; reading a stale write as
// stored would show a Plan whose memory is being kept while its absence clocks
// stand still. The newer record is written first through the same store, the
// late round's write is then sent through the worker, and the record is
// checked to be the newer one still.
func TestALateNoDataWriteIsReportedAsNotStoredOnARealServer(t *testing.T) {
	store, client := redisNoDataStore(t)
	ctx := context.Background()
	due := refusedMemoryDue(t)
	contractRef := noDataPreflightContract(t, due)
	group := []execution.NoDataGroupMemory{{GroupKey: "a", LastSeen: 900, FirstAbsent: 940}}

	newer, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: contractRef, Retention: execution.GenerationRetention{Unknown: true},
		Items: []execution.PlanNoDataMutation{memoryOf(t, 120, group)},
	})
	if err != nil || newer.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("the newer round's write = (%+v, %v)", newer, err)
	}

	coordinator, observed := observingCoordinator(store)
	late := memoryOf(t, 60, []execution.NoDataGroupMemory{{GroupKey: "b", LastSeen: 900, FirstAbsent: 940}})
	if err := coordinator.applyNoDataMemory(ctx, execution.SlotExecutionRequest{
		Operation: execution.OperationNormal, Contract: contractRef,
	}, due, []execution.PlanNoDataMutation{late}); err != nil {
		t.Fatalf("a stale write failed the Slot: %v", err)
	}
	writes := stagesOf(*observed, observability.StageNoDataMemoryWritten)
	if len(writes) != 1 || len(stagesOf(*observed, observability.StageNoDataMemoryRefused)) != 0 {
		t.Fatalf("observations = %+v, want one write line and no refusal line", *observed)
	}
	facts := writes[0].NoDataMemoryWrite
	if facts == nil || facts.Outcome != string(execution.NoDataStale) || facts.Stored {
		t.Fatalf("write facts = %+v, want STALE_VERSION reported as not stored", facts)
	}
	if writes[0].Result != observability.ResultDegraded {
		t.Fatalf("write result = %q, want degraded: the memory this round decided was not kept", writes[0].Result)
	}

	key, err := state.PlanNoDataHashKeyV2("alarmd", late.Identity)
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		ApplyVersion execution.ApplyVersion `json:"apply_version"`
	}
	if err := json.Unmarshal([]byte(client.HGet(ctx, key, "_meta").Val()), &header); err != nil {
		t.Fatal(err)
	}
	if header.ApplyVersion.EvaluationTime != 120 {
		t.Fatalf("the record holds round %d, want the newer round 120 untouched", header.ApplyVersion.EvaluationTime)
	}
}

// A memory at the default group bound is kept, and one over it is reported by
// name with both numbers while the Slot goes on, against a real server.
//
// decision-008 section 2: over the bound the write is refused by name, not
// thrown, and counted -- a Plan whose memory is refused goes on evaluating and
// only stops remembering. The default is decision-018 section 1.3's 100000,
// and the store here is opened without a bound of its own.
func TestAMemoryOverTheGroupBoundIsReportedWithoutFailingTheSlotOnARealServer(t *testing.T) {
	const limit = 100000
	for _, groups := range []int{limit, limit + 1} {
		t.Run(fmt.Sprintf("%d groups", groups), func(t *testing.T) {
			store, client := redisNoDataStore(t)
			ctx := context.Background()
			due := refusedMemoryDue(t)
			memory := make([]execution.NoDataGroupMemory, groups)
			for index := range memory {
				memory[index] = execution.NoDataGroupMemory{
					GroupKey: fmt.Sprintf("bk_target_cloud_id=0,bk_target_ip=192.0.%d.%d,%s=true",
						index/256, index%256, contract.NoDataDimensionTag),
					LastSeen: 1000,
				}
			}
			mutation := memoryOf(t, 60, memory)
			coordinator, observed := observingCoordinator(store)
			if err := coordinator.applyNoDataMemory(ctx, execution.SlotExecutionRequest{
				Operation: execution.OperationNormal, Contract: noDataPreflightContract(t, due),
			}, due, []execution.PlanNoDataMutation{mutation}); err != nil {
				t.Fatalf("a memory of %d groups failed the Slot: %v", groups, err)
			}
			key, err := state.PlanNoDataHashKeyV2("alarmd", mutation.Identity)
			if err != nil {
				t.Fatal(err)
			}
			writes := stagesOf(*observed, observability.StageNoDataMemoryWritten)
			refusals := stagesOf(*observed, observability.StageNoDataMemoryRefused)
			if groups == limit {
				if len(writes) != 1 || len(refusals) != 0 || writes[0].NoDataMemoryWrite == nil ||
					!writes[0].NoDataMemoryWrite.Stored {
					t.Fatalf("observations = %+v, want one stored write: the bound refuses only what is over it", *observed)
				}
				if fields := client.HLen(ctx, key).Val(); fields != int64(groups+1) {
					t.Fatalf("the record holds %d fields, want %d groups and the header", fields, groups)
				}
				return
			}
			if len(refusals) != 1 || len(writes) != 0 {
				t.Fatalf("observations = %+v, want one refusal line and no write line", *observed)
			}
			want := observability.NoDataMemoryRefusalFacts{
				Reason: contract.ReasonStateBudgetExceeded, Record: string(execution.NoDataRecordGroups),
				Groups: limit + 1, Limit: limit,
			}
			if refusals[0].NoDataMemoryRefusal == nil || *refusals[0].NoDataMemoryRefusal != want {
				t.Fatalf("refusal facts = %+v, want %+v", refusals[0].NoDataMemoryRefusal, want)
			}
			if exists := client.Exists(ctx, key).Val(); exists != 0 {
				t.Fatal("a refused memory left a record behind")
			}
		})
	}
}

// A renewal the server refuses is observed by name, the load still hands the
// round its memory, and the round goes on, against a real server.
//
// decision-008 section 4.5: a failed renewal is not the Plan's failure -- the
// record was read -- and it must report itself, on the renewal line, because
// the write family is correctly silent on a steady Plan and nothing else
// would. The record is written where scripting works and carried to a server
// whose scripting is renamed away, so the header and groups read and the
// renewal script is refused.
func TestARefusedRenewalIsObservedAndTheRoundGoesOnOnARealServer(t *testing.T) {
	ctx := context.Background()
	due := noDataWiredPlan(t)
	writer, writerClient := redisNoDataStore(t)
	absent := execution.NoDataGroupMemory{
		GroupKey: "bk_target_cloud_id=0,bk_target_ip=192.0.2.1," + contract.NoDataDimensionTag + "=true",
		LastSeen: 1_787_999_820, FirstAbsent: 1_787_999_880,
	}
	mutation, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationNone, Identity: due.NoDataIdentity(),
		ApplyVersion:     execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 1_787_999_940, SlotDigest: "previous"},
		ScheduleRevision: due.ScheduleRevision, RosterVersion: "HISTORY/1", PresentAsOf: 1_787_999_880,
		Memory: []execution.NoDataGroupMemory{absent},
	})
	if err != nil {
		t.Fatal(err)
	}
	retention, err := generationRetentionOf(due)
	if err != nil {
		t.Fatal(err)
	}
	written, err := writer.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: noDataPreflightContract(t, []execution.DuePlan{due}), Retention: retention,
		Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil || written.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("the previous round's write = (%+v, %v)", written, err)
	}

	reader, readerClient := redisNoDataStore(t, "--rename-command", "EVAL", "", "--rename-command", "EVALSHA", "")
	key, err := state.PlanNoDataHashKeyV2("alarmd", due.NoDataIdentity())
	if err != nil {
		t.Fatal(err)
	}
	fields := writerClient.HGetAll(ctx, key).Val()
	values := make([]interface{}, 0, 2*len(fields))
	for name, value := range fields {
		values = append(values, name, value)
	}
	if len(values) == 0 {
		t.Fatal("setup: nothing to carry across")
	}
	if err := readerClient.HSet(ctx, key, values...).Err(); err != nil {
		t.Fatal(err)
	}
	// Below half its life, so the load has a renewal to make.
	if err := readerClient.PExpire(ctx, key, 6*time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	stream := noDataWiredStream(t, due, reader)
	observed := make([]observability.Observation, 0, 4)
	stream.coordinator.ports.Observer = observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			observed = append(observed, observability.NormalizeObservation(observation))
		})
	if err := stream.loadNoDataMemory(ctx); err != nil {
		t.Fatalf("a refused renewal stopped the Slot's load: %v", err)
	}
	if snapshot := stream.noData.Items[0]; snapshot.Status != execution.NoDataMemoryFound ||
		len(snapshot.Groups) != 1 || snapshot.Groups[0] != absent {
		t.Fatalf("the round was handed %+v, want the memory the previous round wrote", snapshot)
	}
	renewals := stagesOf(observed, observability.StageNoDataMemoryRenewed)
	if len(renewals) != 1 {
		t.Fatalf("observations = %+v, want one renewal line", observed)
	}
	if renewals[0].Result != observability.ResultDegraded ||
		renewals[0].ReasonCode != observability.ReasonCode(contract.ReasonRedisUnavailable) {
		t.Fatalf("renewal line = %q / %q, want degraded with %s",
			renewals[0].Result, renewals[0].ReasonCode, contract.ReasonRedisUnavailable)
	}
	// Toward the one-day floor every generation-scoped key is at least given
	// (the lifetime mechanism's T = max(runtime lifetime, one day)); this
	// Plan's own runtime lifetime is minutes.
	want := observability.NoDataMemoryRenewalFacts{Renewed: false, TTLSeconds: 86400}
	if renewals[0].NoDataMemoryRenewal == nil || *renewals[0].NoDataMemoryRenewal != want {
		t.Fatalf("renewal facts = %+v, want %+v", renewals[0].NoDataMemoryRenewal, want)
	}

	round, err := stream.noDataRoundFor(due, nil, execution.CompletenessFull)
	if err != nil {
		t.Fatalf("the round after a refused renewal failed: %v", err)
	}
	if round.outcome != nodata.OutcomeEvaluated {
		t.Fatalf("outcome = %q, want the round judged from the memory it was handed", round.outcome)
	}
}
