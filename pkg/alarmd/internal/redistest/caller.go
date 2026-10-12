// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package redistest

import (
	"context"
	"sync"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// CallerHook records the job each Redis operation's context names
// (redisfailure.Caller), one entry per call or pipeline, for a test that
// checks a job tags the calls it makes.
type CallerHook struct {
	mu      sync.Mutex
	callers []string
}

func (h *CallerHook) note(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callers = append(h.callers, redisfailure.Caller(ctx))
}

// Callers is every job recorded so far, in order.
func (h *CallerHook) Callers() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.callers...)
}

func (h *CallerHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	h.note(ctx)
	return ctx, nil
}

func (h *CallerHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (h *CallerHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	h.note(ctx)
	return ctx, nil
}

func (h *CallerHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }
