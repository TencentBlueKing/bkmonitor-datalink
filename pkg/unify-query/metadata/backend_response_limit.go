// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metadata

import (
	"context"
	"sync/atomic"
)

type backendResponseLimitKey struct{}

type backendResponseBudget struct {
	bytes    int64
	exceeded atomic.Bool
}

// WithBackendResponseLimit 限制一次后端响应的解压后大小。拒绝状态由派生
// context 共享，防止聚合后端把容量拒绝降级为普通 partial 后继续构图。
func WithBackendResponseLimit(ctx context.Context, bytes int64) context.Context {
	return context.WithValue(ctx, backendResponseLimitKey{}, &backendResponseBudget{bytes: bytes})
}

func BackendResponseLimit(ctx context.Context) int64 {
	if budget, ok := ctx.Value(backendResponseLimitKey{}).(*backendResponseBudget); ok {
		return budget.bytes
	}
	return 0
}

func MarkBackendResponseLimitExceeded(ctx context.Context) {
	if budget, ok := ctx.Value(backendResponseLimitKey{}).(*backendResponseBudget); ok {
		budget.exceeded.Store(true)
	}
}

func BackendResponseLimitExceeded(ctx context.Context) bool {
	budget, ok := ctx.Value(backendResponseLimitKey{}).(*backendResponseBudget)
	return ok && budget.exceeded.Load()
}
