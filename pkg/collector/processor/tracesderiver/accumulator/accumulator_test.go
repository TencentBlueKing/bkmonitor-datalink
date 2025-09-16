// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package accumulator

import (
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func TestValidateConfig(t *testing.T) {
	conf := Config{}
	conf.Validate()
	assert.Equal(t, prometheus.DefBuckets, conf.Buckets)
}

func TestCalcStats(t *testing.T) {
	r := newRecorder(recorderOptions{
		metricName: "test_metric",
		maxSeries:  100,
		dataID:     1001,
		buckets:    []float64{2, 5, 10, 15},
		gcInterval: time.Minute,
	})
	defer r.Stop()

	dims1 := map[string]string{"label1": "value1"}
	dims2 := map[string]string{"label2": "value2"}

	r.Set(dims1, 1)
	r.Set(dims1, 11)
	r.Set(dims2, 2)
	r.Set(dims2, 12)

	drains := func(ch <-chan *define.Record) {
		for range ch {
		}
	}

	drains(r.buildMetrics(TypeMax))
	drains(r.buildMetrics(TypeMin))
	drains(r.buildMetrics(TypeDelta))

	// reset stats
	for _, stat := range r.statsMap {
		assert.Equal(t, stat.max, MinValue)
		assert.Equal(t, stat.min, MaxValue)
		assert.Equal(t, stat.prev, stat.curr)
	}

	r.Set(dims1, 10)
}

func TestAccumulatorExceeded(t *testing.T) {
	accumulator := New(&Config{
		MetricName:      "bk_apm_count",
		MaxSeries:       10,
		GcInterval:      time.Hour,
		PublishInterval: time.Minute,
	}, nil)

	ids := []int32{1001, 1002}
	for i := 0; i < 100; i++ {
		for _, id := range ids {
			accumulator.Accumulate(id, random.Dimensions(6), float64(i))
		}
	}

	exceeded := accumulator.Exceeded()
	accumulator.Stop()
	assert.Equal(t, 90, exceeded[1001])
	assert.Equal(t, 90, exceeded[1002])
}

func TestAccumulatorNotExceeded(t *testing.T) {
	accumulator := New(&Config{
		MetricName:      "bk_apm_count",
		MaxSeries:       10,
		GcInterval:      time.Hour,
		PublishInterval: time.Minute,
	}, nil)

	ids := []int32{1001, 1002}
	for i := 0; i < 10; i++ {
		for _, id := range ids {
			accumulator.Accumulate(id, random.Dimensions(6), float64(i))
		}
	}

	ret := accumulator.Exceeded()
	accumulator.Stop()
	assert.Equal(t, 0, ret[1001])
	assert.Equal(t, 0, ret[1002])
}

func TestAccumulatorGcOk(t *testing.T) {
	accumulator := New(&Config{
		MetricName:      "bk_apm_count",
		MaxSeries:       10,
		GcInterval:      250 * time.Millisecond,
		PublishInterval: time.Second,
	}, nil)
	accumulator.noAlign = true

	ids := []int32{1001, 1002}
	for i := 0; i < 20; i++ {
		for _, id := range ids {
			accumulator.Accumulate(id, random.Dimensions(6), float64(i))
		}
		if i == 9 {
			exceeded := accumulator.Exceeded()
			assert.Equal(t, 0, exceeded[1001])
			assert.Equal(t, 0, exceeded[1002])
			time.Sleep(time.Second * 2) // 超过 gcInterval
		}
	}

	exceeded := accumulator.Exceeded()
	accumulator.Stop()

	// gc 后所有 series 都不应超限
	assert.Equal(t, 0, exceeded[1001])
	assert.Equal(t, 0, exceeded[1002])
}

func TestAccumulatorGcNotYet(t *testing.T) {
	accumulator := New(&Config{
		MetricName:      "bk_apm_count",
		MaxSeries:       10,
		GcInterval:      time.Second,
		PublishInterval: time.Second,
	}, nil)
	accumulator.noAlign = true

	ids := []int32{1001, 1002}
	for i := 0; i < 20; i++ {
		for _, id := range ids {
			accumulator.Accumulate(id, random.Dimensions(6), float64(i))
		}
		if i == 9 {
			exceeded := accumulator.Exceeded()
			assert.Equal(t, 0, exceeded[1001])
			assert.Equal(t, 0, exceeded[1002])
			time.Sleep(250 * time.Millisecond) // 不超过 gcInterval
		}
	}

	exceeded := accumulator.Exceeded()
	accumulator.Stop()

	// gc 后所有 series 都不应超限
	assert.Equal(t, 10, exceeded[1001])
	assert.Equal(t, 10, exceeded[1002])
}

func testAccumulatorPublish(t *testing.T, dt string, value float64, count int) {
	records := make([]*define.Record, 0)
	accumulator := New(&Config{
		MetricName:      "bk_apm_metric",
		MaxSeries:       10,
		GcInterval:      time.Minute,
		PublishInterval: 250 * time.Millisecond,
		Buckets:         prometheus.DefBuckets,
		Type:            dt,
	}, func(r *define.Record) { records = append(records, r) })
	accumulator.noAlign = true

	dimensions := random.Dimensions(3)
	accumulator.Accumulate(1001, dimensions, 0.1*float64(time.Second))
	accumulator.Accumulate(1001, dimensions, 0.2*float64(time.Second))
	accumulator.Accumulate(1001, dimensions, 0.3*float64(time.Second))
	accumulator.Accumulate(1001, dimensions, 0.4*float64(time.Second))
	accumulator.Accumulate(1001, dimensions, 0.5*float64(time.Second))
	accumulator.Accumulate(1001, dimensions, 1.0*float64(time.Second))

	time.Sleep(time.Second)
	record := records[0]

	metrics := record.Data.(*define.MetricV2Data)
	first := metrics.Data[0]

	v, ok := first.Metrics["bk_apm_metric"]
	assert.True(t, ok)
	assert.Equal(t, value, v)
	accumulator.Stop()
}

func TestAccumulatorPublishCount(t *testing.T) {
	testAccumulatorPublish(t, TypeCount, 6, 1)
}

func TestAccumulatorPublishDelta(t *testing.T) {
	testAccumulatorPublish(t, TypeDelta, 6, 1)
}

func TestAccumulatorPublishDeltaDuration(t *testing.T) {
	testAccumulatorPublish(t, TypeDeltaDuration, 2.5*float64(time.Second), 1)
}

func TestAccumulatorPublishMin(t *testing.T) {
	testAccumulatorPublish(t, TypeMin, 1e8, 1)
}

func TestAccumulatorPublishMax(t *testing.T) {
	testAccumulatorPublish(t, TypeMax, 1e9, 1)
}

func TestAccumulatorPublishSum(t *testing.T) {
	testAccumulatorPublish(t, TypeSum, 2.5*float64(time.Second), 1)
}

func TestAccumulatorPublishBucket(t *testing.T) {
	testAccumulatorPublish(t, TypeBucket, 0, 12)
}

func TestAccumulatorPublishCount10(t *testing.T) {
	records := make([]*define.Record, 0)
	accumulator := New(&Config{
		MetricName:      "bk_apm_count",
		MaxSeries:       10,
		GcInterval:      time.Minute,
		PublishInterval: 1 * time.Second,
		Type:            TypeDelta,
	}, func(r *define.Record) {
		records = append(records, r)
	})
	accumulator.noAlign = true

	dims := random.Dimensions(6)
	for i := 0; i < 10; i++ {
		accumulator.Accumulate(1001, dims, float64(i))
	}

	time.Sleep(time.Second * 2)
	record := records[0]

	metrics := record.Data.(*define.MetricV2Data)
	first := metrics.Data[0]

	v, ok := first.Metrics["bk_apm_count"]
	assert.True(t, ok)
	assert.Equal(t, float64(10), v)
	accumulator.Stop()
}

func testAccumulatorMemoryConsumption(b *testing.B, dataIDCount, iter, dims int) {
	logger.SetLoggerLevel("info")
	accumulator := New(&Config{
		MetricName:      "bk_apm_count",
		MaxSeries:       100000,
		GcInterval:      time.Hour,
		PublishInterval: time.Hour,
		Type:            TypeDelta,
	}, func(r *define.Record) {})

	var dataids []int32
	for i := 1001; i <= 1001+dataIDCount; i++ {
		dataids = append(dataids, int32(i))
	}

	start := time.Now()
	wg := sync.WaitGroup{}
	for _, dataid := range dataids {
		wg.Add(1)
		go func(id int32) {
			defer wg.Done()
			for i := 0; i < iter; i++ {
				accumulator.Accumulate(id, random.FastDimensions(dims), float64(i))
			}
		}(dataid)
	}
	wg.Wait()

	b.Log("Build take:", time.Since(start))

	t0 := time.Now()
	accumulator.doPublish()
	b.Log("Publish take:", time.Since(t0))

	t1 := time.Now()
	accumulator.doGc()
	b.Log("Gc take:", time.Since(t1))

	prettyprint.RuntimeMemStats(b.Logf)
	// select {} // block forever
}

const (
	appCount = 100
	setCount = 100000
	dimCount = 6
)

func BenchmarkAccumulatorStorageConsumption(b *testing.B) {
	testAccumulatorMemoryConsumption(b, appCount, setCount, dimCount)
}

func BenchmarkStatsPointer(b *testing.B) {
	objs := make(map[string]map[string]*rStats)
	for i := 0; i < appCount; i++ {
		obj := make(map[string]*rStats)
		for j := 0; j < setCount; j++ {
			obj[strconv.Itoa(j)] = &rStats{}
		}
		objs[strconv.Itoa(i)] = obj
	}

	for i := 0; i < 5; i++ {
		t0 := time.Now()
		runtime.GC()
		b.Logf("gc%d take: %v", i, time.Since(t0))
	}

	b.Logf("objs len: %d", len(objs))
	b.FailNow()
}

func BenchmarkStatsStruct(b *testing.B) {
	objs := make(map[string]map[string]rStats)
	for i := 0; i < appCount; i++ {
		obj := make(map[string]rStats)
		for j := 0; j < setCount; j++ {
			obj[strconv.Itoa(j)] = rStats{}
		}
		objs[strconv.Itoa(i)] = obj
	}

	for i := 0; i < 5; i++ {
		t0 := time.Now()
		runtime.GC()
		b.Logf("gc%d take: %v", i, time.Since(t0))
	}

	b.Logf("objs len: %d", len(objs))
	b.FailNow()
}

func TestAccumulatorGrowthExceeded(t *testing.T) {
	accumulator := New(&Config{
		MetricName:          "bk_apm_count",
		MaxSeries:           1000,
		MaxSeriesGrowthRate: 10,
		GcInterval:          time.Hour,
		PublishInterval:     time.Minute,
	}, nil)

	ids := []int32{1001, 1002}
	for i := 0; i < 100; i++ {
		for _, id := range ids {
			accumulator.Accumulate(id, random.Dimensions(6), float64(i))
		}
	}

	exceeded := accumulator.Exceeded()
	assert.True(t, accumulator.enableLimitGrowRate())
	assert.Equal(t, 90, exceeded[1001])
	assert.Equal(t, 90, exceeded[1002])

	accumulator.resetGrowthRate()
	for i := 0; i < 100; i++ {
		for _, id := range ids {
			accumulator.Accumulate(id, random.Dimensions(6), float64(i))
		}
	}

	exceeded = accumulator.Exceeded()
	assert.Equal(t, 180, exceeded[1001])
	assert.Equal(t, 180, exceeded[1002])

	accumulator.Stop()
}

func TestAccumulatorGrowthNotExceeded(t *testing.T) {
	accumulator := New(&Config{
		MetricName:          "bk_apm_count",
		MaxSeries:           1000,
		MaxSeriesGrowthRate: 10,
		GcInterval:          time.Hour,
		PublishInterval:     time.Minute,
	}, nil)

	ids := []int32{1001, 1002}
	for i := 0; i < 10; i++ {
		for _, id := range ids {
			accumulator.Accumulate(id, random.Dimensions(6), float64(i))
		}
	}

	ret := accumulator.Exceeded()
	accumulator.Stop()
	assert.Equal(t, 0, ret[1001])
	assert.Equal(t, 0, ret[1002])
}
