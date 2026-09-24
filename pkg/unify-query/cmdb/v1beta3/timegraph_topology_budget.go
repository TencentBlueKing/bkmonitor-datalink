// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var topologyAdmission struct {
	sync.Mutex
	active int
}

type topologyAdmissionKey struct{}

func positiveTopologyLimit(value, fallback int) int {
	if yoloMode {
		return 0
	}
	if value > 0 {
		return value
	}
	return fallback
}

// AcquireSharedTopology 仅跟踪活跃拓扑请求，不限制并发，也不排队。
// HTTP 层计数持续到响应写完；嵌套模型调用复用计数，直接模型调用单独计数。
func AcquireSharedTopology(ctx context.Context) (admittedCtx context.Context, releaseFn func(), err error) {
	_, span := trace.NewSpan(ctx, "timegraph-admission")
	defer finishTimeGraphStage(ctx, span, "admission", time.Now(), &err)
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if admitted, _ := ctx.Value(topologyAdmissionKey{}).(bool); admitted {
		span.Set("admission-reused", true)
		return ctx, func() {}, nil
	}
	topologyAdmission.Lock()
	defer topologyAdmission.Unlock()
	defer func() {
		metric.CMDBTopologyAdmissionSet(topologyAdmission.active)
		span.Set("admission-active", topologyAdmission.active)
	}()
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
			releaseSpan.Set("admission-active", topologyAdmission.active)
			metric.CMDBTopologyAdmissionSet(topologyAdmission.active)
		})
	}
	return context.WithValue(ctx, topologyAdmissionKey{}, true), release, nil
}

// TopologyOutputByteLimit 同时用于单查询物化预算与 HTTP 批次响应预算。
func TopologyOutputByteLimit() int {
	if yoloMode {
		return int(^uint(0) >> 1)
	}
	return positiveTopologyLimit(MaxSharedTopologyOutputBytes, 64*1024*1024)
}

type topologyOutputBudget struct {
	elements    int
	bytes       int64
	maxElements int
	maxBytes    int64
}

func newTopologyOutputBudget(grid TopologyGrid, partial map[int64]string) *topologyOutputBudget {
	maxElements := positiveTopologyLimit(MaxSharedTopologyOutputElements, 200000)
	if yoloMode {
		maxElements = int(^uint(0) >> 1)
	}
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
	if b.maxElements > 0 && b.elements >= b.maxElements {
		return &ResultLimitError{Reason: "max_topology_output_elements", Count: b.elements + 1, Limit: b.maxElements}
	}
	if err := b.checkBytes(size); err != nil {
		return err
	}
	b.elements++
	b.bytes += size
	return nil
}
