// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracestore

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pkg/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pkg/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pkg/random"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pkg/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func TestStorage(t *testing.T) {
	storage := New()
	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 1,
	})

	for i := 0; i < 10; i++ {
		traces := g.Generate()
		span := testkits.FirstSpan(traces)

		traceID := pcommon.NewTraceID([16]byte{1, 2, 3, byte(i)})
		spanID := pcommon.NewSpanID([8]byte{1, byte(i)})
		span.SetTraceID(traceID)
		span.SetSpanID(spanID)
		storage.Set(TraceKey{TraceID: traceID, SpanID: spanID}, traces)
	}

	for i := 0; i < 10; i++ {
		traceID := pcommon.NewTraceID([16]byte{1, 2, 3, byte(i)})
		spanID := pcommon.NewSpanID([8]byte{1, byte(i)})

		traces, ok := storage.Get(TraceKey{TraceID: traceID, SpanID: spanID})
		assert.True(t, ok)
		assert.Equal(t, 1, traces.SpanCount())
	}

	traceID := pcommon.NewTraceID([16]byte{1, 2, 3, 1})
	spanID := pcommon.NewSpanID([8]byte{1, 1})

	storage.Del(TraceKey{TraceID: traceID, SpanID: spanID})

	_, ok := storage.Get(TraceKey{TraceID: traceID, SpanID: spanID})
	assert.False(t, ok)
}

const (
	setCount = 10000
	appCount = 10
)

type Option struct {
	ResourceCount  int
	AttributeCount int
	SpanCount      int
	EventCount     int
	LinkCount      int
}

func testStoragePut(storage *Storage, opt Option) {
	var resourceKeys, attributeKeys []string
	for i := 0; i < opt.ResourceCount; i++ {
		resourceKeys = append(resourceKeys, fmt.Sprintf("resource%d", i))
	}
	for i := 0; i < opt.AttributeCount; i++ {
		attributeKeys = append(attributeKeys, fmt.Sprintf("attribute%d", i))
	}

	g := generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			RandomResourceKeys:  resourceKeys,
			RandomAttributeKeys: attributeKeys,
		},
		SpanCount:  opt.SpanCount,
		EventCount: opt.EventCount,
		LinkCount:  opt.LinkCount,
	})
	traces := g.Generate()

	b, _ := ptrace.NewProtoMarshaler().MarshalTraces(traces)
	size := float64(len(b)) / 1024 / 1024
	logger.Infof("storage put: PerTracesSize=%v(MB), TotalSize=%v(MB)", size, size*setCount)

	for i := 0; i < setCount; i++ {
		tk := TraceKey{
			TraceID: random.TraceID(),
			SpanID:  random.SpanID(),
		}
		storage.Set(tk, traces)
	}
}

func testBuiltinPut(t *testing.T, opt Option) {
	start := time.Now()
	wg := sync.WaitGroup{}
	for i := 0; i < appCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			testStoragePut(New(), opt)
		}()
	}
	wg.Wait()
	prettyprint.RuntimeMemStats(t.Logf)
	t.Logf("builtinStorage Put operation take: %v\n", time.Since(start))
}

func TestStoragePutSmallSize(t *testing.T) {
	testBuiltinPut(t, Option{
		ResourceCount:  5,
		AttributeCount: 5,
		SpanCount:      10,
		EventCount:     5,
		LinkCount:      5,
	})
}
