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
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func makeTempDir(logf func(format string, args ...any)) string {
	dir, err := os.MkdirTemp("", "stor_test")
	if err != nil {
		panic(err)
	}
	logf("make temp dir: %v", dir)
	return dir
}

func removeTempDir(logf func(format string, args ...any), dir string) {
	err := os.RemoveAll(dir)
	if err != nil {
		panic(err)
	}
	logf("remove temp dir: %v", dir)
}

func TestGlobal(t *testing.T) {
	InitStorage(".", "")
	stor := GetOrCreateStorage(1001)
	assert.NotNil(t, stor)
	assert.NoError(t, CleanStorage())
}

func TestBuiltinStorage(t *testing.T) {
	sc := NewStorageController(".", TypeBuiltin)
	defer sc.Clean()

	stor := sc.GetOrCreate(1001)
	testStorage(t, stor)
	assert.NoError(t, stor.Clean())
}

func TestLeveldbStorage(t *testing.T) {
	dir := makeTempDir(t.Logf)
	defer removeTempDir(t.Logf, dir)

	sc := NewStorageController(dir, TypeLeveldb)
	defer sc.Clean()

	stor := sc.GetOrCreate(1001)
	testStorage(t, stor)
	assert.NoError(t, stor.Clean())
}

func testStorage(t *testing.T, stor Storage) {
	t.Log("testing storage", stor.Name())

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
		err := stor.Set(TraceKey{TraceID: traceID, SpanID: spanID}, traces)
		assert.NoError(t, err)
	}

	for i := 0; i < 10; i++ {
		traceID := pcommon.NewTraceID([16]byte{1, 2, 3, byte(i)})
		spanID := pcommon.NewSpanID([8]byte{1, byte(i)})

		traces, err := stor.Get(TraceKey{TraceID: traceID, SpanID: spanID})
		assert.NoError(t, err)
		assert.Equal(t, 1, traces.SpanCount())
	}

	traceID := pcommon.NewTraceID([16]byte{1, 2, 3, 1})
	spanID := pcommon.NewSpanID([8]byte{1, 1})

	err := stor.Del(TraceKey{TraceID: traceID, SpanID: spanID})
	assert.NoError(t, err)

	_, err = stor.Get(TraceKey{TraceID: traceID, SpanID: spanID})
	assert.Error(t, err)
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

func benchmarkStoragePut(stor Storage, opt Option) {
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
		_ = stor.Set(tk, traces)
	}
}

func benchmarkBuiltinPut(b *testing.B, opt Option) {
	storMap := make(map[int]Storage)
	start := time.Now()
	wg := sync.WaitGroup{}
	mut := sync.Mutex{}
	for i := 0; i < appCount; i++ {
		id := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			stor := newBuiltinStorage(int32(id))
			benchmarkStoragePut(stor, opt)
			mut.Lock()
			storMap[id] = stor
			mut.Unlock()
		}()
	}
	wg.Wait()
	prettyprint.RuntimeMemStats(b.Logf)
	b.Logf("builtinStorage Put operation take: %v\n", time.Since(start))
	b.FailNow()
}

func BenchmarkBuiltinPutSmallSize(b *testing.B) {
	benchmarkBuiltinPut(b, Option{
		ResourceCount:  5,
		AttributeCount: 5,
		SpanCount:      10,
		EventCount:     5,
		LinkCount:      5,
	})
}

func benchmarkLeveldbPut(b *testing.B, opt Option) {
	start := time.Now()
	dir := makeTempDir(b.Logf)
	defer removeTempDir(b.Logf, dir)

	ctr := NewStorageController(dir, TypeLeveldb)
	defer ctr.Clean()

	storMap := make(map[int]Storage)
	mut := sync.Mutex{}
	wg := sync.WaitGroup{}
	for i := 0; i < appCount; i++ {
		id := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			mut.Lock()
			stor := ctr.GetOrCreate(int32(id))
			storMap[id] = stor
			mut.Unlock()

			benchmarkStoragePut(stor, opt)
		}()
	}
	wg.Wait()
	prettyprint.RuntimeMemStats(b.Logf)
	b.Logf("leveldbStorage Put operation take: %v\n", time.Since(start))
	b.FailNow()
}

func BenchmarkLeveldbPutSmallSize(b *testing.B) {
	benchmarkLeveldbPut(b, Option{
		ResourceCount:  5,
		AttributeCount: 5,
		SpanCount:      10,
		EventCount:     5,
		LinkCount:      5,
	})
}

func BenchmarkLeveldbPutMiddleSize(b *testing.B) {
	benchmarkLeveldbPut(b, Option{
		ResourceCount:  10,
		AttributeCount: 10,
		SpanCount:      30,
		EventCount:     5,
		LinkCount:      5,
	})
}

func BenchmarkLeveldbPutLargeSize(b *testing.B) {
	benchmarkLeveldbPut(b, Option{
		ResourceCount:  30,
		AttributeCount: 30,
		SpanCount:      100,
		EventCount:     20,
		LinkCount:      20,
	})
}
