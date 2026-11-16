// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metadata

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

var ErrBackendResponseTooLarge = errors.New("backend response exceeds its maximum size")

type BackendResponseTooLargeError struct {
	Message string
}

func (e *BackendResponseTooLargeError) Error() string {
	if e == nil || e.Message == "" {
		return ErrBackendResponseTooLarge.Error()
	}
	return fmt.Sprintf("%s: %s", ErrBackendResponseTooLarge, e.Message)
}

func (e *BackendResponseTooLargeError) Unwrap() error {
	return ErrBackendResponseTooLarge
}

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

type backendReadStatsKey struct{}

// BackendReadStats 汇总一个请求内收到响应的后端读取：次数、首字节前等待与响应字节。
// 逐次读取的 span 可能不入库，汇总值仍能拆出后端耗时。
type BackendReadStats struct {
	count, bytes, firstByteNanos atomic.Int64
}

// WithBackendReadStats 让派生 context 上的后端读取计入同一份汇总。
func WithBackendReadStats(ctx context.Context) (context.Context, *BackendReadStats) {
	stats := &BackendReadStats{}
	return context.WithValue(ctx, backendReadStatsKey{}, stats), stats
}

// RecordBackendRead 记录一次收到响应的后端读取；context 上没有汇总时不记录。
func RecordBackendRead(ctx context.Context, bytes int, firstByte time.Duration) {
	stats, ok := ctx.Value(backendReadStatsKey{}).(*BackendReadStats)
	if !ok || stats == nil {
		return
	}
	stats.count.Add(1)
	stats.bytes.Add(int64(bytes))
	stats.firstByteNanos.Add(int64(firstByte))
}

// Snapshot 返回已记录的读取次数、响应字节与首字节前等待之和。
func (s *BackendReadStats) Snapshot() (count, bytes int64, firstByte time.Duration) {
	if s == nil {
		return 0, 0, 0
	}
	return s.count.Load(), s.bytes.Load(), time.Duration(s.firstByteNanos.Load())
}
