// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package script

import (
	"context"
	"sort"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	bkcommon "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ExecCmdLine is so tests can mock out exec.Command usage.
var ExecCmdLine = utils.RunStringWithoutErr

// Gather gather script task output
type Gather struct {
	tasks.BaseTask
}

// Run run script command line and parse result as promuthes format
func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	var (
		err         error
		taskConf    = g.TaskConfig.(*configs.ScriptTaskConfig)
		originEvent = NewEvent(g)
	)
	//init event time
	localtime, utctime, _ := bkcommon.GetDateTime()
	originEvent.LocalTime = localtime
	originEvent.UTCTime = utctime

	g.PreRun(ctx)
	defer g.PostRun(ctx)

	// 生成当前时间戳和时间处理函数
	milliTimestamp := time.Now().UnixMilli()
	timeHandler, err := tasks.GetTimestampHandler(taskConf.TimestampUnit)
	if timeHandler == nil {
		timeHandler, _ = tasks.GetTimestampHandler("ms")
	}
	if err != nil {
		logger.Errorf("use timestamp unit:%s to get timestamp handler failed, %s", taskConf.TimestampUnit, err)
		e <- tasks.NewGatherUpEvent(g, define.BeatErrScriptTsUnitConfigError)
		return
	}

	logger.Infof("task command %s timeout config %v", taskConf.Command, taskConf.Timeout)
	cmdCtx, cmdCancel := context.WithTimeout(ctx, taskConf.Timeout)
	// releases resources if execCmd completes before timeout elapses
	defer cmdCancel()
	fmtCommand := ShellWordPreProcess(taskConf.Command)
	logger.Debugf("start to run command line [%s]", taskConf.Command)

	t0 := time.Now()
	out, err := ExecCmdLine(cmdCtx, fmtCommand, taskConf.UserEnvs)
	if err != nil {
		logger.Errorf("execCmd [%s] failed:%s, failed content:%s", fmtCommand, err.Error(), out)
		e <- tasks.NewGatherUpEvent(g, define.BeatScriptRunOuterError)
		return
	}
	logger.Infof("task-take: %v", time.Since(t0))
	logger.Debugf("run command line %s success", fmtCommand)

	aggreRst, formatErr := FormatOutput([]byte(out), milliTimestamp, taskConf.TimeOffset, timeHandler)

	gConfig, ok := g.GlobalConfig.(*configs.Config)
	if ok && gConfig.KeepOneDimension {
		// 清理aggreRst，对于相同的指标，只保留一个维度的数据
		g.KeepOneDimension(aggreRst)
	}

	for timestamp, subResult := range aggreRst {
		for _, pe := range subResult {
			ev := NewEvent(g)
			ev.StartAt = originEvent.StartAt
			ev.Timestamp = timestamp
			ev.LocalTime = originEvent.LocalTime
			ev.UTCTime = originEvent.UTCTime

			ev.UserTime = time.Unix(ev.Timestamp, 0).UTC().Format(bkcommon.TimeFormat)
			for aggKey, aggValue := range pe.AggreValue {
				ev.Metric[aggKey] = aggValue
			}
			if len(pe.Labels) > 0 {
				for k, v := range pe.Labels {
					ev.Dimension[k] = v
				}
			}

			if pe.Exemplar != nil && pe.Exemplar.Ts > 0 {
				exemplarLbs := make(map[string]string)
				for _, pair := range pe.Exemplar.Labels {
					exemplarLbs[pair.Name] = pair.Value
				}

				// 允许只提供 traceID 或者只提供 spanID
				tmp := common.MapStr{}
				traceID, spanID := tasks.MatchTraces(exemplarLbs)
				if traceID != "" {
					tmp["bk_trace_id"] = traceID
				}
				if spanID != "" {
					tmp["bk_span_id"] = spanID
				}
				if len(tmp) > 0 {
					tmp["bk_trace_timestamp"] = pe.Exemplar.Ts
					tmp["bk_trace_value"] = pe.Exemplar.Value
					ev.Exemplar = tmp
				}
			}

			ev.Success()
			logger.Infof("event:%+v", ev)
			e <- ev
		}
	}

	if formatErr != nil {
		e <- tasks.NewGatherUpEvent(g, define.BeatScriptPromFormatOuterError)
		if len(aggreRst) == 0 {
			logger.Errorf("format output failed totally: %s", formatErr.Error())
		} else {
			logger.Errorf("format output failed partly: %s", formatErr.Error())
		}
	} else {
		e <- tasks.NewGatherUpEvent(g, define.BeatErrCodeOK)
		logger.Debugf("format command line %s result success", fmtCommand)
	}
}

// KeepOneDimension 只在测试模式需要这么处理
// 指标名+维度字段名 作为唯一的key
// 不同维度值只保留一个，但是如果有多的维度名，那么需要保留，详细可以看test里的案例
func (g *Gather) KeepOneDimension(data map[int64]map[string]tasks.PromEvent) {
	logger.Infof("[script collect] keep one dimension %v", data)
	for timestamp, subResult := range data {
		keySet := common.StringSet{}
		newSubResult := make(map[string]tasks.PromEvent)
		for dimensionKey, pe := range subResult {
			// 清理部分指标，当前面的维度已经包含了某个指标后，那么接下来的维度里，则删除这个指标
			lenOfdimensionNames := len(pe.Labels)
			dimFieldNames := make([]string, 0)
			for dimK := range pe.Labels {
				dimFieldNames = append(dimFieldNames, dimK)
			}
			sort.Strings(dimFieldNames)
			dimFieldNames = append(dimFieldNames, "") // 先占个空位

			newAggreValue := make(common.MapStr)
			for aggKey, aggValue := range pe.AggreValue {
				dimFieldNames[lenOfdimensionNames] = aggKey
				hashKey := utils.GeneratorHashKey(dimFieldNames)
				if !keySet.Has(hashKey) {
					keySet.Add(hashKey)
					newAggreValue[aggKey] = aggValue
				}
			}
			pe.AggreValue = newAggreValue

			// 如果该维度下的还有指标未被清理。则保留这个维度的数据
			if len(newAggreValue) > 0 {
				newSubResult[dimensionKey] = pe
			}
		}

		// 将保留下来的指标数据重新赋值回去
		data[timestamp] = newSubResult
	}
}

// New :
func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig

	gather.Init()

	return gather
}
