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
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Connection describes one DataID to consume in standalone mode.
type Connection struct {
	DataID uint64 `yaml:"dataId"`
}

// ConnectionList is the standalone Active Profiling task configuration.
type ConnectionList struct {
	Connections []Connection `yaml:"connections"`
}

const (
	// Follow the existing APM pre-calculation refresh and readiness retry cadence.
	activeProfilingConsulRefreshInterval = 10 * time.Minute
	activeProfilingConfigRetryInterval   = 5 * time.Second
)

// ReadConnectionList reads and validates the standalone task configuration.
func ReadConnectionList(filePath string) (ConnectionList, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ConnectionList{}, fmt.Errorf("read Active Profiling config %q: %w", filePath, err)
	}

	var list ConnectionList
	if err := yaml.Unmarshal(data, &list); err != nil {
		return ConnectionList{}, fmt.Errorf("parse Active Profiling config %q: %w", filePath, err)
	}
	if len(list.Connections) == 0 {
		return ConnectionList{}, fmt.Errorf("Active Profiling config %q has no connections", filePath)
	}

	dataIDs := make(map[uint64]struct{}, len(list.Connections))
	for _, connection := range list.Connections {
		if connection.DataID == 0 {
			return ConnectionList{}, fmt.Errorf("Active Profiling config %q contains an empty dataId", filePath)
		}
		if _, exists := dataIDs[connection.DataID]; exists {
			return ConnectionList{}, fmt.Errorf("Active Profiling config %q contains duplicate dataId %d", filePath, connection.DataID)
		}
		dataIDs[connection.DataID] = struct{}{}
	}
	return list, nil
}

// RunStandalone starts tasks from a custom file or the BMW Consul DataID prefix.
func (p *activeProfiling) RunStandalone(filePath string) error {
	var dataIDs []uint64
	if filePath != "" {
		connections, err := ReadConnectionList(filePath)
		if err != nil {
			return err
		}
		dataIDs = make([]uint64, 0, len(connections.Connections))
		for _, connection := range connections.Connections {
			dataIDs = append(dataIDs, connection.DataID)
		}
	}

	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	go p.Run(ctx)
	if filePath == "" {
		return p.runConsulConsumers(ctx)
	}
	return p.runConsumers(ctx, dataIDs)
}

func (p *activeProfiling) runConsulConsumers(ctx context.Context) error {
	return newConsulConsumerManager(p, ctx).run()
}

func (p *activeProfiling) runConsumers(ctx context.Context, dataIDs []uint64) error {
	consumerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for _, dataID := range dataIDs {
		wg.Add(1)
		go func(dataID uint64) {
			defer wg.Done()
			for {
				err := p.StartByDataID(consumerCtx, dataID)
				if ctx.Err() != nil {
					return
				}
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Errorf("Active Profiling DataID consumer failed; retrying this DataID dataid=%d err=%s", dataID, err)
				}
				if err := waitContext(consumerCtx, activeProfilingConfigRetryInterval); err != nil {
					return
				}
			}
		}(dataID)
	}
	logger.Infof("Active Profiling consumers started count=%d", len(dataIDs))
	<-ctx.Done()
	cancel()
	wg.Wait()
	logger.Infof("Active Profiling consumers stopped")
	return nil
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
