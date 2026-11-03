// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package daemon

import (
	"context"
	"fmt"
	"sync"

	redis "github.com/go-redis/redis/v8"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ActiveProfilingTaskKind is the daemon task kind for one DataID or the Consul prefix.
const ActiveProfilingTaskKind = "daemon:profiling:active"

const activeProfilingQueue = "profiling"

// EnsureActiveProfilingTask registers the Consul-prefix task when this worker listens to the profiling queue.
func EnsureActiveProfilingTask(ctx context.Context, queues []string) error {
	if !config.ActiveProfilingEnabled {
		return nil
	}
	for _, queue := range queues {
		if queue == activeProfilingQueue {
			return enqueueActiveProfilingTask(ctx, rdb.GetRDB().Client())
		}
	}
	return nil
}

func enqueueActiveProfilingTask(ctx context.Context, client redis.UniversalClient) error {
	data, err := jsonx.Marshal(task.SerializerTask{
		Kind:    ActiveProfilingTaskKind,
		Payload: []byte("{}"),
		Options: task.Options{Queue: activeProfilingQueue},
	})
	if err != nil {
		return err
	}
	return client.SAdd(ctx, common.DaemonTaskKey(), data).Err()
}

type activeProfilingOperator struct {
	ctx context.Context

	initMu    sync.Mutex
	processor profiling.ActiveProfilingProcessor
}

func newActiveProfilingOperator(ctx context.Context) *activeProfilingOperator {
	if ctx == nil {
		ctx = context.Background()
	}
	return &activeProfilingOperator{ctx: ctx}
}

func (o *activeProfilingOperator) Start(runCtx context.Context, errorReceiveChan chan<- error, payload []byte) {
	if !config.ActiveProfilingEnabled {
		return
	}
	if _, err := profiling.ParseTaskParams(payload); err != nil {
		o.reportError(runCtx, errorReceiveChan, fmt.Errorf("parse Active Profiling task payload: %w", err))
		return
	}
	if err := o.initialize(); err != nil {
		o.reportError(runCtx, errorReceiveChan, err)
		return
	}
	o.processor.Start(runCtx, errorReceiveChan, payload)
}

func (o *activeProfilingOperator) GetTaskDimension(payload []byte) string {
	return profiling.GetTaskDimension(payload)
}

func (o *activeProfilingOperator) initialize() error {
	o.initMu.Lock()
	defer o.initMu.Unlock()
	if o.processor != nil {
		return nil
	}
	processor, err := profiling.Initial(o.ctx)
	if err != nil {
		return fmt.Errorf("initialize Active Profiling processor: %w", err)
	}
	o.processor = processor
	go processor.Run(o.ctx)
	return nil
}

func (o *activeProfilingOperator) reportError(ctx context.Context, errorReceiveChan chan<- error, err error) {
	if errorReceiveChan == nil {
		logger.Errorf("[profiling] active profiling task failed: %s", err)
		return
	}
	select {
	case errorReceiveChan <- err:
	case <-ctx.Done():
	}
}
