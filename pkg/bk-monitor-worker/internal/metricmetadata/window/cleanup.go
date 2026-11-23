// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package window

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/storage"
)

var activeProcessors = struct {
	sync.Mutex
	items map[*Processor]struct{}
}{items: make(map[*Processor]struct{})}

var purgeMu sync.Mutex

// RegisterProcessor makes this processor visible to coordinated metric cleanup.
func RegisterProcessor(p *Processor) {
	activeProcessors.Lock()
	activeProcessors.items[p] = struct{}{}
	activeProcessors.Unlock()
}

// UnregisterProcessor removes a processor after its consumer and tasks have stopped.
func UnregisterProcessor(p *Processor) {
	activeProcessors.Lock()
	delete(activeProcessors.items, p)
	activeProcessors.Unlock()
}

// PurgeDeletedMetrics 清理删除的指标引用，并同步失效本进程中的相关缓存。
func PurgeDeletedMetrics(ctx context.Context, db *sql.DB, groupID uint, fields []string) error {
	purgeMu.Lock()
	defer purgeMu.Unlock()
	activeProcessors.Lock()
	defer activeProcessors.Unlock()
	processors := make([]*Processor, 0, len(activeProcessors.items))
	for p := range activeProcessors.items {
		processors = append(processors, p)
	}
	// 持有注册表锁直到清理和缓存失效完成，避免新 processor 在快照之后启动消费。
	for _, p := range processors {
		p.mu.Lock()
	}
	defer func() {
		for i := len(processors) - 1; i >= 0; i-- {
			processors[i].mu.Unlock()
		}
	}()
	options := core.DefaultOptions()
	if err := storage.NewMySQLStore(db, 30*time.Second, 0, 0, options.TouchInterval, options.LoadPageSize).PurgeMetricReferences(ctx, int(groupID), fields); err != nil {
		return err
	}
	for _, p := range processors {
		p.purgeMetricLocked(groupID, fields)
	}
	return nil
}
