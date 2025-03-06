// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricbeat

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/metricbeat/mb"
	"github.com/elastic/beats/metricbeat/mb/module"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/metricbeat/include" // 初始化 bkmetricbeats 组件
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	metricKey             = "prometheus.collector.metrics"
	metricReaderKey       = "prometheus.collector.metrics_reader"
	tempFilePatternFormat = "metricset_metrics_%s_*.list"
)

type Tool struct {
	module          *module.Wrapper
	mbConfig        *configs.MetricBeatConfig
	tempFilePattern string

	globalConf define.Config
	taskConf   define.TaskConfig
}

// Init 初始化参数 处理配置文件中的 modules
func (t *Tool) Init(mbConfig *configs.MetricBeatConfig, globalConf define.Config, taskConf define.TaskConfig) error {
	// 补充 period 把调度直接交给 Gather
	t.tempFilePattern = fmt.Sprintf(tempFilePatternFormat, mbConfig.GetIdent())
	err := mbConfig.Module.Merge(common.MapStr{
		"period":            mbConfig.Period,
		"temp_file_pattern": t.tempFilePattern,
		"workers":           mbConfig.Workers,
	})
	if err != nil {
		return errors.Wrap(err, "merge modules failed")
	}
	modules, err := module.NewWrapper(mbConfig.Module, mb.Registry)
	if err != nil {
		return errors.Wrap(err, "get modules failed")
	}

	logger.Infof("modules.Config: %+v", modules.Config())
	t.module = modules
	t.mbConfig = mbConfig
	t.globalConf = globalConf
	t.taskConf = taskConf
	return nil
}

func splitBigMetricsFromReader(m common.MapStr, batchsize, maxBatches int, ret chan<- common.MapStr) {
	defer close(ret)

	fn, err := m.GetValue(metricReaderKey)
	if err != nil {
		logger.Errorf("get reader failed: %v", err)
		return
	}
	metricsReader, ok := fn.(define.MetricsReaderFunc)
	if !ok {
		logger.Errorf("as reader failed, got %T", fn)
		return
	}
	metricsChan, err := metricsReader()
	if err != nil {
		logger.Errorf("failed to get metricsChan: %v", err)
		return
	}

	eventList := make([]common.MapStr, 0, batchsize)
	batches := 0
	total := 0

	var drain bool
	for events := range metricsChan {
		if drain {
			// 排空 channel
			continue
		}

		for i := 0; i < len(events); i++ {
			event := events[i]
			eventList = append(eventList, event)
			if len(eventList) >= batchsize {
				cloned := m.Clone()
				if _, err = cloned.Put(metricKey, eventList); err != nil {
					logger.Errorf("failed to put '%s' key: %v", metricKey, err)
					continue
				}
				err = cloned.Delete(metricReaderKey)
				if err != nil {
					logger.Errorf("failed to delete '%s' key: %v", metricReaderKey, err)
					continue
				}

				ret <- cloned
				total += len(eventList)
				batches++

				// 清空当前批次
				eventList = make([]common.MapStr, 0, batchsize)
				if batches >= maxBatches {
					logger.Errorf("metric batches reached max batches: %d", maxBatches)
					drain = true // 如果已经超过了最大批次 则需要丢弃接下来的其他数据
				}
			}
		}
	}

	// 处理批次剩余事件
	if len(eventList) > 0 {
		cloned := m.Clone()
		if _, err = cloned.Put(metricKey, eventList); err != nil {
			logger.Errorf("failed to put '%s' key: %v", metricKey, err)
			return
		}
		err = cloned.Delete(metricReaderKey)
		if err != nil {
			logger.Errorf("failed to delete '%s' key: %v", metricReaderKey, err)
			return
		}
		ret <- cloned
		total += len(eventList)
	}
	logger.Infof("get events from channel: %d", total)
}

func splitBigMetricsFromSlice(m common.MapStr, batchsize int, maxBatches int, ret chan common.MapStr) {
	defer close(ret)

	// 无 metrics key 或者非切片类型直接返回 只对 `prometheus.collector.metrics` key 做改动 不入侵其他逻辑
	i, err := m.GetValue(metricKey)
	if err != nil {
		return
	}
	lst, ok := i.([]common.MapStr)
	if !ok {
		return
	}
	logger.Infof("get events from slice: %d", len(lst))

	total := len(lst)
	batches := 0
	// 按批组装
	for i := 0; i < (total/batchsize)+1; i++ {
		left, right := i*batchsize, (i+1)*batchsize

		// 边界修正
		if left >= total {
			continue
		}
		if right > total {
			right = total
		}

		cloned := m.Clone()
		if _, err := cloned.Put(metricKey, lst[left:right]); err != nil {
			logger.Errorf("failed to put '%s' key: %v", metricKey, err)
			continue
		}

		ret <- cloned
		batches++
		if batches >= maxBatches {
			logger.Errorf("metric batches reached max batches: %d", maxBatches)
			break
		}
	}
}

// splitBigMetrics 拆分 prometheus 上报采集的大指标
func splitBigMetrics(m common.MapStr, batchsize int, maxBatches int) <-chan common.MapStr {
	ret := make(chan common.MapStr)
	if ok, err := m.HasKey(metricReaderKey); err == nil && ok {
		// 通过 channel 返回的指标列表
		go splitBigMetricsFromReader(m, batchsize, maxBatches, ret)
	} else {
		// 直接返回的指标列表
		go splitBigMetricsFromSlice(m, batchsize, maxBatches, ret)
	}

	return ret
}

func alignTs(period, nowSecs int) int {
	if period <= 0 {
		period = 1
	}

	n := period - (nowSecs % period)
	if n >= 60 || n == period {
		return 0 // 超过 1min 的就没有对齐的必要了
	}
	return n
}

func (t *Tool) waitTsAlign() {
	n := alignTs(int(t.mbConfig.Period.Seconds()), time.Now().Second())
	if n <= 0 {
		return
	}
	time.Sleep(time.Duration(n) * time.Second)
}

func (t *Tool) waitTsRandom() {
	mod := time.Now().Nanosecond() % int(t.mbConfig.Period.Seconds())
	if mod > 0 {
		mod = mod / 2
		time.Sleep(time.Duration(mod+1) * time.Second)
	}
}

func (t *Tool) Run(ctx context.Context, e chan<- define.Event) error {
	switch {
	case t.mbConfig.EnableAlignTs:
		t.waitTsAlign() // 采集时刻对齐
	case t.mbConfig.SpreadWorkload:
		t.waitTsRandom() // 随机打散任务
	}

	keepOneDimension := false
	var batchsize int
	var maxBatches int

	gConfig, ok := ctx.Value("gConfig").(*configs.Config)
	if !ok {
		logger.Error("get global config failed")
	} else {
		keepOneDimension = gConfig.KeepOneDimension
		batchsize = gConfig.MetricsBatchSize
		maxBatches = gConfig.MaxMetricBatches
	}

	// 设置 batchsize 缺省值
	if batchsize <= 0 {
		batchsize = 1024
	}
	if maxBatches <= 0 {
		maxBatches = 5000
	}

	mo := t.module
	evChan := mo.Start(ctx.Done())
	defer func() {
		err0 := utils.ClearTempFile(t.tempFilePattern)
		if err0 != nil {
			logger.Errorf("clear temp file failed: %v", err0)
		}
	}()

	// ctx.Done() 触发后 evChan 将关闭 循环结束
	for evc := range evChan {
		// Compat: v1 版本 elastic/beats 返回的是 MapStr 而 v2 版本返回的是 Event 对象
		// 这里需要做一个转换
		ev := common.MapStr{}
		ev.Update(evc.Meta)
		ev.Update(evc.Fields)
		ev.Put("dataid", t.mbConfig.DataID)

		// 是个坑 一定要 UTC 时间
		ev.Put("@timestamp", evc.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"))

		if keepOneDimension {
			t.KeepOneDimension(t.module.Name(), ev)
		}

		var total int
		splitMetrics := splitBigMetrics(ev, batchsize, maxBatches)
		for v := range splitMetrics {
			event := tasks.NewMetricEvent(t.mbConfig)
			event.Data = v
			total++
			// 启动自定义上报时，按自定义上报格式发送数据
			if t.mbConfig.CustomReport {
				event.DataID = t.mbConfig.DataID
				e <- &tasks.CustomMetricEvent{
					MetricEvent: event,
					Timestamp:   evc.Timestamp.Unix(),
				}
			} else {
				e <- event
			}
		}

		total++ // gather_up 本身也是一个数据包
		if configs.IsContainerMode() {
			e <- tasks.NewGatherUpEventWithConfig(t.taskConf.GetDataID(), t.taskConf, define.CodeOK, nil, float64(total))
		} else {
			// 兼容二进制环境数据 使用内置 dataid
			e <- tasks.NewGatherUpEventWithConfig(t.globalConf.GetGatherUpDataID(), t.taskConf, define.CodeOK, nil, float64(total))
		}
	}

	logger.Infof("metric task evChan exit, module: %s", mo.String())
	return nil
}

// KeepOneDimension 只在测试模式 && Prometheus场景需要这么处理
// 指标名+维度字段名 作为唯一的key
// 不同维度值只保留一个，但是如果有多的维度名，那么需要保留
func (t *Tool) KeepOneDimension(name string, data common.MapStr) {
	if name != "prometheus" {
		return
	}

	val, err := data.GetValue(name)
	if err != nil {
		logger.Warnf("tool get data failed: %v", err)
		return
	}

	moduleData, ok := val.(common.MapStr)
	if !ok {
		return
	}

	collector, err := moduleData.GetValue("collector")
	if err != nil {
		logger.Warnf("tool get collector data failed: %v", err)
		return
	}

	collectorData, ok := collector.(common.MapStr)
	if !ok {
		return
	}

	metrics, err := collectorData.GetValue("metrics")
	if err != nil {
		logger.Warnf("tool get collector metrics data failed: %v", err)
		return
	}

	oldMetrics, ok := metrics.([]common.MapStr)
	if !ok {
		return
	}

	metricNameSet := common.StringSet{}
	newMetrics := make([]common.MapStr, 0)
	for _, m := range oldMetrics {
		key, err := m.GetValue("key")
		if err != nil {
			continue
		}

		k, ok := key.(string)
		if !ok {
			continue
		}

		labels, err := m.GetValue("labels")
		if err != nil {
			continue
		}

		dimensions, ok := labels.(common.MapStr)
		if !ok {
			continue
		}

		dimFieldNames := make([]string, 0)
		for dimK := range dimensions {
			dimFieldNames = append(dimFieldNames, dimK)
		}
		dimFieldNames = append(dimFieldNames, k)
		sort.Strings(dimFieldNames)
		hashKey := utils.GeneratorHashKey(dimFieldNames)

		if !metricNameSet.Has(hashKey) {
			metricNameSet.Add(hashKey)
			newMetrics = append(newMetrics, m)
		}
	}
	collectorData["metrics"] = newMetrics
}
