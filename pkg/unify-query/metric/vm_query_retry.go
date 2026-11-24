// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var vmQuerySyncHTTP2RetryEvents = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "unify_query",
	Name:      "vm_query_sync_http2_retry_events_total",
	Help:      "VM query_sync retries after recognized HTTP/2 connection closures, by reason and bounded outcome event",
}, []string{"event", "reason"})

var vmQuerySyncWriteRetryEvents = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "unify_query",
	Name:      "vm_query_sync_write_retry_events_total",
	Help:      "VM query_sync retries after a typed network write EPIPE, by bounded outcome event",
}, []string{"event"})

// VMQuerySyncRetryInc 只接受固定事件与原因，避免将 URL、错误原文或请求字段写进标签。
func VMQuerySyncRetryInc(ctx context.Context, event, reason string) {
	switch event {
	case "attempted", "recovered", "failed", "canceled":
	default:
		return
	}
	switch reason {
	case "client_conn_close", "goaway_no_error":
		counterInc(ctx, vmQuerySyncHTTP2RetryEvents.WithLabelValues(event, reason))
	case "write_broken_pipe":
		counterInc(ctx, vmQuerySyncWriteRetryEvents.WithLabelValues(event))
	}
}
