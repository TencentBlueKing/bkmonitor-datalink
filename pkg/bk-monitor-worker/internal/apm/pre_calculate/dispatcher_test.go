// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pre_calculate

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
)

func TestDispatcherRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	appA := core.AppKey{BkBizId: "2", AppName: "app-a"}
	appB := core.AppKey{BkBizId: "3", AppName: "app-b"}
	spanChanA := make(chan []window.StandardSpan, 1)
	spanChanB := make(chan []window.StandardSpan, 1)
	routes := map[core.AppKey]chan []window.StandardSpan{
		appA: spanChanA,
		appB: spanChanB,
	}
	input := make(chan []window.StandardSpan, 1)
	errChan := make(chan error, 1)

	go newDispatcher(ctx, "1001", routes, errChan).Run(input)
	input <- []window.StandardSpan{
		{TraceId: "trace-a", BkBizId: "2", AppName: appA.AppName},
		{TraceId: "trace-b", BkBizId: "3", AppName: appB.AppName},
		{TraceId: "trace-fallback"},
		{TraceId: "trace-drop", BkBizId: "4", AppName: "app-c"},
	}
	close(input)

	gotA := <-spanChanA
	gotB := <-spanChanB
	assert.ElementsMatch(t, []string{"trace-a"}, traceIds(gotA))
	assert.ElementsMatch(t, []string{"trace-b"}, traceIds(gotB))

	_, okA := <-spanChanA
	_, okB := <-spanChanB
	assert.False(t, okA)
	assert.False(t, okB)
}

func TestDispatcherRunDropsMissingAppKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	appA := core.AppKey{BkBizId: "2", AppName: "app-a"}
	spanChanA := make(chan []window.StandardSpan, 1)
	input := make(chan []window.StandardSpan, 1)
	errChan := make(chan error, 1)

	go newDispatcher(ctx, "1001", map[core.AppKey]chan []window.StandardSpan{appA: spanChanA}, errChan).Run(input)
	input <- []window.StandardSpan{
		{TraceId: "trace-a", BkBizId: "2", AppName: appA.AppName},
		{TraceId: "trace-fallback"},
	}
	close(input)

	gotA := <-spanChanA
	assert.ElementsMatch(t, []string{"trace-a"}, traceIds(gotA))

	_, okA := <-spanChanA
	assert.False(t, okA)
}

func traceIds(spans []window.StandardSpan) []string {
	res := make([]string, 0, len(spans))
	for _, span := range spans {
		res = append(res, span.TraceId)
	}
	return res
}

func BenchmarkDispatcherDispatchBatch(b *testing.B) {
	// goos: darwin
	// goarch: arm64
	// pkg: github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate
	// cpu: Apple M4 Pro
	// BenchmarkDispatcherDispatchBatch/apps_10_batch_10000-14         	    2874	    373474 ns/op	 1557687 B/op	      10 allocs/op
	// BenchmarkDispatcherDispatchBatch/apps_20_batch_10000-14         	    3051	    363022 ns/op	 1639650 B/op	      20 allocs/op
	// BenchmarkDispatcherDispatchBatch/apps_50_batch_10000-14         	    2970	    388789 ns/op	 1639956 B/op	      50 allocs/op
	// BenchmarkDispatcherDispatchBatch/apps_100_batch_10000-14        	    2781	    405983 ns/op	 1639951 B/op	     100 allocs/op

	for _, tc := range []struct {
		name      string
		appCount  int
		batchSize int
	}{
		{name: "apps_10_batch_10000", appCount: 10, batchSize: 10000},
		{name: "apps_20_batch_10000", appCount: 20, batchSize: 10000},
		{name: "apps_50_batch_10000", appCount: 50, batchSize: 10000},
		{name: "apps_100_batch_10000", appCount: 100, batchSize: 10000},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkDispatcherDispatchBatch(b, tc.appCount, tc.batchSize)
		})
	}
}

func benchmarkDispatcherDispatchBatch(b *testing.B, appCount, batchSize int) {
	routes := benchmarkDispatchRoutes(appCount)
	dispatcher := newDispatcher(
		context.Background(), "benchmark-data-id", routes, make(chan error, 1),
	)
	buckets := make(map[chan []window.StandardSpan][]window.StandardSpan, len(dispatcher.routes))
	for _, spanChan := range dispatcher.routes {
		buckets[spanChan] = make([]window.StandardSpan, 0)
	}
	batch := benchmarkBatch(appCount, batchSize)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dispatcher.dispatchBatch(batch, buckets)
		drainDispatcher(dispatcher)
	}
}

func benchmarkDispatchRoutes(appCount int) map[core.AppKey]chan []window.StandardSpan {
	routes := make(map[core.AppKey]chan []window.StandardSpan, appCount)
	for i := 0; i < appCount; i++ {
		appKey := core.AppKey{BkBizId: strconv.Itoa(1000 + i), AppName: benchmarkAppName(i)}
		routes[appKey] = make(chan []window.StandardSpan, 1)
	}
	return routes
}

func benchmarkBatch(appCount, batchSize int) []window.StandardSpan {
	batch := make([]window.StandardSpan, batchSize)
	for i := 0; i < batchSize; i++ {
		appIndex := i % appCount
		batch[i] = window.StandardSpan{
			BkBizId: strconv.Itoa(1000 + appIndex),
			AppName: benchmarkAppName(appIndex),
			TraceId: strconv.Itoa(i),
		}
	}
	return batch
}

func benchmarkAppName(i int) string {
	return fmt.Sprintf("app-%d", i)
}

func drainDispatcher(dispatcher *dispatcher) {
	for _, spanChan := range dispatcher.routes {
		<-spanChan
	}
}
