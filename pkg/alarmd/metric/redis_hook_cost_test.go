// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/racedetector"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// What the script, caller and byte accounting adds to one operation, on the
// real families. The hook runs on every command of every Slot.
const (
	hookAddedSingleLimit = 2 * time.Microsecond
	hookAddedBatchLimit  = 30 * time.Microsecond
)

func fenceLikeCommand(ctx context.Context) *redis.Cmd {
	cmd := redis.NewCmd(ctx, "evalsha", testScriptSHA, 2, "alarmd:ownership:{query-group}:assignment",
		"alarmd:ownership:{query-group}:ownership", "1", "worker-identity", "1234567", "lease-token-0123456789abcdef", "content-scope")
	cmd.SetVal([]interface{}{"OK", int64(1700000000000), "worker-identity", int64(1234567), "lease-token-0123456789abcdef", "content-scope", int64(1), "", "", int64(0), int64(0)})
	return cmd
}

func stateLikeBatch(ctx context.Context, writes int) []redis.Cmder {
	value := strings.Repeat("v", 1287)
	args := []interface{}{"evalsha", testScriptSHA, 2 + writes, "assignment", "ownership", "1", writes, "1", "worker-identity", "1234567", "lease-token", "content-scope"}
	reply := make([]interface{}, writes)
	for index := 0; index < writes; index++ {
		args = append(args, "alarmd:state:{query-group}:series-0123456789", value, "0123456789012345678901234567890123456789", "86400000", "0")
		reply[index] = "APPLIED"
	}
	cmd := redis.NewCmd(ctx, args...)
	cmd.SetVal(reply)
	return []redis.Cmder{scriptLoadCommand(ctx, testScriptBody), cmd}
}

func hookForCost() (*RedisCallHook, context.Context) {
	return NewRecorder(BuildInfo{}).RedisHook("runtime"), redisfailure.WithCaller(context.Background(), redisfailure.CallerOwnership)
}

// addedForOne and addedForBatch are the work this accounting adds and nothing
// the hook did before it, so the limit is on what it costs.
func addedForOne(ctx context.Context, hook *RedisCallHook, cmd redis.Cmder) {
	caller := callerLabel(ctx)
	command := cmd.Name()
	hook.metrics.callerDuration.WithLabelValues(hook.client, caller, "false").Observe(0.001)
	script, scriptCommand := hook.account(cmd, command, boundedRedisCommand(command), caller, "false")
	if script != "" && scriptCommand != "load" {
		hook.metrics.scriptDuration.WithLabelValues(script, "false").Observe(0.001)
	}
}

func addedForBatch(ctx context.Context, hook *RedisCallHook, cmds []redis.Cmder) {
	caller := callerLabel(ctx)
	hook.metrics.callerDuration.WithLabelValues(hook.client, caller, "true").Observe(0.001)
	timed := false
	for _, cmd := range cmds {
		command := cmd.Name()
		script, scriptCommand := hook.account(cmd, command, boundedRedisCommand(command), caller, "true")
		if !timed && script != "" && scriptCommand != "load" {
			hook.metrics.scriptDuration.WithLabelValues(script, "true").Observe(0.001)
			timed = true
		}
	}
}

func fastestOf(rounds, iterations int, work func()) time.Duration {
	fastest := time.Duration(1<<63 - 1)
	for round := 0; round < rounds; round++ { // fixed-wait: this measures the work, nothing is waited for
		started := time.Now()
		for iteration := 0; iteration < iterations; iteration++ {
			work()
		}
		if elapsed := time.Since(started) / time.Duration(iterations); elapsed < fastest {
			fastest = elapsed
		}
	}
	return fastest
}

func TestTheAddedHookWorkAllocatesNothingOnASingleCall(t *testing.T) {
	if racedetector.Enabled {
		t.Skip("the race detector allocates")
	}
	hook, ctx := hookForCost()
	cmd := fenceLikeCommand(ctx)
	addedForOne(ctx, hook, cmd)
	if allocs := testing.AllocsPerRun(1000, func() { addedForOne(ctx, hook, cmd) }); allocs != 0 {
		t.Fatalf("%v allocations per call", allocs)
	}
	batch := stateLikeBatch(ctx, 500)
	addedForBatch(ctx, hook, batch)
	if allocs := testing.AllocsPerRun(100, func() { addedForBatch(ctx, hook, batch) }); allocs != 0 {
		t.Fatalf("%v allocations per batch", allocs)
	}
}

// The fastest of several rounds, so that a loaded machine reads as what the
// code costs: the bound is on the code.
func TestTheAddedHookWorkStaysWithinItsBudget(t *testing.T) {
	if racedetector.Enabled {
		t.Skip("the race detector is not what the budget is for")
	}
	hook, ctx := hookForCost()
	cmd := fenceLikeCommand(ctx)
	single := fastestOf(7, 5000, func() { addedForOne(ctx, hook, cmd) })
	batch := stateLikeBatch(ctx, 500)
	large := fastestOf(7, 500, func() { addedForBatch(ctx, hook, batch) })
	t.Logf("a single call adds %v, a batch of 500 writes %v", single, large)
	if single > hookAddedSingleLimit {
		t.Errorf("a single call adds %v, the budget is %v", single, hookAddedSingleLimit)
	}
	if large > hookAddedBatchLimit {
		t.Errorf("a batch of 500 writes adds %v, the budget is %v", large, hookAddedBatchLimit)
	}
}

func BenchmarkRedisHookSingleCall(b *testing.B) {
	hook, ctx := hookForCost()
	cmd := fenceLikeCommand(ctx)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		issued, _ := hook.BeforeProcess(ctx, cmd)
		_ = hook.AfterProcess(issued, cmd)
	}
}

func BenchmarkRedisHookAddedSingleCall(b *testing.B) {
	hook, ctx := hookForCost()
	cmd := fenceLikeCommand(ctx)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		addedForOne(ctx, hook, cmd)
	}
}

func BenchmarkRedisHookAddedBatch50(b *testing.B) {
	hook, ctx := hookForCost()
	batch := stateLikeBatch(ctx, 50)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		addedForBatch(ctx, hook, batch)
	}
}

func BenchmarkRedisHookAddedBatch500(b *testing.B) {
	hook, ctx := hookForCost()
	batch := stateLikeBatch(ctx, 500)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		addedForBatch(ctx, hook, batch)
	}
}
