// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package profiling

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type consulConsumer struct {
	info   core.DataIDInfo
	cancel context.CancelFunc
	done   chan struct{}
}

type consulConsumerFailure struct {
	dataID   uint64
	consumer *consulConsumer
	err      error
}

type consulConsumerManager struct {
	processor *activeProfiling
	ctx       context.Context
	consumers map[uint64]*consulConsumer
	pending   map[uint64]struct{}
	failures  chan consulConsumerFailure
}

func newConsulConsumerManager(processor *activeProfiling, ctx context.Context) *consulConsumerManager {
	return &consulConsumerManager{
		processor: processor,
		ctx:       ctx,
		consumers: make(map[uint64]*consulConsumer),
		pending:   make(map[uint64]struct{}),
		failures:  make(chan consulConsumerFailure, 1),
	}
}

func (m *consulConsumerManager) run() error {
	defer m.stopAll()
	if err := m.reconcile(); err != nil {
		return fmt.Errorf("discover Active Profiling DataIDs from Consul: %w", err)
	}

	discoveryTicker := time.NewTicker(activeProfilingConsulRefreshInterval)
	defer discoveryTicker.Stop()
	configRetryTicker := time.NewTicker(activeProfilingConfigRetryInterval)
	defer configRetryTicker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return nil
		case failure := <-m.failures:
			if m.ctx.Err() != nil {
				return nil
			}
			m.handleFailure(failure)
		case <-discoveryTicker.C:
			if err := m.reconcile(); err != nil {
				logger.Errorf("Active Profiling Consul discovery failed; will retry: %s", err)
			}
		case <-configRetryTicker.C:
			m.retryPending()
		}
	}
}

func (m *consulConsumerManager) reconcile() error {
	dataIDs, err := m.processor.metadataCenter.ListDataIDs()
	if err != nil {
		return err
	}

	present := make(map[uint64]struct{}, len(dataIDs))
	for _, dataID := range dataIDs {
		present[dataID] = struct{}{}
	}
	for dataID := range m.consumers {
		if _, exists := present[dataID]; exists {
			continue
		}
		m.stop(dataID)
		logger.Infof("Active Profiling DataID removed from Consul; consumer stopped dataid=%d", dataID)
	}
	for dataID := range m.pending {
		if _, exists := present[dataID]; !exists {
			delete(m.pending, dataID)
		}
	}
	for _, dataID := range dataIDs {
		m.loadConfig(dataID)
	}
	logger.Debugf("Active Profiling Consul discovery complete consumers=%d pending=%d", len(m.consumers), len(m.pending))
	return nil
}

func (m *consulConsumerManager) loadConfig(dataID uint64) {
	info, err := m.processor.metadataCenter.GetDataIDInfo(m.ctx, dataID)
	if err != nil {
		logger.Errorf("load Active Profiling DataID %d config from Consul failed: %s", dataID, err)
		m.pending[dataID] = struct{}{}
		return
	}
	delete(m.pending, dataID)
	m.startOrUpdate(dataID, info)
}

func (m *consulConsumerManager) startOrUpdate(dataID uint64, info core.DataIDInfo) {
	if current, exists := m.consumers[dataID]; exists {
		if current.info == info {
			return
		}
		m.stop(dataID)
		logger.Infof("Active Profiling DataID config changed; consumer restarting dataid=%d", dataID)
	}

	consumerCtx, cancel := context.WithCancel(m.ctx)
	consumer := &consulConsumer{
		info:   info,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	m.consumers[dataID] = consumer
	go func() {
		defer close(consumer.done)
		if err := m.processor.startDataIDConsumer(consumerCtx, dataID, info); err != nil && !errors.Is(err, context.Canceled) {
			select {
			case m.failures <- consulConsumerFailure{dataID: dataID, consumer: consumer, err: err}:
			case <-consumerCtx.Done():
			}
		}
	}()
	logger.Infof("Active Profiling consumer started from Consul discovery dataid=%d", dataID)
}

func (m *consulConsumerManager) handleFailure(failure consulConsumerFailure) {
	current, exists := m.consumers[failure.dataID]
	if !exists || current != failure.consumer {
		return
	}
	m.stop(failure.dataID)
	m.pending[failure.dataID] = struct{}{}
	logger.Errorf("Active Profiling DataID consumer failed; retrying this DataID without stopping siblings dataid=%d err=%s",
		failure.dataID, failure.err)
}

func (m *consulConsumerManager) retryPending() {
	for dataID := range m.pending {
		m.loadConfig(dataID)
	}
}

func (m *consulConsumerManager) stop(dataID uint64) {
	consumer, exists := m.consumers[dataID]
	if !exists {
		return
	}
	consumer.cancel()
	<-consumer.done
	delete(m.consumers, dataID)
}

func (m *consulConsumerManager) stopAll() {
	for dataID := range m.consumers {
		m.stop(dataID)
	}
}
