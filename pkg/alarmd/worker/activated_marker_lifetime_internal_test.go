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
	"bytes"
	"context"
	"net"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// sixtyHours is the interval of the Plan these cases follow, and the time
// that passes between its rounds.
const sixtyHours = 60 * time.Hour

// startLifetimeRedis starts a redis-server of its own for one case.
func startLifetimeRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	executable := redistest.Server(t)
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
	command := exec.Command(executable, "--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no",
		"--dir", t.TempDir(), "--daemonize", "no", "--loglevel", "warning")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatalf("redis-server start error = %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	for deadline := time.Now().Add(5 * time.Second); client.Ping(context.Background()).Err() != nil; {
		if time.Now().After(deadline) {
			t.Fatalf("redis-server did not become ready: %s", output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return address, client
}

// openLifetimeStore is the execution store on that server under the
// production defaults for the key lifetimes: a minute to thirty days, with a
// ten-minute restart margin.
func openLifetimeStore(t *testing.T, address string) *state.ExecutionStore {
	t.Helper()
	backend, err := state.NewRedisBackend(state.RedisBackendOptions{
		Address: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	router, err := state.NewFixedRouter("activated-marker-lifetime", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.NewExecutionStore(state.ExecutionStoreOptions{
		Prefix: "alarmd", Router: router, MaxValueBytes: 1 << 20, MaxItemsPerCall: 16,
		MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: 10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// ageRedis lets elapsed pass for every key with a lifetime, the one clock
// this case depends on and the one a test cannot move on a real server: a key
// with no more than elapsed left is gone, the rest have elapsed less.
func ageRedis(t *testing.T, client *redis.Client, elapsed time.Duration) {
	t.Helper()
	ctx := context.Background()
	keys, err := client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		left, err := client.PTTL(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case left < 0:
			// No lifetime: time does not end it.
		case left <= elapsed:
			if err := client.Del(ctx, key).Err(); err != nil {
				t.Fatal(err)
			}
		default:
			if err := client.PExpire(ctx, key, left-elapsed).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type admitEverything struct{}

func (admitEverything) Check(context.Context, execution.SideEffectAdmissionRequest) (execution.SideEffectAdmissionResult, error) {
	return execution.SideEffectAdmissionResult{Admitted: true, ReasonCode: observability.ReasonNone}, nil
}

// sixtyHourRound is one round of a sixty-hour Plan: its request and the
// header its gap load is built from.
func sixtyHourRound(t *testing.T, due execution.DuePlan, evaluationTime execution.EvaluationTime) (execution.SlotExecutionRequest, execution.InternalExecutionHeader) {
	t.Helper()
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: evaluationTime},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: 1_787_999_940, DuePlanSetDigest: "due-set-v1",
	}
	request := execution.SlotExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
		OwnerFence:       execution.OwnerFence{QueryGroup: "query-group", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"},
		ExpectedNextSlot: evaluationTime,
	}
	return request, execution.InternalExecutionHeader{Contract: contractRef, DuePlans: []execution.DuePlan{due}}
}

// loadRound loads the round's gap markers the way an evaluated round does,
// with the Plan's own retention.
func loadRound(t *testing.T, store *state.ExecutionStore, header execution.InternalExecutionHeader) execution.GapLoadResult {
	t.Helper()
	items, err := gapPreflightForHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	request := execution.GapLoadRequest{Contract: header.Contract, Items: items}
	loaded, err := store.LoadGaps(context.Background(), request)
	if err == nil {
		err = execution.ValidateGapLoad(request, loaded)
	}
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// A marker written from the activation record alone -- the forced warming a
// changed or returning Plan starts with, and the guard a Slot finished
// without a query puts down -- is still there when a sixty-hour Plan's next
// round loads it.
//
// For the forced warming, missing was a loop: the activation keeps asking to
// warm up until the Plan changes again, so the next round, finding no marker,
// wrote it again and finished without evaluating, and a Plan whose rounds are
// further apart than the marker lived did that at every round, never
// evaluating again. Written for a day, the marker was gone after sixty hours.
func TestAMarkerWrittenFromTheActivationOutlivesASixtyHourPlansNextRound(t *testing.T) {
	for name, test := range map[string]struct {
		forceWarming bool
		reason       execution.ReasonCode
		reuse        bool
	}{
		"the forced warming of a changed Plan": {
			forceWarming: true, reason: execution.ReasonCode(contract.ReasonConfigDrift),
		},
		"the guard of a Slot finished without a query": {
			reason: execution.ReasonCode(contract.ReasonSnapshotUnavailable), reuse: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			address, client := startLifetimeRedis(t)
			store := openLifetimeStore(t, address)
			plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9002"}
			due := execution.DuePlan{
				Identity: plan, CompiledPlan: internalCompiledPlan(t, "9002", 1, uint32(sixtyHours/time.Second)),
				StateGeneration: "generation", StateApplyEpoch: 1, ScheduleRevision: "plan-r1",
			}
			activations := execution.PlanActivationResult{Facts: []execution.PlanActivationFact{{
				Plan: plan, Selection: execution.ActivationCurrent,
				Selected: execution.ActivatedPlan{
					Identity: plan, StateGeneration: "generation", StateApplyEpoch: 1, ScheduleRevision: "plan-r1",
					RequiredFullSlots: 1, ForceWarming: test.forceWarming,
				},
			}}}
			coordinator := &SlotExecutionCoordinator{
				ports: Ports{GapGuard: store, Admission: admitEverything{},
					Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})},
				budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10},
			}

			first := execution.EvaluationTime(1_788_048_000)
			request, header := sixtyHourRound(t, due, first)
			activations.Contract = request.Contract
			// The round finds no marker for the new generation.
			loaded := loadRound(t, store, header)
			if marker, found := loaded.Find(due.GapIdentity()); !found || marker.Status != execution.GapMissing {
				t.Fatalf("setup: the first round reads %+v, want no marker", marker)
			}
			// And the activation asks for warming: this is the round that
			// writes it.
			if forced := unsatisfiedForcedWarmingActivations(activations, loaded, nil); len(forced.Facts) != 1 && test.forceWarming {
				t.Fatalf("setup: the first round forces %+v, want the changed Plan", forced.Facts)
			}
			if _, err := coordinator.ensureActivatedPlanGaps(context.Background(), request, test.reason, activations, test.reuse); err != nil {
				t.Fatalf("ensureActivatedPlanGaps() error = %v", err)
			}

			ageRedis(t, client, sixtyHours)

			_, next := sixtyHourRound(t, due, first+execution.EvaluationTime(sixtyHours/time.Second))
			loaded = loadRound(t, store, next)
			if forced := unsatisfiedForcedWarmingActivations(activations, loaded, nil); len(forced.Facts) != 0 {
				t.Fatalf("the next round forces warming again for %+v: it would finish without evaluating, as every round after it would", forced.Facts)
			}
			if marker, found := loaded.Find(due.GapIdentity()); !found || marker.Status != execution.GapFound {
				t.Fatalf("the next round, sixty hours on, reads the marker as %+v: it did not outlive the interval", marker)
			}
		})
	}
}

// Each Plan written in one request lives for its own retention, not for the
// first one's: two Plans of one Slot can run on different intervals.
func TestEachPlansRetentionIsItsOwn(t *testing.T) {
	minute := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9001"},
		CompiledPlan: internalCompiledPlan(t, "9001", 1, 60)}
	sixty := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9002"},
		CompiledPlan: internalCompiledPlan(t, "9002", 1, uint32(sixtyHours/time.Second))}
	retention, err := generationRetentionOf(minute, sixty)
	if err != nil {
		t.Fatal(err)
	}
	for _, due := range []execution.DuePlan{minute, sixty} {
		want, err := execution.DeriveStateRetentionRequirement(due.CompiledPlan)
		if err != nil {
			t.Fatal(err)
		}
		if got := retention.ByPlan[due.Identity]; !reflect.DeepEqual(got, want) {
			t.Fatalf("Plan %s written with %+v, want its own %+v", due.Identity.StrategyID, got, want)
		}
	}
	if reflect.DeepEqual(retention.ByPlan[minute.Identity], retention.ByPlan[sixty.Identity]) {
		t.Fatal("setup: the two Plans have the same retention")
	}
}
