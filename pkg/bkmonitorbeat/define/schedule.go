// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package define

import (
	"context"
	"time"
)

type scheduledAtContextKey struct{}

type scheduleExpiredContextKey struct{}

// WithScheduledAt records the exact occurrence selected by the scheduler.
func WithScheduledAt(ctx context.Context, scheduledAt time.Time) context.Context {
	return context.WithValue(ctx, scheduledAtContextKey{}, scheduledAt)
}

// ScheduledAtFromContext returns the exact occurrence selected by the scheduler.
func ScheduledAtFromContext(ctx context.Context) (time.Time, bool) {
	value, ok := ctx.Value(scheduledAtContextKey{}).(time.Time)
	return value, ok && !value.IsZero()
}

// WithScheduleExpired tells the task to emit an expired terminal result without
// performing the resource-consuming collection.
func WithScheduleExpired(ctx context.Context) context.Context {
	return context.WithValue(ctx, scheduleExpiredContextKey{}, true)
}

// ScheduleExpiredFromContext reports whether this occurrence was already
// expired when Beat registered it.
func ScheduleExpiredFromContext(ctx context.Context) bool {
	value, _ := ctx.Value(scheduleExpiredContextKey{}).(bool)
	return value
}
