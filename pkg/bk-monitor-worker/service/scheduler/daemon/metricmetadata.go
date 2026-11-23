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

	redis "github.com/go-redis/redis/v8"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// MetricMetadataTaskKind consumes messages using the fixed Consul Kafka config.
	MetricMetadataTaskKind = "daemon:metricmetadata:consume"
	// MetricMetadataQueueName is the dedicated queue for the metric metadata daemon task.
	MetricMetadataQueueName = "metricmetadata"
)

// EnsureMetricMetadataTask registers the task on the dedicated queue when enabled, otherwise on default.
func EnsureMetricMetadataTask(ctx context.Context, queues []string) error {
	queue := selectMetricMetadataQueue(queues)
	if queue == "" {
		return nil
	}
	return enqueueMetricMetadataTask(ctx, rdb.GetRDB().Client(), queue)
}

func selectMetricMetadataQueue(queues []string) string {
	defaultEnabled := false
	for _, queue := range queues {
		if queue == MetricMetadataQueueName {
			return queue
		}
		if queue == common.DefaultQueueName {
			defaultEnabled = true
		}
	}
	if defaultEnabled {
		return common.DefaultQueueName
	}
	return ""
}

func enqueueMetricMetadataTask(ctx context.Context, client redis.UniversalClient, queue string) error {
	data, err := jsonx.Marshal(task.SerializerTask{
		Kind:    MetricMetadataTaskKind,
		Payload: []byte("{}"),
		Options: task.Options{Queue: queue},
	})
	if err != nil {
		return err
	}
	return client.SAdd(ctx, common.DaemonTaskKey(), data).Err()
}

type metricMetadataOperator struct{}

func (metricMetadataOperator) Start(ctx context.Context, errorReceiveChan chan<- error, _ []byte) {
	err := metricmetadata.RunStandalone(ctx)
	if err == nil || ctx.Err() != nil {
		return
	}
	if errorReceiveChan == nil {
		logger.Errorf("metric metadata consumer failed: %s", err)
		return
	}
	select {
	case errorReceiveChan <- fmt.Errorf("run metric metadata consumer: %w", err):
	case <-ctx.Done():
	}
}

func (metricMetadataOperator) GetTaskDimension([]byte) string {
	return metricmetadata.GetTaskDimension(nil)
}
