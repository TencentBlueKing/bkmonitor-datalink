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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
)

func TestStatusCodeEvaluatorPost(t *testing.T) {
	evaluator := newStatusCodeEvaluator(Config{
		MaxDuration: time.Second,
		StatusCode:  []string{"ERROR"},
	})
	defer evaluator.Stop()

	t1 := random.TraceID()
	t2 := random.TraceID()

	// round1
	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	span1 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span1.SetTraceID(t1)
	span1.Status().SetCode(ptrace.StatusCodeError) // 采样

	span2 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span2.SetTraceID(t2)
	span2.Status().SetCode(ptrace.StatusCodeOk) // 未采样（缓存 tracesID）

	_ = evaluator.Evaluate(&define.Record{
		RecordType: define.RecordTraces,
		Data:       traces,
	})

	assert.Equal(t, 1, traces.SpanCount())
	span3 := testkits.FirstSpan(traces)
	assert.Equal(t, t1, span3.TraceID())
	_, ok := evaluator.traces[span3.TraceID()]
	assert.True(t, ok)

	// round2
	traces = ptrace.NewTraces()
	rs = traces.ResourceSpans().AppendEmpty()

	span4 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span4.SetTraceID(t1)
	span4.Status().SetCode(ptrace.StatusCodeOk) // 采样（已经出现过错误）

	span5 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span5.SetTraceID(t2)
	span5.Status().SetCode(ptrace.StatusCodeOk) // 未采样（缓存 tracesID）
	_ = evaluator.Evaluate(&define.Record{
		RecordType: define.RecordTraces,
		Data:       traces,
	})

	assert.Equal(t, 1, traces.SpanCount())
	span6 := testkits.FirstSpan(traces)
	assert.Equal(t, t1, span6.TraceID())
	_, ok = evaluator.traces[span6.TraceID()]
	assert.True(t, ok)

	// round3
	evaluator.traces = make(map[pcommon.TraceID]int64) // gc
	traces = ptrace.NewTraces()
	rs = traces.ResourceSpans().AppendEmpty()

	span7 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span7.SetTraceID(t1)
	span7.Status().SetCode(ptrace.StatusCodeOk) // 未采样（缓存 tracesID）

	span8 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span8.SetTraceID(t2)
	span8.Status().SetCode(ptrace.StatusCodeOk) // 未采样（缓存 tracesID）
	_ = evaluator.Evaluate(&define.Record{
		RecordType: define.RecordTraces,
		Data:       traces,
	})

	assert.Equal(t, 0, traces.SpanCount()) // drop t2
	assert.Len(t, evaluator.traces, 0)
}

func TestStatusCodeEvaluatorFull(t *testing.T) {
	evaluator := newStatusCodeEvaluator(Config{
		MaxDuration:   time.Second * 10,
		MaxSpan:       10,
		StoragePolicy: "full",
		StatusCode:    []string{"ERROR"},
	})
	defer evaluator.Stop()

	t1 := random.TraceID()
	t2 := random.TraceID()

	// round1
	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	span1 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span1.SetTraceID(t1)
	span1.SetSpanID(random.SpanID())
	span1.Status().SetCode(ptrace.StatusCodeError)

	span2 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span2.SetTraceID(t2)
	span2.SetSpanID(random.SpanID())
	span2.Status().SetCode(ptrace.StatusCodeOk) // t2 会被缓存

	_ = evaluator.Evaluate(&define.Record{
		RecordType: define.RecordTraces,
		Data:       traces,
	})

	assert.Equal(t, 1, traces.SpanCount())
	span3 := testkits.FirstSpan(traces)
	assert.Equal(t, t1, span3.TraceID())
	_, ok := evaluator.traces[span3.TraceID()]
	assert.True(t, ok)

	// round2
	traces = ptrace.NewTraces()
	rs = traces.ResourceSpans().AppendEmpty()

	span4 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span4.SetTraceID(t1)
	span4.SetSpanID(random.SpanID())
	span4.Status().SetCode(ptrace.StatusCodeOk) // keep（已经出现过错误）

	span5 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span5.SetTraceID(t2)
	span5.SetSpanID(random.SpanID())
	span5.Status().SetCode(ptrace.StatusCodeError) // 读取 t2 缓存并上报
	_ = evaluator.Evaluate(&define.Record{
		RecordType: define.RecordTraces,
		Data:       traces,
	})

	assert.Equal(t, 3, traces.SpanCount())
	span6 := testkits.FirstSpan(traces)
	assert.Equal(t, t1, span6.TraceID())
	_, ok = evaluator.traces[span6.TraceID()]
	assert.True(t, ok)

	// round3
	traces = ptrace.NewTraces()
	rs = traces.ResourceSpans().AppendEmpty()

	span7 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span7.SetTraceID(t1)
	span7.Status().SetCode(ptrace.StatusCodeOk) // keep

	span8 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span8.SetTraceID(t2)
	span8.Status().SetCode(ptrace.StatusCodeOk) // keep
	_ = evaluator.Evaluate(&define.Record{
		RecordType: define.RecordTraces,
		Data:       traces,
	})

	assert.Equal(t, 2, traces.SpanCount())
	assert.Len(t, evaluator.traces, 2)
}
