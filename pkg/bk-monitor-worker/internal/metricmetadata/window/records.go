// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

// Package window contains the mutable state used by a metric metadata batch window.
package window

import (
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
)

// PendingRecords combines repeated records while preserving storage and consumption order.
// Its zero value is ready to use.
type PendingRecords struct {
	records []core.Record
	index   map[core.MetadataKey]int
	order   []uint64
	next    uint64
}

// Len returns the number of unique pending records.
func (p *PendingRecords) Len() int { return len(p.records) }

// Append merges records by metadata key and returns the number rejected as stale.
func (p *PendingRecords) Append(records []core.Record) int {
	if len(records) == 0 {
		return 0
	}
	if p.index == nil {
		p.index = make(map[core.MetadataKey]int, len(records))
	}
	staleRecords := 0
	// 同一窗口内优先保留较新的事件时间；无法比较时按消费顺序保留最后一条。
	for _, record := range records {
		key := record.Key()
		order := p.next
		p.next++
		if index, ok := p.index[key]; ok {
			current := p.records[index]
			if record.HasTimestamp && current.HasTimestamp && record.TimestampMS < current.TimestampMS {
				staleRecords++
				continue
			}
			p.records[index] = record
			p.order[index] = order
			continue
		}
		p.index[key] = len(p.records)
		p.records = append(p.records, record)
		p.order = append(p.order, order)
	}
	return staleRecords
}

// Replace resets the pending window and adds records in their supplied order.
func (p *PendingRecords) Replace(records []core.Record) {
	p.Reset()
	p.Append(records)
}

// Reset clears all records and ordering state.
func (p *PendingRecords) Reset() {
	p.records = nil
	p.index = nil
	p.order = nil
	p.next = 0
}

// Records returns pending records in first-seen key order.
// Callers must treat the returned slice as read-only.
func (p *PendingRecords) Records() []core.Record {
	return p.records
}

// Ordered returns records in the order of their latest accepted update.
func (p *PendingRecords) Ordered() []core.Record {
	indexes := make([]int, len(p.records))
	for i := range indexes {
		indexes[i] = i
	}
	sort.Slice(indexes, func(i, j int) bool {
		return p.order[indexes[i]] < p.order[indexes[j]]
	})
	records := make([]core.Record, len(indexes))
	for i, index := range indexes {
		records[i] = p.records[index]
	}
	return records
}
