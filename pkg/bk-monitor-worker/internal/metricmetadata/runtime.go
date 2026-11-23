// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

// Package metricmetadata assembles Kafka ingestion, state processing, periodic
// snapshot refresh, and transactional MySQL persistence for metric reports.
package metricmetadata

import (
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/window"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Run consumes one transport DataID and persists its reports in MySQL.
// A zero DataID resolves from Consul; independent and daemon entry points share this lifecycle.
func Run(ctx context.Context, dataID uint) error {
	if ctx == nil {
		return errors.New("metric metadata context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	retryInterval := optionsFromConfig().RetryInterval
	waitForRetry := func() error {
		timer := time.NewTimer(retryInterval)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		instance, err := newRunInstance(ctx, dataID)
		if err != nil {
			if !storage.IsTimeout(err) {
				return err
			}
			logger.Warnf("metric metadata startup timed out; retrying dataid=%d retry_in=%s err=%v", dataID, retryInterval, err)
			if err := waitForRetry(); err != nil {
				return err
			}
			continue
		}
		dataID = instance.dataID

		err = func() error {
			defer func() {
				if closeErr := instance.db.Close(); closeErr != nil {
					logger.Warnf("metric metadata MySQL close failed dataid=%d err=%v", dataID, closeErr)
				}
			}()
			window.RegisterProcessor(instance.window)
			defer window.UnregisterProcessor(instance.window)
			return instance.Run(ctx)
		}()
		if err == nil || ctx.Err() != nil || !storage.IsTimeout(err) {
			return err
		}
		// 失败批次尚未标记 offset；重建实例后 Kafka 会从已提交位置重放。
		logger.Warnf("metric metadata run timed out; retrying dataid=%d retry_in=%s err=%v", dataID, retryInterval, err)
		if err := waitForRetry(); err != nil {
			return err
		}
	}
}

// GetTaskDimension identifies the singleton daemon task backed by the fixed Consul key.
func GetTaskDimension(_ []byte) string {
	return ConsulKafkaConfigKey
}
