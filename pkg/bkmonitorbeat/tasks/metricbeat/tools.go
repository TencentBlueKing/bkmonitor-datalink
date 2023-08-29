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
	"strconv"

	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/metricbeat/mb"
	"github.com/elastic/beats/metricbeat/mb/module"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/metricbeat/include" // 初始化 bkmetricbeats 组件
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	metricKey             = "prometheus.collector.metrics"
	metricReaderKey       = "prometheus.collector.metrics_reader"
	tempFilePatternFormat = "metricset_metrics_%s_*.list"
)

// MetricTool metricbeat接口
type MetricTool interface {
	Init(taskConf *configs.MetricBeatConfig, globalConf *configs.Config) error
	Run(ctx context.Context, e chan<- define.Event) error
}

// BKMetricbeatTool 蓝鲸修改后的metricbeat移植
type BKMetricbeatTool struct {
	module          *module.Wrapper
	taskConf        *configs.MetricBeatConfig
	tempFilePattern string
	globalConf      *configs.Config
}

// Init 初始化参数，主要处理配置文件中的modules
func (t *BKMetricbeatTool) Init(taskConf *configs.MetricBeatConfig, globalConf *configs.Config) error {
	// 补充 period 把调度直接交给 Gather
	t.tempFilePattern = fmt.Sprintf(tempFilePatternFormat, taskConf.GetIdent())
	err := taskConf.Module.Merge(common.MapStr{
		"period":            taskConf.Period,
		"temp_file_pattern": t.tempFilePattern,
		"workers":           taskConf.Workers,
	})
	if err != nil {
		logger.Errorf("merge modules failed, error: %v", err)
		return err
	}
	modules, err := module.NewWrapper(taskConf.Module, mb.Registry)
	if err != nil {
		logger.Errorf("get modules failed, error: %v", err)
		return err
	}

	logger.Infof("modules.Config: %+v", modules.Config())
	t.module = modules
	t.taskConf = taskConf
	t.globalConf = globalConf
	return nil
}

func splitBigMetricsFromReader(m common.MapStr, batchsize, maxBatches int, ret chan<- common.MapStr) {
	defer close(ret)
	metricsReaderInterface, err := m.GetValue(metricReaderKey)
	if err != nil {
		logger.Errorf("no metricChannelKey [%v]", m)
		return
	}
	metricsReader, ok := metricsReaderInterface.(define.MetricsReaderFunc)
	if !ok {
		logger.Errorf("metricChannelKey not chan [%v]", metricsReaderInterface)
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
	// 按批组装
	for event := range metricsChan {
		eventList = append(eventList, event)
		// 达到批次数量
		if len(eventList) >= batchsize {
			cloned := m.Clone()
			if _, err = cloned.Put(metricKey, eventList); err != nil {
				logger.Errorf("failed to put prometheus.collector.metrics key: %v", err)
				continue
			}
			err = cloned.Delete(metricReaderKey)
			if err != nil {
				logger.Errorf("failed to delete prometheus.collector.metrics_reader key: %v", err)
				continue
			}
			logger.Debugf("sent eventList: %d", len(eventList))
			ret <- cloned
			// 计数
			total += len(eventList)
			batches++
			// 清空当前批次
			eventList = make([]common.MapStr, 0, batchsize)
			if batches >= maxBatches {
				logger.Warnf("metric batches reached max batches:%d, will not report more data in this task", maxBatches)
				break
			}
		}
	}
	if len(eventList) > 0 {
		// 处理批次剩余事件
		cloned := m.Clone()
		if _, err = cloned.Put(metricKey, eventList); err != nil {
			logger.Errorf("failed to put prometheus.collector.metrics key: %v", err)
			return
		}
		err = cloned.Delete(metricReaderKey)
		if err != nil {
			logger.Errorf("failed to delete prometheus.collector.metrics_reader key: %v", err)
			return
		}
		logger.Debugf("sent eventList: %d", len(eventList))
		ret <- cloned
		total += len(eventList)
	}
	logger.Infof("get events from channel %d", total)
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
	logger.Debugf("splitBigMetricsFromSlice %d", len(lst))
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
			logger.Errorf("failed to put prometheus.collector.metrics key: %v", err)
			continue
		}

		ret <- cloned
		batches++
		if batches >= maxBatches {
			logger.Warnf("metric batches reached max batches:%d,will not report more data in this task", maxBatches)
			break
		}
	}
}

// splitBigMetrics 拆分 prometheus 上报采集的大指标
func (t *BKMetricbeatTool) splitBigMetrics(m common.MapStr, batchsize int, maxBatches int) <-chan common.MapStr {
	ret := make(chan common.MapStr)
	if ok, err := m.HasKey(metricReaderKey); err == nil && ok {
		// 通过channel返回的指标列表
		logger.Debug("splitBigMetricsFromReader")
		go splitBigMetricsFromReader(m, batchsize, maxBatches, ret)
	} else {
		// 直接返回的指标列表
		logger.Debug("splitBigMetricsFromSlice")
		go splitBigMetricsFromSlice(m, batchsize, maxBatches, ret)
	}

	return ret
}

// Run 使用bkmetricbeat原有逻辑执行任务
func (t *BKMetricbeatTool) Run(ctx context.Context, e chan<- define.Event) error {
	logger.Debug("BKMetricbeatTool is running...")
	keepOneDimension := false
	var batchsize int
	var maxBatches int

	globalConfig, ok := ctx.Value("gConfig").(*configs.Config)
	if !ok {
		logger.Error("get global config in bkmetricbeat running failed.")
	} else {
		keepOneDimension = globalConfig.KeepOneDimension
		batchsize = globalConfig.MetricsBatchSize
		maxBatches = globalConfig.MaxMetricBatches
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
			logger.Error("clear temp file failed: %v", err0)
		}
	}()
loop:
	for evc := range evChan {
		logger.Debugf("receviced event:%v", evc)
		// Compat: v1 版本 elastic/beats 返回的是 MapStr 而 v2 版本返回的是 Event 对象
		// 这里需要做一个转换
		ev := common.MapStr{}
		ev.Update(evc.Meta)
		ev.Update(evc.Fields)
		ev.Put("dataid", t.taskConf.DataID)

		// 是个坑 一定要 UTC 时间
		ev.Put("@timestamp", evc.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"))

		if keepOneDimension {
			t.KeepOneDimension(t.module.Name(), ev)
		}

		splitedMetrics := t.splitBigMetrics(ev, batchsize, maxBatches)

		for v := range splitedMetrics {
			var sendEvent define.Event
			event := tasks.NewMetricEvent(t.taskConf)
			logger.Debugf("data_id: %d, labels: %#v", t.taskConf.DataID, t.taskConf.GetLabels())

			event.Data = v
			// 仅二进制采集环境下，提取状态指标整理，整理成专属格式和DataID，进行分发
			if !beat.IsContainerMode() {
				upEvent := t.SendGatherUp(v)
				if upEvent != nil {
					e <- upEvent
				}
			}

			// 启动自定义上报时，按自定义上报格式发送数据
			if t.taskConf.CustomReport {
				logger.Debugf("dataid:%d use custom report format metricbeat event", t.taskConf.DataID)
				event.DataID = t.taskConf.DataID
				sendEvent = &tasks.CustomMetricEvent{
					MetricEvent: event,
					Timestamp:   evc.Timestamp.Unix(),
				}
			} else {
				logger.Debugf("dataid:%d use normal format metricbeat event", t.taskConf.DataID)
				sendEvent = event
			}

			select {
			case <-ctx.Done():
				logger.Info("metric task get ctx done")
				break loop
			case e <- sendEvent:
				logger.Debug("send metricbeat event")
			}
		}
	}

	logger.Infof("metric task evChan exit,module:%v", mo.String())
	return nil
}

// KeepOneDimension 只在测试模式 && Prometheus场景需要这么处理
// 指标名+维度字段名 作为唯一的key
// 不同维度值只保留一个，但是如果有多的维度名，那么需要保留
//
//	 "prometheus":{
//	    "collector":{
//	        "metrics":[
//	            {
//	                "key":"go_gc_duration_seconds",
//	                "labels":{
//	                    "quantile":"0"
//	                },
//	                "value":0
//	            },
//	            {
//	                "key":"go_gc_duration_seconds_sum",
//	                "labels":{
//
//	                },
//	                "value":0
//	            }
//	        ],
//	        "namespace":"bond_example"
//	    }
//	}
func (t *BKMetricbeatTool) KeepOneDimension(name string, data common.MapStr) {
	if name != "prometheus" {
		return
	}

	val, err := data.GetValue(name)
	if err != nil {
		logger.Warnf("get module(%s) data err=>(%v)", name, err)
		return
	}

	moduleData, ok := val.(common.MapStr)
	if !ok {
		return
	}

	collector, err := moduleData.GetValue("collector")
	if err != nil {
		logger.Warnf("get module(%s) collector data err=>(%v)", name, err)
		return
	}

	collectorData, ok := collector.(common.MapStr)
	if !ok {
		return
	}

	metrics, err := collectorData.GetValue("metrics")
	if err != nil {
		logger.Warnf("get module(%s) collector metrics data err=>(%v)", name, err)
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
	logger.Debugf("old metrics(%v), \n new metrics(%v)", oldMetrics, newMetrics)
}

// SendGatherUp 从 Beat 事件中提取状态信息
func (t *BKMetricbeatTool) SendGatherUp(evc common.MapStr) define.Event {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("Panic in SendGatherUp: %v", r)
		}
	}()

	metricsVal, err := evc.GetValue("prometheus.collector.metrics")
	if err != nil {
		logger.Errorf("KeyNotFound(prometheus.collector.metrics) in metricbeat: %v", err)
	}
	metrics := metricsVal.([]common.MapStr)
	for _, m := range metrics {
		if m["key"] == "bkm_metricbeat_endpoint_up" {
			code := m["labels"].(common.MapStr)["code"].(string)
			codeNum, _ := strconv.ParseInt(code, 10, 32)
			return tasks.NewGatherUpEventWithConfig(t.taskConf, t.globalConf, define.BeatErrorCode(codeNum), nil)
		}
	}
	return nil
}
