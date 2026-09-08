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
	hook := recorder.RedisHook()
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
	if got := testutil.ToFloat64(hook.metrics.calls.WithLabelValues("get", "true")); got != 2 {
		t.Fatalf("pipelined get count = %v, want 2", got)
	}
	if got := testutil.ToFloat64(hook.metrics.calls.WithLabelValues("set", "true")); got != 1 {
		t.Fatalf("pipelined set count = %v, want 1", got)
	}
}

func TestRedisCallHookBoundsCommandNamesAndRecordsFailures(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook()
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
	if got := testutil.ToFloat64(hook.metrics.calls.WithLabelValues("other", "false")); got != 1 {
		t.Fatalf("unbounded command must collapse to other, got %v", got)
	}
	// redis.Nil is the empty-result signal, not a failure.
	if got := testutil.ToFloat64(hook.metrics.failures.WithLabelValues("other", "false")); got != 0 {
		t.Fatalf("redis.Nil must not count as a failure, got %v", got)
	}

	ctx, _ = hook.BeforeProcess(context.Background(), nil)
	failed := redis.NewStringCmd(ctx, "GET", "k")
	failed.SetErr(context.DeadlineExceeded)
	if err := hook.AfterProcess(ctx, failed); err != nil {
		t.Fatalf("AfterProcess: %v", err)
	}
	if got := testutil.ToFloat64(hook.metrics.failures.WithLabelValues("get", "false")); got != 1 {
		t.Fatalf("real error must count as a failure, got %v", got)
	}
}
