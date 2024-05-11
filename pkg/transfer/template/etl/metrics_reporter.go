// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
)

const (
	defaultMetricNameRegex = "^[a-zA-Z0-9_]+$"
	redisMetricKeyPrefix   = "bkmonitor:metrics_"
	redisDimensionPrefix   = "bkmonitor:metric_dimensions_"
	dimensionValuesOpt     = "dimension_values"
	metricNameRegexOpt     = "metric_name_regex"
)

var (
	metricNamePattern = regexp.MustCompile(defaultMetricNameRegex)
	reconcilePeriod   = time.Duration(7200+rand.Intn(10)) * time.Minute // reconcile 周期为 5d+[0, 10)m
	timeUnix          = func() int64 { return time.Now().Unix() }
	syncPeriod        = time.Second * 5

	monitorMetricsReporterRecords = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "metrics_reporter_records",
		Help:      "Metrics reporter records",
	}, []string{"id"})
)

func init() {
	prometheus.MustRegister(monitorMetricsReporterRecords)
}

// DimensionsEntity 维度信息 保存到 redis 中的结构
type DimensionsEntity struct {
	Dimensions DimensionMap `json:"dimensions"`
}

// DimensionMap 存储 dimension key/values
type DimensionMap map[string]*DimensionItem

// DimensionItem 单个维度详情
type DimensionItem struct {
	LastUpdateTime int64    `json:"last_update_time"`
	Values         []string `json:"values"`
}

type RedisKV interface {
	ZAddBatch(string, map[string]float64) error
	HSetBatch(string, map[string]string) error
	HGetBatch(string, []string) ([]interface{}, error)
}

// MetricsReportProcessor 用于提取 metric 与 dimensions 对应关系
type MetricsReportProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	ctx        context.Context
	redisStore RedisKV
	once       sync.Once

	dataid            int
	redisMetricKey    string             // metricTimeMap 数据存放在 redis 中 sorted set key
	redisDimensionKey string             // metricDimensionsMap 数据存放在 redis 中 hash key
	dimensionOpt      DimensionValuesOpt // 需要上报的维度值列表

	metricStoreMut   sync.Mutex
	metricStore      map[string]float64
	dimensionStore   *DimensionStore
	dimensionUpdated chan struct{}
}

type Label struct {
	Name  string
	Value string
}

// DimensionStore dimension 存储实现
type DimensionStore struct {
	mut   sync.Mutex
	store map[string]map[Label]int64
}

func NewDimensionStore() *DimensionStore {
	return &DimensionStore{store: map[string]map[Label]int64{}}
}

// Set 返回 true 标识传入的 key 已经存在 反之为 false
func (ds *DimensionStore) Set(metric string, label Label) bool {
	ds.mut.Lock()
	defer ds.mut.Unlock()

	// 如果 metric 不存在 建立 map 并返回 false
	dim, ok := ds.store[metric]
	if !ok {
		ds.store[metric] = map[Label]int64{label: timeUnix()}
		return false
	}

	_, ok = dim[label]
	dim[label] = timeUnix()
	return ok
}

// GetOrMergeDimensions 根据 metric 返回合并后的 dimensions 结果
func (ds *DimensionStore) GetOrMergeDimensions(metric string, remoteDimensions DimensionMap) DimensionMap {
	ds.mut.Lock()
	defer ds.mut.Unlock()

	// 先从本地 dimensions 缓存中搜索
	ret := make(DimensionMap)
	localDimensions := ds.store[metric]
	for lbs, t := range localDimensions {
		if _, ok := ret[lbs.Name]; !ok {
			ret[lbs.Name] = &DimensionItem{}
		}
		ret[lbs.Name].LastUpdateTime = t
		if len(lbs.Value) > 0 {
			ret[lbs.Name].Values = append(ret[lbs.Name].Values, lbs.Value)
		}
	}

	// 接着再 merge 远程 dimensions
	for name, v := range remoteDimensions {
		di := ret[name]
		// 远程存在而本地不存在 使用远程的
		if di == nil {
			ret[name] = &DimensionItem{
				LastUpdateTime: v.LastUpdateTime,
				Values:         v.Values,
			}
			continue
		}

		// 存在的话两者合并 先排序再使用二分搜索 提高效率
		miss := make([]string, 0)
		sort.Strings(di.Values)
		for _, val := range v.Values {
			if len(val) <= 0 {
				continue
			}
			// 返回索引等于数据长度则证明 val 不在列表中
			if sort.SearchStrings(di.Values, val) == len(di.Values) {
				miss = append(miss, val)
			}
		}

		// 以最新更新时间为准
		di.Values = append(di.Values, miss...)
		if di.LastUpdateTime < v.LastUpdateTime {
			di.LastUpdateTime = v.LastUpdateTime
		}
	}
	for k := range ret {
		sort.Strings(ret[k].Values)
	}
	return ret
}

func (p *MetricsReportProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	p.process(d, nil, outputChan, killChan)
}

func (p *MetricsReportProcessor) process(d define.Payload, record *define.ETLRecord, outputChan chan<- define.Payload, _ chan<- error) {
	p.once.Do(func() {
		p.start()
	})

	// 通过 Process 函数入口进来的，仍旧需要序列化一遍，得到 Record 结构体
	// 兼容原有逻辑
	if record == nil {
		defer func() {
			outputChan <- d
		}()
		var dst define.ETLRecord
		if err := d.To(&dst); err != nil {
			p.CounterFails.Inc()
			logging.Errorf("payload %v to record failed: %v", d, err)
			return
		}
		record = &dst
	}

	var gotNewDimensions bool
	now := timeUnix()
	for metric := range record.Metrics {
		if !metricNamePattern.MatchString(metric) {
			logging.Warnf("the metric name: %s does not satisfy the regex: %s", metric, metricNamePattern.String())
			p.CounterFails.Inc()
			continue
		}

		p.metricStoreMut.Lock()
		p.metricStore[metric] = float64(now)
		p.metricStoreMut.Unlock()

		// 处理需要提取的维度信息
		for name, value := range record.Dimensions {
			v, ok := value.(string)
			if !ok {
				continue
			}
			// 如果 dimension name 没有找到 对  value 赋空值
			if _, ok = p.dimensionOpt.dimensionValues[name]; !ok {
				v = ""
			}
			// 更新标识位 不存在则表示为新数据 需要同步
			has := p.dimensionStore.Set(metric, Label{Name: name, Value: v})
			gotNewDimensions = gotNewDimensions || !has
		}

		// 处理需要拼接的维度信息
		for name, values := range p.dimensionOpt.dimensionJoin {
			fields := make([]string, 0)
			for _, v := range values {
				// 维度没找到 终止流程
				val, ok := record.Dimensions[v]
				if !ok {
					break
				}

				// 转换为 string 类型 转换失败同样终止流程
				s, ok := val.(string)
				if !ok {
					break
				}
				fields = append(fields, s)
			}

			// 如果 fields 没完全匹配 那置空
			if len(fields) != len(values) {
				fields = []string{}
			}

			has := p.dimensionStore.Set(metric, Label{Name: name, Value: strings.Join(fields, "/")})
			gotNewDimensions = gotNewDimensions || !has
		}
	}

	if gotNewDimensions {
		select {
		case p.dimensionUpdated <- struct{}{}:
		default:
		}
	}
	p.CounterSuccesses.Inc()
}

func (p *MetricsReportProcessor) start() {
	go p.syncRedis()
	go p.reconcileRedis()
}

func (p *MetricsReportProcessor) init() error {
	store := define.StoreFromContext(p.ctx)
	if store == nil {
		return errors.Wrapf(define.ErrOperationForbidden, "store not found")
	}
	redisStore, ok := store.(*storage.RedisStore)
	if !ok {
		return errors.Wrapf(define.ErrOperationForbidden, "store should be redis")
	}
	p.redisStore = redisStore
	return nil
}

func (p *MetricsReportProcessor) reconcileRedis() {
	ticker := time.NewTicker(reconcilePeriod)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return

		case <-ticker.C:
			select {
			case p.dimensionUpdated <- struct{}{}:
			default:
			}
		}
	}
}

func (p *MetricsReportProcessor) syncRedis() {
	timer := time.NewTimer(syncPeriod)
	timer.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return

		case <-p.dimensionUpdated:
			timer.Reset(syncPeriod)

		case <-timer.C:
			if err := p.flushRedis(); err != nil {
				logging.Errorf("failed to flush redis, err: %v", err)
			}
		}
	}
}

func (p *MetricsReportProcessor) flushRedis() error {
	p.metricStoreMut.Lock()
	cloned := make(map[string]float64)
	for k, v := range p.metricStore {
		cloned[k] = v
	}
	p.metricStoreMut.Unlock()

	if err := p.redisStore.ZAddBatch(p.redisMetricKey, cloned); err != nil {
		return fmt.Errorf("%v refresh redis failed, ZAddBatch err: %+v", p, err)
	}
	monitorMetricsReporterRecords.WithLabelValues(strconv.Itoa(p.dataid)).Set(float64(len(cloned)))

	localMetrics := make([]string, 0, len(cloned))
	for metric := range cloned {
		localMetrics = append(localMetrics, metric)
	}

	// 获取 redis 原有的 metrics 维度信息
	body, err := p.redisStore.HGetBatch(p.redisDimensionKey, localMetrics)
	if err != nil {
		return fmt.Errorf("%v refresh redis failed, HGetBatch err: %+v", p, err)
	}
	content := convertRedisBody(body)

	data := make(map[string]DimensionsEntity)
	for i, dim := range content {
		metric := localMetrics[i]
		// redis 中没有此维度 则使用本地缓存记录
		if dim == "" {
			data[metric] = DimensionsEntity{
				Dimensions: p.dimensionStore.GetOrMergeDimensions(metric, nil),
			}
			continue
		}

		// redis 中存在记录 则合并
		var entity DimensionsEntity
		if err := json.Unmarshal([]byte(dim), &entity); err != nil {
			logging.Warnf("failed to unmarshal dimensions, err: %v", err)
			continue
		}

		data[metric] = DimensionsEntity{
			Dimensions: p.dimensionStore.GetOrMergeDimensions(metric, entity.Dimensions),
		}
	}

	encoded := make(map[string]string)
	for metric, di := range data {
		b, err := json.Marshal(di)
		if err != nil {
			continue
		}
		encoded[metric] = string(b)
	}

	// 更新 metrics 的维度信息
	if err := p.redisStore.HSetBatch(p.redisDimensionKey, encoded); err != nil {
		return fmt.Errorf("%v refresh redis failed, HSetBatch err: %+v", p, err)
	}

	logging.Infof("%v refresh redis success", p)
	return nil
}

func convertRedisBody(input []interface{}) []string {
	var lst []string
	for _, value := range input {
		if value == nil {
			lst = append(lst, "")
			continue
		}
		if v, ok := value.(string); ok {
			lst = append(lst, v)
			continue
		}
		lst = append(lst, fmt.Sprintf("%v", value))
	}
	return lst
}

// DimensionValuesOpt dimension values 配置规则
type DimensionValuesOpt struct {
	dimensionValues map[string]struct{} // 单维度
	dimensionJoin   map[string][]string // 多维度拼装
}

// getDimensionValuesOpt 获取该数据源结果表需要保存维度值的维度列表以及拼接的维度值的列表
func getDimensionValuesOpt(pipelineConfig *config.PipelineConfig) DimensionValuesOpt {
	dimensionValOpt := DimensionValuesOpt{
		dimensionValues: make(map[string]struct{}),
		dimensionJoin:   make(map[string][]string),
	}

	for _, table := range pipelineConfig.ResultTableList {
		if table.Option != nil {
			if value, ok := table.Option[dimensionValuesOpt]; ok {
				switch val := value.(type) {
				case []string:
					for _, v := range val {
						if strings.Contains(v, "/") {
							dimensionValOpt.dimensionJoin[v] = strings.Split(v, "/")
						} else {
							dimensionValOpt.dimensionValues[v] = struct{}{}
						}
					}

				case []interface{}:
					for _, value := range val {
						if v, ok := value.(string); ok {
							if strings.Contains(v, "/") {
								dimensionValOpt.dimensionJoin[v] = strings.Split(v, "/")
							} else {
								dimensionValOpt.dimensionValues[v] = struct{}{}
							}
						}
					}
				default:
					logging.Errorf("unknown dimension_values type: %T, value: %#v", value, value)
				}
			}
		}
	}
	return dimensionValOpt
}

// getMetricNameRegexOpt 获取该数据源结果表指标名该匹配的正则表达式
func getMetricNameRegexOpt(pipelineConfig *config.PipelineConfig) string {
	metricNameRegex := ""
	for _, table := range pipelineConfig.ResultTableList {
		if table.Option != nil {
			if value, ok := table.Option[metricNameRegexOpt]; ok {
				switch val := value.(type) {
				case string:
					metricNameRegex = val
				default:
					logging.Errorf("unknown metric_name_regex type: %T, values: %#v", value, value)
				}
			}
		}
	}
	return metricNameRegex
}

func newMetricsReportProcessor(ctx context.Context, name string) *MetricsReportProcessor {
	pipelineConfig := config.PipelineConfigFromContext(ctx)
	// 优先使用数据源结果表的正则配置
	if expr := getMetricNameRegexOpt(pipelineConfig); expr != "" {
		p, err := regexp.Compile(expr)
		if err == nil {
			metricNamePattern = p
		}
	}

	processor := &MetricsReportProcessor{
		ctx:               ctx,
		dataid:            pipelineConfig.DataID,
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, pipelineConfig),
		redisMetricKey:    redisMetricKeyPrefix + strconv.Itoa(pipelineConfig.DataID),
		redisDimensionKey: redisDimensionPrefix + strconv.Itoa(pipelineConfig.DataID),
		dimensionOpt:      getDimensionValuesOpt(pipelineConfig),
		dimensionUpdated:  make(chan struct{}, 1),
		metricStore:       make(map[string]float64),
		dimensionStore:    NewDimensionStore(),
	}
	return processor
}

// NewMetricsReportProcessor :
func NewMetricsReportProcessor(ctx context.Context, name string) (*MetricsReportProcessor, error) {
	processor := newMetricsReportProcessor(ctx, name)
	if err := processor.init(); err != nil {
		return nil, err
	}
	return processor, nil
}

func init() {
	define.RegisterDataProcessor("metrics_reporter", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipe := config.PipelineConfigFromContext(ctx)
		if pipe == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		rt := config.ResultTableConfigFromContext(ctx)
		if rt == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table is empty")
		}
		if config.FromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "config is empty")
		}
		return NewMetricsReportProcessor(ctx, pipe.FormatName(rt.FormatName(name)))
	})
}
