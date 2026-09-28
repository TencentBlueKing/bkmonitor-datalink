// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var topologyAdmission struct {
	sync.Mutex
	active   int
	reserved int64
}

type topologyAdmissionKey struct{}

type topologyLease struct {
	base, reserved int64
	stages         map[string]int64
	released       bool
	yolo           bool
}

// AcquireSharedTopology reserves process-wide capacity without queueing.
// The HTTP lease lasts through response writing. Nested calls reuse it.
func AcquireSharedTopology(ctx context.Context) (admittedCtx context.Context, releaseFn func(), err error) {
	_, span := trace.NewSpan(ctx, "timegraph-admission")
	defer finishTimeGraphStage(ctx, span, "admission", time.Now(), &err)
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if admitted, _ := ctx.Value(topologyAdmissionKey{}).(*topologyLease); admitted != nil {
		topologyAdmission.Lock()
		released := admitted.released
		topologyAdmission.Unlock()
		if released {
			return ctx, nil, context.Canceled
		}
		span.Set("admission-reused", true)
		return ctx, func() {}, nil
	}
	topologyAdmission.Lock()
	defer topologyAdmission.Unlock()
	defer func() {
		metric.CMDBTopologyAdmissionSet(topologyAdmission.active)
		metric.CMDBTopologyReservedSet(topologyAdmission.reserved)
		span.Set("admission-active", topologyAdmission.active)
		span.Set("admission-reserved-bytes", topologyAdmission.reserved)
	}()
	lease := &topologyLease{stages: make(map[string]int64), yolo: yoloMode}
	if !lease.yolo {
		limit := effectiveTimeGraphLimit(MaxSharedTopologyConcurrent, 8)
		if topologyAdmission.active >= limit {
			return ctx, nil, &ResultLimitError{Reason: "max_topology_concurrent_requests", Count: topologyAdmission.active + 1, Limit: limit}
		}
		lease.base = int64(effectiveTimeGraphLimit(SharedTopologyRequestMemoryBytes, 512*1024*1024))
		if err := lease.reserve(lease.base, span); err != nil {
			return ctx, nil, err
		}
	}
	topologyAdmission.active++
	var once sync.Once
	release := func() {
		once.Do(func() {
			_, releaseSpan := trace.NewSpan(ctx, "timegraph-release-admission")
			var releaseErr error
			defer finishTimeGraphStage(ctx, releaseSpan, "admission-release", time.Now(), &releaseErr)
			topologyAdmission.Lock()
			defer topologyAdmission.Unlock()
			topologyAdmission.active--
			topologyAdmission.reserved -= lease.reserved
			lease.released = true
			releaseSpan.Set("admission-active", topologyAdmission.active)
			metric.CMDBTopologyAdmissionSet(topologyAdmission.active)
			metric.CMDBTopologyReservedSet(topologyAdmission.reserved)
		})
	}
	return context.WithValue(ctx, topologyAdmissionKey{}, lease), release, nil
}

// reserve requires the admission lock. Reservations are estimates, not a hard
// RSS guarantee; current cgroup/host headroom is an additional rejection gate.
func (lease *topologyLease) reserve(want int64, span *trace.Span) error {
	delta := want - lease.reserved
	if delta > 0 {
		limit := int64(effectiveTimeGraphLimit(MaxSharedTopologyReservedBytes, 4*1024*1024*1024))
		if delta > limit-topologyAdmission.reserved {
			return &ResultLimitError{Reason: "max_topology_reserved_bytes", Count: int(topologyAdmission.reserved + delta), Limit: int(limit)}
		}
		available, known := topologyMemoryHeadroom()
		span.Set("memory-headroom-known", known)
		span.Set("memory-headroom-bytes", available)
		if known {
			margin := int64(effectiveTimeGraphLimit(SharedTopologyMemoryHeadroom, 512*1024*1024))
			usable := max(int64(0), available-margin)
			// Deliberately conservative: live usage may include existing leases.
			if delta > usable-topologyAdmission.reserved {
				return &ResultLimitError{Reason: "max_topology_memory_headroom", Count: int(topologyAdmission.reserved + delta), Limit: int(usable)}
			}
		}
	}
	topologyAdmission.reserved += delta
	lease.reserved = want
	return nil
}

// ReserveTopologyStage updates an estimated simultaneously-live stage before
// allocation. Zero releases that stage, including on a canceled context.
func ReserveTopologyStage(ctx context.Context, stage string, bytes int64) (err error) {
	lease, _ := ctx.Value(topologyAdmissionKey{}).(*topologyLease)
	if lease == nil || lease.yolo {
		return nil
	}
	if bytes < 0 {
		return fmt.Errorf("negative or overflowing topology memory estimate for %s", stage)
	}
	if bytes > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	_, span := trace.NewSpan(ctx, "timegraph-reserve-memory")
	defer span.End(&err)
	topologyAdmission.Lock()
	defer topologyAdmission.Unlock()
	if lease.released {
		return context.Canceled
	}
	want := bytes
	for name, size := range lease.stages {
		if name != stage {
			if size > math.MaxInt64-want {
				return &ResultLimitError{Reason: "max_topology_reserved_bytes", Count: math.MaxInt, Limit: effectiveTimeGraphLimit(MaxSharedTopologyReservedBytes, 4*1024*1024*1024)}
			}
			want += size
		}
	}
	if err := lease.reserve(max(lease.base, want), span); err != nil {
		return err
	}
	lease.stages[stage] = bytes
	span.Set("memory-stage", stage)
	span.Set("stage-estimated-bytes", bytes)
	span.Set("request-reserved-bytes", lease.reserved)
	metric.CMDBTopologyReservedSet(topologyAdmission.reserved)
	return nil
}

// TopologyOutputByteLimit 同时用于单查询物化预算与 HTTP 批次响应预算。
func TopologyOutputByteLimit() int {
	return effectiveTimeGraphLimit(MaxSharedTopologyOutputBytes, 64*1024*1024)
}

type topologyOutputBudget struct {
	ctx           context.Context
	reservedBytes int64
	elements      int
	bytes         int64
	maxElements   int
	maxBytes      int64
}

func newTopologyOutputBudget(grid TopologyGrid, partial map[int64]string) *topologyOutputBudget {
	maxElements := effectiveTimeGraphLimit(MaxSharedTopologyOutputElements, 200000)
	b := &topologyOutputBudget{maxElements: maxElements, maxBytes: int64(TopologyOutputByteLimit()), bytes: 512}
	for _, ts := range grid.Timestamps {
		b.bytes += 128 + jsonStringByteBound(partial[ts])
	}
	return b
}

// JSON 最坏情况下每个输入字节转为六字节转义；使用保守上界在分配快照前
// 拒绝，不先构造完整 JSON 再检查。节点/边常量覆盖字段名、ID、标点与逗号。
func jsonStringByteBound(value string) int64 { return 2 + 6*int64(len(value)) }

func topologyNodeByteBound(resource cmdb.Resource, info cmdb.Matcher) int64 {
	size := int64(64) + jsonStringByteBound(string(resource))
	for key, value := range info {
		size += jsonStringByteBound(key) + jsonStringByteBound(value) + 2
	}
	return size
}

func topologyEdgeByteBound(edge timeGraphTopologyEdgeKey) int64 {
	return 160 + jsonStringByteBound(edge.relation.relationType) + jsonStringByteBound(edge.relation.metricName) + jsonStringByteBound(edge.relation.category) + jsonStringByteBound(edge.relation.direction)
}

func (b *topologyOutputBudget) checkBytes(size int64) error {
	if b.maxBytes <= 0 {
		return nil
	}
	if size > b.maxBytes-b.bytes {
		return &ResultLimitError{Reason: "max_topology_output_bytes", Count: int(b.bytes + size), Limit: int(b.maxBytes)}
	}
	return nil
}

func (b *topologyOutputBudget) add(size int64) error {
	return b.addRepeated(size, 1)
}

func (b *topologyOutputBudget) addRepeated(size int64, count int) error {
	if b.maxElements > 0 && count > b.maxElements-b.elements {
		return &ResultLimitError{Reason: "max_topology_output_elements", Count: b.elements + count, Limit: b.maxElements}
	}
	if err := b.checkBytes(size * int64(count)); err != nil {
		return err
	}
	if b.ctx != nil && 2*(b.bytes+size*int64(count)) > b.reservedBytes {
		const chunk = 4 * 1024 * 1024
		want := (2*(b.bytes+size*int64(count)) + chunk - 1) / chunk * chunk
		if err := ReserveTopologyStage(b.ctx, "output", want); err != nil {
			return err
		}
		b.reservedBytes = want
	}
	b.elements += count
	b.bytes += size * int64(count)
	return nil
}
