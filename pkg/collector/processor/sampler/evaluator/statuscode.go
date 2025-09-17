// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluator

import (
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/batchspliter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/sampler/queue"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/fasttime"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var statusMap = map[string]string{
	"STATUS_CODE_UNSET": "UNSET",
	"STATUS_CODE_OK":    "OK",
	"STATUS_CODE_ERROR": "ERROR",
}

// statusCodeEvaluator 状态码采样
type statusCodeEvaluator struct {
	mut         sync.RWMutex
	traces      map[pcommon.TraceID]int64
	status      map[string]struct{} // 无须锁保护
	policy      queue.Policy
	q           *queue.Queue
	maxDuration time.Duration
	stop        chan struct{}
	gcInterval  time.Duration
}

func newStatusCodeEvaluator(config Config) *statusCodeEvaluator {
	status := make(map[string]struct{})
	for _, s := range config.StatusCode {
		status[s] = struct{}{}
	}

	maxSpans := config.MaxSpan
	if maxSpans <= 0 {
		maxSpans = 100
	}

	eval := &statusCodeEvaluator{
		policy:      queue.Policy(config.StoragePolicy),
		traces:      make(map[pcommon.TraceID]int64),
		q:           queue.New(config.StoragePolicy, maxSpans),
		status:      status,
		maxDuration: config.MaxDuration,
		stop:        make(chan struct{}),
	}
	go eval.gc()
	return eval
}

func (e *statusCodeEvaluator) Evaluate(record *define.Record) error {
	switch record.RecordType {
	case define.RecordTraces:
		e.processTraces(record)
	}
	return nil
}

func (e *statusCodeEvaluator) Type() string {
	return evaluatorTypeStatusCode
}

func (e *statusCodeEvaluator) Stop() {
	close(e.stop)
	e.q.Clean()
}

func (e *statusCodeEvaluator) processTraces(record *define.Record) {
	dataID := record.Token.TracesDataId
	pdTraces := record.Data.(ptrace.Traces)

	// 先遍历一遍确定哪些 span 需要采样 并记录 traceID
	allTraceIDs := make(map[pcommon.TraceID]struct{})
	foreach.Spans(pdTraces, func(span ptrace.Span) {
		allTraceIDs[span.TraceID()] = struct{}{}
		code := span.Status().Code().String()
		if _, ok := e.status[statusMap[code]]; ok {
			e.mut.Lock()
			e.traces[span.TraceID()] = fasttime.UnixTimestamp()
			e.mut.Unlock()
		}
	})

	// 如果 Full 的话需要将 traces 按 span 切分
	var batch []ptrace.Traces
	if e.policy.IsFull() {
		batch = batchspliter.SplitEachSpans(pdTraces)
	}

	// 移除无须采样的 span 并记录需要缓存的 spanID
	holdSpanIDs := make(map[pcommon.SpanID]struct{})
	foreach.SpansRemoveIf(pdTraces, func(span ptrace.Span) bool {
		e.mut.RLock()
		_, ok := e.traces[span.TraceID()]
		e.mut.RUnlock()
		if !ok {
			holdSpanIDs[span.SpanID()] = struct{}{}
		}
		return !ok
	})

	// batch 为空无需处理
	if len(batch) == 0 {
		return
	}

	for i := 0; i < len(batch); i++ {
		t := batch[i]
		_, spanID, ok := queue.IdFromTraces(t)
		if !ok {
			continue
		}
		_, ok = holdSpanIDs[spanID] // 已采样的不需要缓存在本地
		if !ok {
			continue
		}
		if err := e.q.Put(dataID, t); err != nil {
			logger.Warnf("queue failed to put traces, dataID=%v, err: %v", dataID, err)
		}
	}

	// 遍历本次上报所有 traces
	for traceID := range allTraceIDs {
		e.mut.RLock()
		_, ok := e.traces[traceID] // 不存在错误的 traces 跳过
		e.mut.RUnlock()
		if !ok {
			continue
		}

		// pop 如果已经弹出过数据 则后续快速返回
		// 因为一旦弹出意味着 e.traces 已经被记录
		popItems := e.q.Pop(dataID, traceID)
		for i := 0; i < len(popItems); i++ {
			traces := popItems[i]
			rs := pdTraces.ResourceSpans().At(0).ScopeSpans().AppendEmpty()
			traces.ResourceSpans().At(0).ScopeSpans().At(0).CopyTo(rs)
		}
	}
}

func (e *statusCodeEvaluator) gc() {
	d := e.gcInterval
	if d <= 0 {
		d = time.Minute
	}
	ticker := time.NewTicker(d)
	defer ticker.Stop()

	maxDuration := int64(e.maxDuration.Seconds())
	for {
		select {
		case <-e.stop:
			return

		case <-ticker.C:
			now := time.Now().Unix()
			e.mut.Lock()
			for traceID, ts := range e.traces {
				drop := now-ts > maxDuration
				logger.Debugf("traceID=%v, now=%v, ts=%v, drop=%v", traceID, now, ts, drop)
				if drop {
					delete(e.traces, traceID)
				}
			}
			e.mut.Unlock()
		}
	}
}
