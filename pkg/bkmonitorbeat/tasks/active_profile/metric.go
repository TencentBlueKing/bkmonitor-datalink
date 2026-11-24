// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"strconv"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// metricActiveProfilingExecution 在主机执行得到终态后上报一次，用于按业务、任务、
// 主机和状态统计执行量与成功率。指标不携带执行聚合 ID、产物 ID 或错误文本，失败
// 原因从 Event 的 content.message 查询。
const metricActiveProfilingExecution = "bkm_active_profiling_execution"

// ExecutionMetricEvent 复用 Beat 内置 gather_up_beat 数据链路上报终态运营指标。
type ExecutionMetricEvent struct {
	DataID     int32
	Time       time.Time
	Dimensions common.MapStr
}

func (e *ExecutionMetricEvent) GetType() string { return define.ModuleActiveProfile }

func (e *ExecutionMetricEvent) IgnoreCMDBLevel() bool { return true }

func (e *ExecutionMetricEvent) AsMapStr() common.MapStr {
	return common.MapStr{
		"dataid": e.DataID,
		"data": []common.MapStr{{
			"metrics":   common.MapStr{metricActiveProfilingExecution: 1},
			"dimension": e.Dimensions,
			"timestamp": e.Time.UnixMilli(),
		}},
	}
}

// newExecutionMetricEvent 按技术基线要求的维度构造终态指标。指标时间使用主机终态
// 实际发生时间。
func newExecutionMetricEvent(dataID int32, event *FinishedEvent) *ExecutionMetricEvent {
	dimensions := common.MapStr{
		"bk_biz_id":  strconv.FormatInt(int64(event.BizID), 10),
		"bk_host_id": strconv.FormatInt(event.BKHostID, 10),
		"ip":         event.IP,
		"status":     event.Status,
		"task_id":    strconv.FormatInt(int64(event.TaskID), 10),
	}
	return &ExecutionMetricEvent{
		DataID:     dataID,
		Time:       event.FinishedAt,
		Dimensions: dimensions,
	}
}

// reportExecutionMetric 尽力上报终态指标。发送失败只记日志，不改变 Event 或采集
// 结果。sink 为 nil 时说明当前执行入口还没有接通内置指标链路。
func reportExecutionMetric(sink chan<- define.Event, dataID int32, event *FinishedEvent) {
	if dataID <= 0 {
		logger.Warnf("[active_profile] skip execution metric: gather up dataid is not configured")
		return
	}
	metric := newExecutionMetricEvent(dataID, event)
	if sink == nil {
		logger.Warnf(
			"[active_profile] execution metric is not delivered: no built-in metric link on this entrypoint, dimensions=%v",
			metric.Dimensions,
		)
		return
	}
	select {
	case sink <- metric:
	default:
		logger.Warnf("[active_profile] drop execution metric: event channel is full, dimensions=%v", metric.Dimensions)
	}
}
