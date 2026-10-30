// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package profiling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/notifier"
	profilingStorage "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ActiveProfilingProcessor exposes the BMW task and standalone entry points.
type ActiveProfilingProcessor interface {
	Start(context.Context, chan<- error, []byte)
	GetTaskDimension([]byte) string
	StartByDataID(context.Context, uint64) error
	Run(context.Context)
	RunStandalone(string) error
}

// TaskParams are submitted to BMW to consume one DataID or the configured Consul prefix.
type TaskParams struct {
	DataID uint64 `json:"data_id"`
}

type activeProfiling struct {
	ctx            context.Context
	processor      *core.Processor
	metadataCenter *core.MetadataCenter
}

// Initial creates the Active Profiling processor from BMW's MySQL and Consul configuration.
func Initial(parentCtx context.Context) (ActiveProfilingProcessor, error) {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	store, err := profilingStorage.NewConfiguredMySQLStore()
	if err != nil {
		return nil, fmt.Errorf("initialize Active Profiling MySQL store: %w", err)
	}
	processor, err := core.NewProcessor(store, core.Options{
		WindowMaxDuration: config.ActiveProfilingWindowMaxDuration,
		WindowDelay:       config.ActiveProfilingWindowDelay,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Active Profiling processor: %w", err)
	}
	metadataCenter, err := core.NewConfiguredMetadataCenter()
	if err != nil {
		return nil, fmt.Errorf("initialize Active Profiling metadata center: %w", err)
	}
	return &activeProfiling{
		ctx:            parentCtx,
		processor:      processor,
		metadataCenter: metadataCenter,
	}, nil
}

// GetTaskDimension returns the DataID or Consul-prefix identity of a BMW daemon task.
func (p *activeProfiling) GetTaskDimension(payload []byte) string {
	return GetTaskDimension(payload)
}

// GetTaskDimension returns the DataID or Consul-prefix identity carried by a BMW task payload.
func GetTaskDimension(payload []byte) string {
	params, err := ParseTaskParams(payload)
	if err != nil {
		return ""
	}
	if params.DataID == 0 {
		return "consul-prefix"
	}
	return strconv.FormatUint(params.DataID, 10)
}

// ParseTaskParams decodes a BMW Active Profiling task payload. A zero DataID
// means that the task should load all DataIDs from the configured Consul prefix.
func ParseTaskParams(payload []byte) (TaskParams, error) {
	var params TaskParams
	if len(bytes.TrimSpace(payload)) == 0 {
		return params, nil
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		return TaskParams{}, fmt.Errorf("decode task payload: %w", err)
	}
	return params, nil
}

// Start runs one DataID task or all DataIDs under the configured Consul prefix.
func (p *activeProfiling) Start(runCtx context.Context, errorReceiveChan chan<- error, payload []byte) {
	params, err := ParseTaskParams(payload)
	if err != nil {
		p.reportError(runCtx, errorReceiveChan, fmt.Errorf("parse Active Profiling task payload: %w", err))
		return
	}
	if params.DataID == 0 {
		logger.Infof("start submitted Active Profiling task from Consul prefix")
		if err := p.startFromConsul(runCtx); err != nil && runCtx.Err() == nil {
			p.reportError(runCtx, errorReceiveChan, fmt.Errorf("run Active Profiling task from Consul prefix: %w", err))
		}
		return
	}

	logger.Infof("start submitted Active Profiling DataID task dataid=%d", params.DataID)
	if err := p.StartByDataID(runCtx, params.DataID); err != nil && runCtx.Err() == nil {
		p.reportError(runCtx, errorReceiveChan, fmt.Errorf("run Active Profiling DataID %d: %w", params.DataID, err))
	}
}

// StartByDataID loads the DataID configuration and consumes its Kafka topic.
func (p *activeProfiling) StartByDataID(ctx context.Context, dataID uint64) error {
	info, err := p.metadataCenter.GetDataIDInfo(ctx, dataID)
	if err != nil {
		return err
	}
	return p.startDataIDConsumer(ctx, dataID, info)
}

func (p *activeProfiling) startDataIDConsumer(ctx context.Context, dataID uint64, info core.DataIDInfo) error {
	kafkaNotifier, err := notifier.NewKafkaNotifier(
		p.processor, dataID, info.KafkaInfo, config.ActiveProfilingEventFlushInterval,
		config.ActiveProfilingEventQueueSize, config.ActiveProfilingEventBatchSize,
	)
	if err != nil {
		return err
	}
	return kafkaNotifier.Run(ctx)
}

func (p *activeProfiling) startFromConsul(ctx context.Context) error {
	return p.runConsulConsumers(ctx)
}

// Run starts the processor's periodic unknown-state sweep.
func (p *activeProfiling) Run(ctx context.Context) {
	if err := p.processor.RunUnknownSweeper(ctx); err != nil && ctx.Err() == nil {
		logger.Errorf("Active Profiling unknown-state sweeper stopped: %s", err)
	}
}

func (p *activeProfiling) reportError(ctx context.Context, errorReceiveChan chan<- error, err error) {
	if errorReceiveChan == nil {
		logger.Errorf("Active Profiling task failed: %s", err)
		return
	}
	select {
	case errorReceiveChan <- err:
	case <-ctx.Done():
	}
}

var _ ActiveProfilingProcessor = (*activeProfiling)(nil)
