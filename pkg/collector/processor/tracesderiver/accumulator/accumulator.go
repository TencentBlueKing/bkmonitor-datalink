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
	"math"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/labels"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/tracesderiver/labelstore"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/fasttime"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	seriesExceededTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "accumulator_series_exceeded_total",
			Help:      "Accumulator series exceeded total",
		},
		[]string{"record_type", "id"},
	)

	seriesCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "accumulator_series_count",
			Help:      "Accumulator series count",
		},
		[]string{"record_type", "id"},
	)

	addedSeriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "accumulator_added_series_total",
			Help:      "Accumulator added series total",
		},
		[]string{"record_type", "id"},
	)

	gcDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "accumulator_gc_duration_seconds",
			Help:      "Accumulator gc duration seconds",
			Buckets:   define.DefObserveDuration,
		},
	)

	publishDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "accumulator_published_duration_seconds",
			Help:      "Accumulator published duration seconds",
			Buckets:   define.DefObserveDuration,
		},
	)
)

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) IncSeriesExceededCounter(dataId int32) {
	seriesExceededTotal.WithLabelValues(define.RecordMetrics.S(), strconv.Itoa(int(dataId))).Inc()
}

func (m *metricMonitor) SetSeriesCount(dataId int32, n int) {
	seriesCount.WithLabelValues(define.RecordMetrics.S(), strconv.Itoa(int(dataId))).Set(float64(n))
}

func (m *metricMonitor) IncAddedSeriesCounter(dataId int32) {
	addedSeriesTotal.WithLabelValues(define.RecordMetrics.S(), strconv.Itoa(int(dataId))).Inc()
}

func (m *metricMonitor) ObserveGcDuration(t time.Time) {
	gcDuration.Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) ObservePublishedDuration(t time.Time) {
	publishDuration.Observe(time.Since(t).Seconds())
}

const (
	TypeMin           = "min"
	TypeMax           = "max"
	TypeDelta         = "delta"
	TypeDeltaDuration = "delta_duration"
	TypeCount         = "count"
	TypeSum           = "sum"
	TypeBucket        = "bucket"

	MaxValue = math.MaxFloat64
	MinValue = math.SmallestNonzeroFloat64
)

func NanValue() float64 {
	return math.NaN()
}

type rStats struct {
	prev    float64
	curr    float64
	min     float64
	max     float64
	currSum float64
	prevSum float64
	buckets []float64
	updated int64
}

type recorder struct {
	done    chan struct{}
	stopped atomic.Bool
	wg      sync.WaitGroup

	metricName          string
	dataID              int32
	gcInterval          time.Duration
	maxSeries           int
	buckets             []float64
	maxSeriesGrowthRate int

	storage *labelstore.Storage
	mut     sync.RWMutex

	// https://github.com/golang/go/issues/9477
	// map 中如果 values 为指针类型 gc 扫描的开销会增大不少
	// 参见 benchmark
	statsMap         map[uint64]rStats
	exceeded         int
	seriesGrowthRate int
}

type recorderOptions struct {
	metricName          string
	maxSeries           int
	dataID              int32
	buckets             []float64
	gcInterval          time.Duration
	maxSeriesGrowthRate int
}

func newRecorder(opts recorderOptions) *recorder {
	buckets := opts.buckets
	sort.Float64s(buckets)
	r := &recorder{
		done:                make(chan struct{}, 1),
		metricName:          opts.metricName,
		dataID:              opts.dataID,
		gcInterval:          opts.gcInterval,
		maxSeries:           opts.maxSeries,
		buckets:             toNanoseconds(buckets),
		maxSeriesGrowthRate: opts.maxSeriesGrowthRate,
		storage:             labelstore.New(),
		statsMap:            map[uint64]rStats{},
	}

	r.wg.Add(1)
	go r.updateMetrics()
	return r
}

func toNanoseconds(buckets []float64) []float64 {
	ret := make([]float64, 0, len(buckets)+1)
	for i := 0; i < len(buckets); i++ {
		ret = append(ret, buckets[i]*1e9)
	}
	ret = append(ret, math.MaxFloat64)
	return ret
}

func (r *recorder) updateMetrics() {
	defer r.wg.Done()

	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			r.storage.Clean() // 退出前释放
			return

		case <-ticker.C:
			DefaultMetricMonitor.SetSeriesCount(r.dataID, r.Total())
		}
	}
}

func (r *recorder) Stop() {
	r.stopped.Store(true)
	close(r.done)
	r.wg.Wait() // 确保清理操作已经执行
}

func (r *recorder) Total() int {
	r.mut.RLock()
	defer r.mut.RUnlock()
	return len(r.statsMap)
}

// Set 更新 labels 缓存
func (r *recorder) Set(dims map[string]string, value float64) bool {
	if r.stopped.Load() {
		return false
	}

	h := labels.HashFromMap(dims)

	r.mut.Lock()
	defer r.mut.Unlock()

	if len(r.statsMap) >= r.maxSeries {
		logger.Debugf("got exceeded series labels: %v", dims)
		DefaultMetricMonitor.IncSeriesExceededCounter(r.dataID)
		r.exceeded++
		return false
	}

	s, ok := r.statsMap[h]
	if !ok {
		if r.enableLimitGrowRate() {
			r.seriesGrowthRate += 1
			if r.seriesGrowthRate > r.maxSeriesGrowthRate {
				logger.Debugf("growth rate exceeded, series labels: %v", dims)
				DefaultMetricMonitor.IncSeriesExceededCounter(r.dataID)
				r.exceeded++
				return false
			}
		}

		DefaultMetricMonitor.IncAddedSeriesCounter(r.dataID)
		s = rStats{}
		s.min = MaxValue
		s.max = MinValue
		s.buckets = make([]float64, len(r.buckets))
	}

	s.curr += 1
	s.currSum += value

	if s.max < value {
		s.max = value
	}
	if s.min > value {
		s.min = value
	}

	for i := 0; i < len(r.buckets); i++ {
		if r.buckets[i] >= value {
			s.buckets[i]++
		}
	}

	s.updated = fasttime.UnixTimestamp()
	r.statsMap[h] = s

	// fastpath: 大多数请求都会命中缓存
	if exist := r.storage.Exist(h); exist {
		return true
	}

	// slowpath: alloc labels 开销较大 尽量减少此操作
	r.storage.SetIf(h, dims)
	return true
}

// Clean 清理计数器缓存
func (r *recorder) Clean() {
	now := time.Now().Unix()
	dropped := make(map[uint64]struct{})
	secs := int64(r.gcInterval.Seconds())

	r.mut.RLock()
	for h, stat := range r.statsMap {
		if now-stat.updated > secs {
			dropped[h] = struct{}{}
		}
	}
	r.mut.RUnlock()

	for h := range dropped {
		r.mut.Lock() // 尽量减少锁临界区
		delete(r.statsMap, h)
		r.storage.Del(h)
		r.mut.Unlock()
	}
}

func (r *recorder) ResetGrowthRate() {
	r.seriesGrowthRate = 0
}

func (r *recorder) Min() <-chan *define.Record {
	return r.buildMetrics(TypeMin)
}

func (r *recorder) Max() <-chan *define.Record {
	return r.buildMetrics(TypeMax)
}

func (r *recorder) Delta() <-chan *define.Record {
	return r.buildMetrics(TypeDelta)
}

func (r *recorder) DeltaDuration() <-chan *define.Record {
	return r.buildMetrics(TypeDeltaDuration)
}

func (r *recorder) Count() <-chan *define.Record {
	return r.buildMetrics(TypeCount)
}

func (r *recorder) Sum() <-chan *define.Record {
	return r.buildMetrics(TypeSum)
}

func (r *recorder) Bucket() <-chan *define.Record {
	return r.buildMetrics(TypeBucket)
}

func (r *recorder) enableLimitGrowRate() bool {
	return r.maxSeriesGrowthRate > 0
}

type LeValue struct {
	Le    string
	Value float64
}

func (r *recorder) calc(kind string, k uint64, stat rStats) (rStats, []define.MetricV2) {
	lbs, ok := r.storage.Get(k)
	if !ok {
		return stat, nil
	}

	val := NanValue()
	leValues := make([]LeValue, 0, len(r.buckets))

	switch kind {
	// Min/Max/Delta/DeltaDuration 需要修改状态
	case TypeMin:
		if stat.min != MaxValue {
			val = stat.min
			stat.min = MaxValue
		}
	case TypeMax:
		if stat.max != MinValue {
			val = stat.max
			stat.max = MinValue
		}
	case TypeDelta:
		val = stat.curr - stat.prev
		stat.prev = stat.curr
		if val < 0 {
			val = NanValue() // 无效值
		}
	case TypeDeltaDuration:
		val = stat.currSum - stat.prevSum
		stat.prevSum = stat.currSum
		if val < 0 {
			val = NanValue() // 无效值
		}

		// Count/Sum/Bucket 不需要更改状态
	case TypeCount:
		val = stat.curr
	case TypeSum:
		val = stat.currSum
	case TypeBucket:
		for i := 0; i < len(stat.buckets); i++ {
			le := strconv.FormatFloat(r.buckets[i], 'f', -1, 64)
			if r.buckets[i] == math.MaxFloat64 {
				le = "+Inf"
			}
			leValues = append(leValues, LeValue{
				Le:    le,
				Value: stat.buckets[i],
			})
		}
	}

	var metrics []define.MetricV2
	unixMill := time.Now().UnixMilli()

	// histogram 类型处理
	if len(leValues) > 0 {
		for _, lev := range leValues {
			dims := utils.CloneMap(lbs) // 复制新的 labels 保证读写安全
			dims["le"] = lev.Le
			metrics = append(metrics, define.MetricV2{
				Metrics:   map[string]float64{r.metricName: lev.Value},
				Timestamp: unixMill,
				Dimension: dims,
			})
		}
	}

	if !math.IsNaN(val) {
		metrics = append(metrics, define.MetricV2{
			Metrics:   map[string]float64{r.metricName: val},
			Timestamp: unixMill,
			Dimension: utils.CloneMap(lbs),
		})
	}

	return stat, metrics
}

func (r *recorder) buildMetrics(kind string) <-chan *define.Record {
	// 复制 keys 列表 尽量减少锁临界区
	r.mut.RLock()
	ks := make([]uint64, 0, len(r.statsMap))
	for k := range r.statsMap {
		ks = append(ks, k)
	}
	r.mut.RUnlock()

	ch := make(chan *define.Record, 1)
	const batch = 5000

	go func() {
		defer close(ch) // 退出前必须确保 ch 被关闭

		count := 0
		ms := make([]define.MetricV2, 0, batch)
		for _, k := range ks {
			// 尽可能使每次持有锁的周期更小一点
			// 保证给 Set 操作抢占的机会
			r.mut.Lock()
			stat, ok := r.statsMap[k]
			if !ok {
				r.mut.Unlock() // 避免并发场景下 stat 已经被清理
				continue
			}

			newStat, metrics := r.calc(kind, k, stat)
			if len(metrics) > 0 {
				ms = append(ms, metrics...)
			}
			count += len(metrics)
			r.statsMap[k] = newStat
			r.mut.Unlock()

			if count >= batch {
				ch <- &define.Record{
					RecordType:  define.RecordMetricV2,
					RequestType: define.RequestDerived,
					Token:       define.Token{MetricsDataId: r.dataID},
					Data:        &define.MetricV2Data{Data: ms},
				}
				// 状态重置
				count = 0
				ms = make([]define.MetricV2, 0, batch)
			}
		}

		if count > 0 {
			ch <- &define.Record{
				RecordType:  define.RecordMetricV2,
				RequestType: define.RequestDerived,
				Token:       define.Token{MetricsDataId: r.dataID},
				Data:        &define.MetricV2Data{Data: ms},
			}
		}
	}()

	return ch
}

type Config struct {
	MetricName          string
	MaxSeries           int
	GcInterval          time.Duration
	PublishInterval     time.Duration
	Buckets             []float64
	Type                string
	MaxSeriesGrowthRate int
}

// Validate 验证配置默认值
func (ac *Config) Validate() {
	if ac.MaxSeries <= 0 {
		ac.MaxSeries = 100000 // 100k
	}
	if ac.GcInterval <= 0 {
		ac.GcInterval = time.Hour
	}
	if ac.PublishInterval <= 0 {
		ac.PublishInterval = time.Minute
	}
	if len(ac.Buckets) == 0 {
		ac.Buckets = prometheus.DefBuckets // 使用 prometheus 默认的 bucket
	}
}

// GetBuckets 复制 buckets 列表
func (ac *Config) GetBuckets() []float64 {
	dst := make([]float64, len(ac.Buckets))
	copy(dst, ac.Buckets)
	return dst
}

func New(conf *Config, publishFunc func(r *define.Record)) *Accumulator {
	logger.Debugf("accumulator config: %+v", conf)
	accumulator := &Accumulator{
		conf:            conf,
		recorders:       map[int32]*recorder{},
		publishFunc:     publishFunc,
		done:            make(chan struct{}, 1),
		publishInterval: conf.PublishInterval,
		gcInterval:      conf.GcInterval / 2, // 以 0.5*gcInterval 频率进行清理
	}
	go accumulator.gc()
	go accumulator.resetGrowthRateLoop()

	if publishFunc != nil {
		go accumulator.publish()
	}

	return accumulator
}

type Accumulator struct {
	mut         sync.RWMutex
	recorders   map[int32]*recorder
	conf        *Config
	publishFunc func(r *define.Record)
	noAlign     bool // 默认对齐时间戳

	done    chan struct{}
	stopped atomic.Bool

	publishInterval time.Duration
	gcInterval      time.Duration
}

func (a *Accumulator) consumeMetrics(ch <-chan *define.Record) {
	for record := range ch {
		a.publishFunc(record)
	}
}

func (a *Accumulator) doPublish() {
	start := time.Now()
	a.mut.RLock()
	rs := make([]*recorder, 0, len(a.recorders))
	for _, r := range a.recorders {
		rs = append(rs, r)
	}
	a.mut.RUnlock()

	for _, r := range rs {
		switch a.conf.Type {
		case TypeDelta:
			a.consumeMetrics(r.Delta())
		case TypeDeltaDuration:
			a.consumeMetrics(r.DeltaDuration())
		case TypeCount:
			a.consumeMetrics(r.Count())
		case TypeMin:
			a.consumeMetrics(r.Min())
		case TypeMax:
			a.consumeMetrics(r.Max())
		case TypeSum:
			a.consumeMetrics(r.Sum())
		case TypeBucket:
			a.consumeMetrics(r.Bucket())
		}
		logger.Debugf("accumulator got dataid=%d, series=%d", r.dataID, r.Total())
	}
	DefaultMetricMonitor.ObservePublishedDuration(start)
}

func (a *Accumulator) publish() {
	if !a.noAlign {
		duration := time.Duration(60-(time.Now().Unix()%60)) * time.Second
		time.Sleep(duration)
	}

	ticker := time.NewTicker(a.publishInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			a.doPublish()
		}
	}
}

func (a *Accumulator) doGc() {
	start := time.Now()
	a.mut.RLock()
	rs := make([]*recorder, 0, len(a.recorders))
	for _, r := range a.recorders {
		rs = append(rs, r)
	}
	a.mut.RUnlock()

	for _, r := range rs {
		r.Clean()
	}
	DefaultMetricMonitor.ObserveGcDuration(start)
}

func (a *Accumulator) resetGrowthRate() {
	a.mut.RLock()
	rs := make([]*recorder, 0, len(a.recorders))
	for _, r := range a.recorders {
		rs = append(rs, r)
	}
	a.mut.RUnlock()

	for _, r := range rs {
		r.ResetGrowthRate()
	}
}

func (a *Accumulator) gc() {
	ticker := time.NewTicker(a.gcInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			a.doGc()
		}
	}
}

func (a *Accumulator) resetGrowthRateLoop() {
	if !a.enableLimitGrowRate() {
		return
	}

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			a.resetGrowthRate()
		}
	}
}

func (a *Accumulator) Stop() {
	close(a.done)
	a.stopped.Store(true)

	a.mut.Lock()
	defer a.mut.Unlock()

	for _, r := range a.recorders {
		r.Stop()
	}
}

func (a *Accumulator) Exceeded() map[int32]int {
	a.mut.Lock()
	defer a.mut.Unlock()

	ret := make(map[int32]int)
	for _, r := range a.recorders {
		ret[r.dataID] = r.exceeded
	}
	return ret
}

func (a *Accumulator) Accumulate(dataID int32, dims map[string]string, value float64) bool {
	if a.stopped.Load() {
		return false
	}

	var r *recorder

	// 先尝试使用读锁
	a.mut.RLock()
	if _, ok := a.recorders[dataID]; ok {
		r = a.recorders[dataID]
	}
	a.mut.RUnlock()
	if r != nil {
		return r.Set(dims, value)
	}

	// 写锁保护
	a.mut.Lock()
	if v, ok := a.recorders[dataID]; ok {
		r = v
	} else {
		opts := recorderOptions{
			metricName:          a.conf.MetricName,
			maxSeries:           a.conf.MaxSeries,
			dataID:              dataID,
			buckets:             a.conf.GetBuckets(),
			gcInterval:          a.conf.GcInterval,
			maxSeriesGrowthRate: a.conf.MaxSeriesGrowthRate,
		}
		r = newRecorder(opts)
		a.recorders[dataID] = r
	}
	a.mut.Unlock()
	return r.Set(dims, value)
}

func (a *Accumulator) enableLimitGrowRate() bool {
	return a.conf.MaxSeriesGrowthRate > 0
}
