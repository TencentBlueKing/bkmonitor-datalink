// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// errScheduleExpired 说明该 once_at 计划在配置到达本机之前就已经过期。此时不再
// 采集，但仍然上报一条终态 Event，避免执行历史留下无法解释的空白。
const errScheduleExpired = "the once_at schedule had already passed when Beat loaded this configuration"

// Gather 是计划任务在常驻 Beat 中的入口。once_at 与 cron 的 started 和 finished
// 都由它上报；立即执行走轻量 CLI，不经过这里。
type Gather struct {
	tasks.BaseTask

	// running 拦住与前一轮重叠的触发。调度器在上一轮结束前就会启动下一轮，而两轮
	// 共用同一个 Gather 实例：BaseTask 的 PreRun/PostRun 会无锁修改 Status 与并发
	// 限制字段，重叠调用本身就是数据竞争。Execute 里的文件锁虽然能阻止第二次采集，
	// 但它在 PreRun 之后才取，竞争此时已经发生，因此必须在这里先拦。
	running atomic.Bool
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()
	return gather
}

// GracefulReload keeps an already started profiling run alive while reload
// removes its old future schedule immediately.
func (g *Gather) GracefulReload() bool {
	return true
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	conf, ok := g.TaskConfig.(*configs.ActiveProfileTaskConfig)
	if !ok {
		logger.Errorf("[active_profile] unexpected task config type %T", g.TaskConfig)
		return
	}

	// 必须在 PreRun 之前判断，原因见 Gather.running。冲突轮次不排队，直接以 failed
	// 收敛，与 Execute 中抢文件锁失败的行为一致。
	if !g.running.CompareAndSwap(false, true) {
		g.reportBusyRound(ctx, conf, e)
		return
	}
	defer g.running.Store(false)

	g.PreRun(ctx)
	defer g.PostRun(ctx)

	scheduledAt, ok := define.ScheduledAtFromContext(ctx)
	if !ok {
		scheduledAt = conf.ScheduledAt(time.Now())
	}
	opts := ExecuteOptions{
		ReportStarted:  true,
		FallbackIP:     g.fallbackIP(),
		Sink:           beatEventSink{events: e},
		MetricSink:     e,
		GatherUpDataID: g.GlobalConfig.GetGatherUpDataID(),
	}

	if define.ScheduleExpiredFromContext(ctx) {
		g.reportExpiredSchedule(ctx, conf, scheduledAt, opts)
		return
	}
	Execute(ctx, conf, scheduledAt, opts)
}

// reportBusyRound 处理与前一轮重叠的触发。不排队、不采集、不发 started，只上报一条
// 带忙碌原因的 failed finished，使执行历史不会出现无法解释的空白。
func (g *Gather) reportBusyRound(
	ctx context.Context,
	conf *configs.ActiveProfileTaskConfig,
	e chan<- define.Event,
) {
	scheduledAt, ok := define.ScheduledAtFromContext(ctx)
	if !ok {
		scheduledAt = conf.ScheduledAt(time.Now())
	}
	opts := ExecuteOptions{
		FallbackIP:     g.fallbackIP(),
		Sink:           beatEventSink{events: e},
		MetricSink:     e,
		GatherUpDataID: g.GlobalConfig.GetGatherUpDataID(),
	}
	logger.Warnf("[active_profile] task_id=%d skipped: %s", conf.GetTaskID(), errTaskAlreadyRunning)
	startedAt := time.Now()
	finish(ctx, conf, resolveExecutionIP(opts.FallbackIP), scheduledAt, startedAt, opts, ExecutionOutcome{
		Status:       StatusFailed,
		ErrorType:    ErrorTypeTaskBusy,
		ErrorMessage: errTaskAlreadyRunning,
	})
}

// reportExpiredSchedule 只上报 finished。started 表示"已经开始采集"，过期计划
// 从未开始，因此不发送。
func (g *Gather) reportExpiredSchedule(
	ctx context.Context,
	conf *configs.ActiveProfileTaskConfig,
	scheduledAt time.Time,
	opts ExecuteOptions,
) {
	logger.Errorf(
		"[active_profile] task_id=%d scheduled_at=%d skipped: %s",
		conf.GetTaskID(),
		scheduledAt.UnixMilli(),
		errScheduleExpired,
	)
	opts.ReportStarted = false
	finish(ctx, conf, resolveExecutionIP(opts.FallbackIP), scheduledAt, scheduledAt, opts, ExecutionOutcome{
		Status:       StatusFailed,
		ErrorType:    ErrorTypeScheduleExpired,
		ErrorMessage: errScheduleExpired,
	})
}

func (g *Gather) fallbackIP() string {
	if globalConfig, ok := g.GetGlobalConfig().(*configs.Config); ok {
		return globalConfig.IP
	}
	return ""
}
