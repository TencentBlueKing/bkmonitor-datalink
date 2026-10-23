// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// Two jobs on one shared client: each job's clone counts its operations
// under its own name beside the client's, a call that names its job itself
// keeps that name, the client itself names none, and every one of them
// went through the one connection pool the client opened.
func TestAJobsCloneOfASharedClientNamesItsCallerOnTheSamePool(t *testing.T) {
	_, server := startPhaseTwoRedis(t)
	recorder := metric.NewRecorder(metric.BuildInfo{Version: "test"})
	base := redis.NewClient(server.Options())
	base.AddHook(recorder.RedisHook("source"))
	t.Cleanup(func() { _ = base.Close() })
	stateStore := redisForCaller(base, redisfailure.CallerRuntimeState)
	strategies := redisForCaller(base, redisfailure.CallerStrategySource)

	ctx := context.Background()
	if err := stateStore.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { return strategies.Get(ctx, "k").Err() },
		func() error { return strategies.Get(redisfailure.WithCaller(ctx, redisfailure.CallerFleet), "k").Err() },
		func() error { return base.Get(ctx, "k").Err() },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_redis_caller_operation_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range series.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			counts[labels["client"]+"/"+labels["caller"]] += series.GetCounter().GetValue()
		}
	}
	for cell, want := range map[string]float64{"source/runtime_state": 1, "source/strategy_source": 1, "source/fleet": 1} {
		if counts[cell] != want {
			t.Errorf("%s = %v, want %v; all %v", cell, counts[cell], want, counts)
		}
	}
	if stats := base.PoolStats(); stats.Hits+stats.Misses < 4 {
		t.Fatalf("the client's pool served %d of the 4 calls: a clone opened a pool of its own", stats.Hits+stats.Misses)
	}
	if _, ok := redisForCaller(nil, redisfailure.CallerFleet).(*redis.Client); ok {
		t.Fatal("a nil client came back as a client")
	}
}

// callerCounts is the caller families of a recorder: operations by
// client/caller, and failures by client/caller/reason.
func callerCounts(t *testing.T, recorder *metric.Recorder) (map[string]float64, map[string]float64) {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	operations, failures := map[string]float64{}, map[string]float64{}
	for _, family := range families {
		for _, series := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range series.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			switch family.GetName() {
			case "bkmonitor_alarmd_redis_caller_operation_total":
				operations[labels["client"]+"/"+labels["caller"]] += series.GetCounter().GetValue()
			case "bkmonitor_alarmd_redis_caller_failure_reason_total":
				failures[labels["client"]+"/"+labels["caller"]+"/"+labels["reason"]] += series.GetCounter().GetValue()
			}
		}
	}
	return operations, failures
}

// A pipeline and a transaction through a job's clone count once each for
// the job, as one operation of the client, whatever they carry: counted
// after every hook has run, once per batch as before, none of their
// commands missed and none twice.
func TestAJobsPipelinesCountOncePerBatch(t *testing.T) {
	_, server := startPhaseTwoRedis(t)
	recorder := metric.NewRecorder(metric.BuildInfo{Version: "test"})
	base := redis.NewClient(server.Options())
	base.AddHook(recorder.RedisHook("runtime"))
	t.Cleanup(func() { _ = base.Close() })
	stateStore := redisForCaller(base, redisfailure.CallerRuntimeState)
	ctx := context.Background()
	if _, err := stateStore.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, "a", "1", 0)
		pipe.Set(ctx, "b", "2", 0)
		pipe.Get(ctx, "a")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Incr(ctx, "n")
		pipe.Incr(ctx, "n")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	operations, _ := callerCounts(t, recorder)
	if operations["runtime/runtime_state"] != 2 {
		t.Fatalf("operations %v, want the pipeline and the transaction once each under runtime_state", operations)
	}
}

// A call that fails - its caller's deadline already past - is counted for
// its job as an operation and as a failure by why: moving the count after
// the call lost neither.
func TestAJobsFailedCallIsCountedWithItsReason(t *testing.T) {
	_, server := startPhaseTwoRedis(t)
	recorder := metric.NewRecorder(metric.BuildInfo{Version: "test"})
	base := redis.NewClient(server.Options())
	base.AddHook(recorder.RedisHook("runtime"))
	t.Cleanup(func() { _ = base.Close() })
	stateStore := redisForCaller(base, redisfailure.CallerRuntimeState)
	spent, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := stateStore.Get(spent, "a").Err(); err == nil {
		t.Fatal("a call on a spent deadline succeeded")
	}
	if _, err := stateStore.Pipelined(spent, func(pipe redis.Pipeliner) error {
		pipe.Get(spent, "a")
		return nil
	}); err == nil {
		t.Fatal("a pipeline on a spent deadline succeeded")
	}
	operations, failures := callerCounts(t, recorder)
	if operations["runtime/runtime_state"] != 2 || failures["runtime/runtime_state/timeout"] != 2 {
		t.Fatalf("operations %v failures %v, want both failed calls counted under runtime_state, as timeouts", operations, failures)
	}
}
