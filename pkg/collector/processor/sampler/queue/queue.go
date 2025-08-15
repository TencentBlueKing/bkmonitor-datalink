// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package queue

import (
	"sync"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/sampler/tracestore"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Policy 代表 traces 的存储策略
type Policy string

func (p Policy) IsFull() bool {
	return p == PolicyFull
}

const (
	// PolicyFull 表示存储所有内容 traces 的任何数据
	PolicyFull Policy = "full"

	// PolicyPost 后采样 即只存储 spanID/traceID
	PolicyPost Policy = "post"
)

type traceIDMap struct {
	size int
	mut  sync.Mutex
	m    map[pcommon.TraceID]chan pcommon.SpanID
}

func newTraceIDMap(size int) *traceIDMap {
	return &traceIDMap{
		size: size,
		m:    make(map[pcommon.TraceID]chan pcommon.SpanID),
	}
}

func (tm *traceIDMap) Set(traceID pcommon.TraceID, spanID pcommon.SpanID) {
	tm.mut.Lock()
	defer tm.mut.Unlock()

	ch, ok := tm.m[traceID]
	if !ok {
		ch = make(chan pcommon.SpanID, tm.size)
		tm.m[traceID] = ch
	}

	// TODO(mando): 丢弃旧数据还是新数据？
	select {
	case ch <- spanID:
	default:
	}
}

func (tm *traceIDMap) Pop(traceID pcommon.TraceID) []pcommon.SpanID {
	tm.mut.Lock()
	defer tm.mut.Unlock()

	ch, ok := tm.m[traceID]
	if !ok {
		return nil
	}
	l := len(ch)
	spanIDs := make([]pcommon.SpanID, 0, l)

	for i := 0; i < l; i++ {
		spanIDs = append(spanIDs, <-ch)
	}
	close(ch)
	delete(tm.m, traceID)

	return spanIDs
}

func IdFromTraces(traces ptrace.Traces) (pcommon.TraceID, pcommon.SpanID, bool) {
	var trace pcommon.TraceID
	var spanID pcommon.SpanID

	resourceSpans := traces.ResourceSpans()
	if resourceSpans.Len() <= 0 {
		return trace, spanID, false
	}
	scopeSpans := resourceSpans.At(0).ScopeSpans()
	if scopeSpans.Len() <= 0 {
		return trace, spanID, false
	}
	spans := scopeSpans.At(0).Spans()
	if spans.Len() <= 0 {
		return trace, spanID, false
	}

	span := spans.At(0)
	return span.TraceID(), span.SpanID(), true
}

type Queue struct {
	policy     Policy
	maxSpans   int
	mut        sync.RWMutex
	storages   map[int32]*tracestore.Storage
	span2trace map[int32]*traceIDMap
}

func New(policy string, maxSpans int) *Queue {
	return &Queue{
		policy:     Policy(policy),
		maxSpans:   maxSpans,
		storages:   map[int32]*tracestore.Storage{},
		span2trace: map[int32]*traceIDMap{},
	}
}

func (q *Queue) Clean() {
	if !q.policy.IsFull() {
		return
	}

	q.mut.Lock()
	defer q.mut.Unlock()

	for _, storage := range q.storages {
		storage.Clean()
	}
}

func (q *Queue) Put(dataID int32, traces ptrace.Traces) error {
	if !q.policy.IsFull() {
		return nil
	}

	traceID, spanID, ok := IdFromTraces(traces)
	if !ok {
		logger.Debugf("extract traceID/spanID from trace failed, dataID=%d", dataID)
		return nil
	}
	logger.Debugf("queue put action: dataID=%v, traceID=%v, spanID=%v", dataID, traceID.HexString(), spanID.HexString())

	tk := tracestore.TraceKey{
		TraceID: traceID,
		SpanID:  spanID,
	}

	// storages / span2trace 实例应该同时被创建
	q.mut.RLock()
	storage, ok := q.storages[dataID]
	idMap := q.span2trace[dataID]
	q.mut.RUnlock()

	if ok {
		idMap.Set(traceID, spanID)
		storage.Set(tk, traces)
		return nil
	}

	q.mut.Lock()
	defer q.mut.Unlock()

	// 先尝试读 避免并发创建
	storage, ok = q.storages[dataID]
	idMap = q.span2trace[dataID]
	if ok {
		idMap.Set(traceID, spanID)
		storage.Set(tk, traces)
		return nil
	}

	// 读取失败
	// 创建 idMap
	idMap = newTraceIDMap(q.maxSpans)
	q.span2trace[dataID] = idMap
	idMap.Set(traceID, spanID)

	// 创建 storage
	storage = tracestore.New()
	q.storages[dataID] = storage
	storage.Set(tk, traces)
	return nil
}

func (q *Queue) Pop(dataID int32, traceID pcommon.TraceID) []ptrace.Traces {
	if !q.policy.IsFull() {
		return nil
	}

	// storages / span2trace 实例应该同时存在
	q.mut.RLock()
	idMap := q.span2trace[dataID]
	storage := q.storages[dataID]
	q.mut.RUnlock()

	if idMap == nil {
		return nil
	}

	spanIDs := idMap.Pop(traceID)
	logger.Debugf("queue pop action: count=%d, dataID=%v, traceID=%v", len(spanIDs), dataID, traceID.HexString())
	if len(spanIDs) == 0 {
		return nil
	}

	result := make([]ptrace.Traces, 0, len(spanIDs))
	for i := 0; i < len(spanIDs); i++ {
		tk := tracestore.TraceKey{TraceID: traceID, SpanID: spanIDs[i]}
		traces, ok := storage.Get(tk)
		if !ok {
			logger.Errorf("failed to get tk=%v, dataid=%d", tk, dataID)
			continue
		}
		storage.Del(tk)
		result = append(result, traces)
	}
	return result
}
