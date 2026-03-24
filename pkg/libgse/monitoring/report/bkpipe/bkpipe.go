// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkpipe

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/elastic/beats/libbeat/monitoring"
	"github.com/elastic/beats/libbeat/monitoring/report"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/gse"
)

var (
	taskPrefix    string = "bkbeat_tasks"
	taskPrefixLen int    = len("bkbeat_tasks")
	version       string = "v2"
)

// DataQualityMonitorSender :
// dataid is default monitor dataid, pass to caller
// data is summary data
type DataQualityMonitorSender interface {
	Report(dataid int32, data common.MapStr) error
}

// 采集器指标发送
type bkpipeSender struct {
	sender DataQualityMonitorSender
	target string
}

// 采集器指标发送实例
var (
	Sender    *bkpipeSender
	agentInfo gse.AgentInfo
)

func GetAgentInfo() gse.AgentInfo {
	return agentInfo
}

// InitSender
func InitSender(sender DataQualityMonitorSender, info gse.AgentInfo) {
	if Sender == nil {
		logp.Info("init with target => %d:%s", info.Cloudid, info.IP)
		Sender = &bkpipeSender{
			sender: sender,
			target: fmt.Sprintf("%d:%s", info.Cloudid, info.IP),
		}
		agentInfo = info
	}
}

// Report
func (s *bkpipeSender) Report(dataid int32, event common.MapStr) error {
	event["target"] = s.target
	return s.sender.Report(dataid, event)
}

// List of metrics that are gauges. This is used to identify metrics that should
// not be reported as deltas. Instead we log the raw value if there was any
// observable change during the interval.
//
// TODO: Replace this with a proper solution that uses the metric type from
// where it is defined. See: https://github.com/elastic/beats/issues/5433
var gauges = map[string]bool{
	"libbeat.pipeline.events.active": true,
	"libbeat.pipeline.clients":       true,
	"libbeat.config.module.running":  true,
	"registrar.states.current":       true,
	"filebeat.harvester.running":     true,
	"filebeat.harvester.open_files":  true,
	"beat.memstats.memory_total":     true,
	"beat.memstats.memory_alloc":     true,
	"beat.memstats.gc_next":          true,
	"beat.memstats.rss":              true,
	"beat.info.uptime.ms":            true,
	"beat.cpu.user.ticks":            true,
	"beat.cpu.user.time":             true,
	"beat.cpu.system.ticks":          true,
	"beat.cpu.system.time":           true,
	"beat.cpu.total.value":           true,
	"beat.cpu.total.pct":             true,
	"beat.cpu.total.norm_pct":        true,
	"beat.cpu.total.ticks":           true,
	"beat.cpu.total.time":            true,
	"beat.handles.open":              true,
	"beat.handles.limit.hard":        true,
	"beat.handles.limit.soft":        true,
	"system.cpu.cores":               true,
	"system.load.1":                  true,
	"system.load.5":                  true,
	"system.load.15":                 true,
	"system.load.norm.1":             true,
	"system.load.norm.5":             true,
	"system.load.norm.15":            true,
}

// TODO: Change this when gauges are refactored, too.
var strConsts = map[string]bool{}

// StartTime is the time that the process was started.
var StartTime = time.Now()

// TaskMetric
type TaskMetric struct {
	DataID int
	Metric string
}

type reporter struct {
	wg           sync.WaitGroup
	done         chan struct{}
	bkBizID      int32
	dataID       int32
	taskDataID   int32
	k8sClusterID string
	k8sNodeName  string
	period       time.Duration
	registry     *monitoring.Registry
	beatName     string
	beatVersion  string
	bkCloudId    int
	ip           string
	extraLabels  map[string]string
}

func init() {
	report.RegisterReporterFactory("bkpipe", makeReporter)
}

// makeReporter returns a new Reporter that periodically reports metrics via
// logp. If cfg is nil defaults will be used.
func makeReporter(beat beat.Info, settings report.Settings, cfg *common.Config) (report.Reporter, error) {
	config := defaultConfig
	if cfg != nil {
		if err := cfg.Unpack(&config); err != nil {
			return nil, err
		}
	}

	extraLabels := make(map[string]string)
	for _, el := range config.ExtraLabels {
		for k, v := range el.Load() {
			extraLabels[k] = v
		}
	}

	reporter := &reporter{
		done:         make(chan struct{}),
		bkBizID:      config.BkBizID,
		dataID:       config.DataID,
		taskDataID:   config.TaskDataID,
		period:       config.Period,
		k8sClusterID: config.K8sClusterID,
		k8sNodeName:  config.K8sNodeName,
		registry:     monitoring.Default,
		beatName:     beat.Beat,
		beatVersion:  beat.Version,
		extraLabels:  extraLabels,
	}

	reporter.wg.Add(1)
	go func() {
		defer reporter.wg.Done()
		reporter.snapshotLoop()
	}()
	return reporter, nil
}

// Stop
func (r *reporter) Stop() {
	close(r.done)
	r.wg.Wait()
}

func (r *reporter) snapshotLoop() {
	logp.Info("Starting metrics logging every %v", r.period)

	var last monitoring.FlatSnapshot
	ticker := time.NewTicker(r.period)

	defer func() {
		ticker.Stop()
		r.sendMetrics(r.makeDeltaSnapshot(last, makeSnapshot(r.registry)))
		logp.Info("Stopping metrics logging. Uptime: %v", time.Since(StartTime))
	}()

	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
		}

		cur := makeSnapshot(r.registry)
		delta := r.makeDeltaSnapshot(last, cur)
		last = cur

		r.sendMetrics(delta)
	}
}

func (r *reporter) sendMetrics(s monitoring.FlatSnapshot) {
	if snapshotLen(s) > 0 {
		metrics := getMetrics(s)
		var data map[string][]common.MapStr = map[string][]common.MapStr{
			"beat":  {},
			"tasks": {},
		}
		var key string

		bkBizID := r.bkBizID
		if r.bkBizID == 0 {
			// 默认使用主机身份的业务ID
			bkBizID = GetAgentInfo().BKBizID
		}

		for dataID, dataMetrics := range metrics {
			if dataID == 0 {
				key = "beat"
			} else {
				key = "tasks"
			}

			dimension := common.MapStr{
				"bk_biz_id":    bkBizID,
				"type":         r.beatName,
				"version":      r.beatVersion,
				"task_data_id": dataID,
			}

			if r.k8sClusterID != "" {
				dimension["k8s_cluster_id"] = r.k8sClusterID
			}

			if r.k8sNodeName != "" {
				dimension["k8s_node_name"] = r.k8sNodeName
			}

			for k, v := range r.extraLabels {
				if _, ok := dimension[k]; !ok {
					dimension[k] = v
				}
			}

			data[key] = append(data[key], common.MapStr{
				"metrics":   dataMetrics,
				"dimension": dimension,
			})
		}
		if Sender == nil {
			logp.Info("Non-zero metrics in the last %s %v", r.period.String(), data)
			return
		}

		// bkpipe
		var dataID int32
		for metricKey, metricList := range data {
			if metricKey == "beat" {
				dataID = r.dataID
			} else {
				dataID = r.taskDataID
			}
			event := common.MapStr{
				"version":   version,
				"timestamp": time.Now().UnixNano(),
			}
			event["data_id"] = dataID
			event["dataid"] = dataID
			event["data"] = metricList

			// 如果有配置对应的任务ID，则发送采集事件
			if dataID != 0 {
				err := Sender.Report(dataID, event)
				if err != nil {
					logp.Err("send metrics err => %v, event=>%v", err, event)
				}
			}
			logp.Info("Non-zero %s metrics in the last %s %v", metricKey, r.period.String(), event)
		}
		return
	}

	logp.Info("No non-zero metrics in the last %v", r.period)
}

func makeSnapshot(r *monitoring.Registry) monitoring.FlatSnapshot {
	mode := monitoring.Full
	return monitoring.CollectFlatSnapshot(r, mode, true)
}

func (r *reporter) makeDeltaSnapshot(prev, cur monitoring.FlatSnapshot) monitoring.FlatSnapshot {
	delta := monitoring.MakeFlatSnapshot()

	for k, b := range cur.Bools {
		if p, ok := prev.Bools[k]; !ok || p != b {
			delta.Bools[k] = b
		}
	}

	for k, s := range cur.Strings {
		if _, found := strConsts[k]; found {
			delta.Strings[k] = s
		} else if r.registry.IsGauge(k) {
			delta.Strings[k] = s
		} else if p, ok := prev.Strings[k]; !ok || p != s {
			delta.Strings[k] = s
		}
	}

	for k, i := range cur.Ints {
		if _, found := gauges[k]; found {
			delta.Ints[k] = i
		} else if r.registry.IsGauge(k) {
			delta.Ints[k] = i
		} else if p := prev.Ints[k]; p != i {
			delta.Ints[k] = i - p
		}
	}

	for k, f := range cur.Floats {
		if _, found := gauges[k]; found {
			delta.Floats[k] = f
		} else if r.registry.IsGauge(k) {
			delta.Floats[k] = f
		} else if p := prev.Floats[k]; p != f {
			delta.Floats[k] = f - p
		}
	}

	return delta
}

func snapshotLen(s monitoring.FlatSnapshot) int {
	return len(s.Bools) + len(s.Floats) + len(s.Ints) + len(s.Strings)
}

func getMetrics(s monitoring.FlatSnapshot) map[int]common.MapStr {
	data := make(map[int]common.MapStr)
	var taskMetric TaskMetric

	for k, v := range s.Bools {
		taskMetric = getTaskMetric(k)
		if _, ok := data[taskMetric.DataID]; !ok {
			data[taskMetric.DataID] = common.MapStr{}
		}
		data[taskMetric.DataID][taskMetric.Metric] = v
	}
	for k, v := range s.Floats {
		taskMetric = getTaskMetric(k)
		if _, ok := data[taskMetric.DataID]; !ok {
			data[taskMetric.DataID] = common.MapStr{}
		}
		data[taskMetric.DataID][taskMetric.Metric] = v
	}
	for k, v := range s.Ints {
		taskMetric = getTaskMetric(k)
		if _, ok := data[taskMetric.DataID]; !ok {
			data[taskMetric.DataID] = common.MapStr{}
		}
		data[taskMetric.DataID][taskMetric.Metric] = v
	}
	for k, v := range s.Strings {
		taskMetric = getTaskMetric(k)
		if _, ok := data[taskMetric.DataID]; !ok {
			data[taskMetric.DataID] = common.MapStr{}
		}
		data[taskMetric.DataID][taskMetric.Metric] = v
	}

	return data
}

func getTaskMetric(metric string) TaskMetric {
	data := TaskMetric{
		DataID: 0,
		Metric: metric,
	}

	if strings.HasPrefix(metric, taskPrefix) {
		metric = metric[taskPrefixLen+1:]
		metricPos := strings.Index(metric, ".")
		dataID, err := strconv.Atoi(metric[0:metricPos])
		if err != nil {
			dataID = 0
		}
		data.DataID = dataID
		data.Metric = metric[metricPos+1:]
	}
	data.Metric = strings.ReplaceAll(data.Metric, ".", "_")
	return data
}
