// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRedisCallHookCountsEveryPipelinedMember(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	if hook == nil {
		t.Fatal("recorder must expose a Redis hook")
	}
	moment := time.Unix(0, 0)
	hook.now = func() time.Time { return moment }

	ctx, err := hook.BeforeProcessPipeline(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeforeProcessPipeline: %v", err)
	}
	moment = moment.Add(3 * time.Millisecond)
	batch := []redis.Cmder{
		redis.NewStringCmd(ctx, "get", "a"),
		redis.NewStringCmd(ctx, "get", "b"),
		redis.NewStatusCmd(ctx, "set", "c", "1"),
	}
	if err := hook.AfterProcessPipeline(ctx, batch); err != nil {
		t.Fatalf("AfterProcessPipeline: %v", err)
	}

	// The shared Redis master is billed per command, so a batch of three must
	// count as three, not as one round trip.
	if got := testutil.ToFloat64(hook.metrics.calls.WithLabelValues("runtime", "get", "true")); got != 2 {
		t.Fatalf("pipelined get count = %v, want 2", got)
	}
	if got := testutil.ToFloat64(hook.metrics.calls.WithLabelValues("runtime", "set", "true")); got != 1 {
		t.Fatalf("pipelined set count = %v, want 1", got)
	}
}

func TestRedisCallHookBoundsCommandNamesAndRecordsFailures(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	moment := time.Unix(0, 0)
	hook.now = func() time.Time { return moment }

	ctx, err := hook.BeforeProcess(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeforeProcess: %v", err)
	}
	moment = moment.Add(time.Millisecond)
	unknown := redis.NewStringCmd(ctx, "bitfield_ro", "k")
	unknown.SetErr(redis.Nil)
	if err := hook.AfterProcess(ctx, unknown); err != nil {
		t.Fatalf("AfterProcess: %v", err)
	}
	if got := testutil.ToFloat64(hook.metrics.calls.WithLabelValues("runtime", "other", "false")); got != 1 {
		t.Fatalf("unbounded command must collapse to other, got %v", got)
	}
	// redis.Nil is the empty-result signal, not a failure.
	if got := testutil.ToFloat64(hook.metrics.failures.WithLabelValues("runtime", "other", "false")); got != 0 {
		t.Fatalf("redis.Nil must not count as a failure, got %v", got)
	}

	ctx, _ = hook.BeforeProcess(context.Background(), nil)
	failed := redis.NewStringCmd(ctx, "GET", "k")
	failed.SetErr(context.DeadlineExceeded)
	if err := hook.AfterProcess(ctx, failed); err != nil {
		t.Fatalf("AfterProcess: %v", err)
	}
	if got := testutil.ToFloat64(hook.metrics.failures.WithLabelValues("runtime", "get", "false")); got != 1 {
		t.Fatalf("real error must count as a failure, got %v", got)
	}
}

// Retries leave no trace of their own: the hook times the whole retry loop and
// only the final outcome reaches the failure counter. Counting operations makes
// them derivable, because the pool counts one acquisition per attempt.
func TestRedisOperationCounterCountsCallsNotCommands(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	ctx := context.Background()
	single, err := hook.BeforeProcess(ctx, redis.NewStringCmd(ctx, "get", "k"))
	if err != nil {
		t.Fatal(err)
	}
	if err := hook.AfterProcess(single, redis.NewStringCmd(ctx, "get", "k")); err != nil {
		t.Fatal(err)
	}
	batch := []redis.Cmder{redis.NewStatusCmd(ctx, "set", "a", "1"), redis.NewStatusCmd(ctx, "set", "b", "2")}
	batched, err := hook.BeforeProcessPipeline(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if err := hook.AfterProcessPipeline(batched, batch); err != nil {
		t.Fatal(err)
	}
	operations := testutil.ToFloat64(hook.metrics.operations)
	if operations != 2 {
		t.Fatalf("operations = %v, want 2 (one call plus one batch, not three commands)", operations)
	}
}

// "Redis 命令率涨了" 在这个进程里是不可行动的信息：它同时开着控制面、运行时和
// 兼容输出几个客户端。没有这一维，诊断突发和控制面负载在指标上长得一模一样，
// 而把这两个客户端分开正是当初拆连接池的全部理由。
func TestRedisCallHookAttributesLoadToItsClient(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	source := recorder.RedisHook("source")
	runtime := recorder.RedisHook("runtime")

	for _, hook := range []*RedisCallHook{source, source, runtime} {
		ctx, err := hook.BeforeProcess(context.Background(), redis.NewStringCmd(context.Background(), "get", "k"))
		if err != nil {
			t.Fatal(err)
		}
		if err := hook.AfterProcess(ctx, redis.NewStringCmd(context.Background(), "get", "k")); err != nil {
			t.Fatal(err)
		}
	}

	if got := testutil.ToFloat64(source.metrics.calls.WithLabelValues("source", "get", "false")); got != 2 {
		t.Fatalf("source calls = %v, want 2", got)
	}
	if got := testutil.ToFloat64(source.metrics.calls.WithLabelValues("runtime", "get", "false")); got != 1 {
		t.Fatalf("runtime calls = %v, want 1", got)
	}
	if got := testutil.ToFloat64(source.metrics.operations.WithLabelValues("source")); got != 2 {
		t.Fatalf("source operations = %v, want 2", got)
	}
}

// 客户端名字写在接线代码里，写错一个就会凭空多一条 series，而且没人会发现。
func TestRedisCallHookClosesTheClientLabel(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("a-client-that-was-never-declared")
	if hook.client != "other" {
		t.Fatalf("client = %q, want the closed bucket", hook.client)
	}
}
