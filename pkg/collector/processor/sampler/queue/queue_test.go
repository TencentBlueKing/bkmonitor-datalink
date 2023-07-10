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
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

func TestIdFromTraces(t *testing.T) {
	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 1,
	})

	t.Run("Success", func(t *testing.T) {
		traces := g.Generate()
		traceID, spanID, ok := IdFromTraces(traces)
		assert.True(t, ok)
		t.Logf("traceID=%v, spanID=%v", traceID.HexString(), spanID.HexString())
	})

	t.Run("NoSpans", func(t *testing.T) {
		traces := ptrace.NewTraces()
		traceID, spanID, ok := IdFromTraces(traces)
		assert.False(t, ok)
		assert.Equal(t, "", traceID.HexString())
		assert.Equal(t, "", spanID.HexString())
	})
}

func TestTraceIDMap(t *testing.T) {
	t.Run("MaxSpan=0", func(t *testing.T) {
		idMap := newTraceIDMap(0)

		traceID := pcommon.NewTraceID([16]byte{1, 2, 3, 4})
		spanID := pcommon.NewSpanID([8]byte{1, 2})
		idMap.Set(traceID, spanID)
		dst := idMap.Pop(traceID)
		assert.Len(t, dst, 0)
		assert.Len(t, idMap.Pop(random.TraceID()), 0)
	})

	t.Run("MaxSpan=1", func(t *testing.T) {
		idMap := newTraceIDMap(1)

		traceID := pcommon.NewTraceID([16]byte{1, 2, 3, 4})
		spanID := pcommon.NewSpanID([8]byte{1, 2})
		idMap.Set(traceID, spanID)
		dst := idMap.Pop(traceID)
		assert.Len(t, dst, 1)
		assert.Equal(t, spanID, dst[0])
		assert.Len(t, idMap.m, 0)
		assert.Len(t, idMap.Pop(random.TraceID()), 0)
	})

	t.Run("Concurrency", func(t *testing.T) {
		idMap := newTraceIDMap(10)

		wg := sync.WaitGroup{}
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					traceID := pcommon.NewTraceID([16]byte{1, 2, 3, byte(j)})
					spanID := pcommon.NewSpanID([8]byte{1, byte(j)})
					idMap.Set(traceID, spanID)
				}
			}()
		}
		wg.Wait()
		assert.Equal(t, 10, len(idMap.m))

		for i := 0; i < 10; i++ {
			spanIDs := idMap.Pop(pcommon.NewTraceID([16]byte{1, 2, 3, byte(i)}))
			assert.Len(t, spanIDs, 10)
		}
	})
}

func TestQueue(t *testing.T) {
	t.Run("Policy=Post", func(t *testing.T) {
		q := New("post", 10)
		defer q.Clean()

		g := generator.NewTracesGenerator(define.TracesOptions{
			SpanCount: 10,
		})

		// put
		assert.NoError(t, q.Put(1001, g.Generate()))
		assert.NoError(t, q.Put(1002, g.Generate()))

		// pop
		assert.Nil(t, q.Pop(1001, random.TraceID()))
	})

	t.Run("Policy=Full", func(t *testing.T) {
		q := New("full", 10)
		defer q.Clean()

		// span1
		traces1 := ptrace.NewTraces()
		span1 := traces1.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		traceID1 := random.TraceID()
		span1.SetTraceID(traceID1)

		// span2
		traces2 := ptrace.NewTraces()
		span2 := traces2.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		traceID2 := random.TraceID()
		span2.SetTraceID(traceID2)

		assert.NoError(t, q.Put(1001, traces1))
		assert.NoError(t, q.Put(1002, traces2))

		// 1001
		ts := q.Pop(1001, traceID1)
		assert.Len(t, ts, 1)
		t1 := ts[0].ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).TraceID()
		assert.Equal(t, traceID1, t1)
		assert.NoError(t, q.Put(1001, traces1))

		// t2
		ts = q.Pop(1002, traceID2)
		assert.Len(t, ts, 1)
		t2 := ts[0].ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).TraceID()
		assert.Equal(t, traceID2, t2)
		assert.Len(t, q.Pop(1001, random.TraceID()), 0)
		assert.Nil(t, q.Pop(1003, random.TraceID()))
	})
}
