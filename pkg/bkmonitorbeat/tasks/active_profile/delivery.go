// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

// Event 与 pprof 产物都经由本机 GSE Agent 上报，路由只靠平台注入的
// event_data_id / profile_data_id，SaaS 不下发上报地址或 Token。
//
// 两个入口的传输载体不同，载荷形态相同：
//   - 常驻 Beat 把上报事件交给 Beat 自己的事件通道，由 gse output 发送；
//   - 轻量 CLI 运行在 Beat 框架之外，自建 GSE 客户端直发（见 gseagent.go）。

package active_profile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/active_profile/executor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// errReportFailed 标记投递失败，finished 据此归为结果上报失败。
var errReportFailed = errors.New("report failed")

// reportTimeout 限制单次投递耗时。投递只是本机动作——交给 Beat 事件通道或写本机
// Agent socket，不涉及远端请求，因此不需要按产物大小放大。
const reportTimeout = 30 * time.Second

// reportSink 投递一条已经带 dataid 的上报事件。
type reportSink interface {
	Send(ctx context.Context, event define.Event) error
}

// beatEventSink 是常驻 Beat 的传输载体：事件进入 Beat 的事件通道，由 gse output
// 统一补上 Agent 信息并写本机 Agent。投递成功只表示已经入队，实际发送结果体现在
// Beat 自身的 gse 指标与日志里，不回写主机执行状态。
type beatEventSink struct {
	events chan<- define.Event
}

func (s beatEventSink) Send(ctx context.Context, event define.Event) error {
	if s.events == nil {
		return errors.New("the beat event link is not available on this entrypoint")
	}
	select {
	case s.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func reportStartedEvent(ctx context.Context, sink reportSink, event *StartedEvent) error {
	return sendReport(ctx, sink, event, "started event")
}

func reportFinishedEvent(ctx context.Context, sink reportSink, event *FinishedEvent) error {
	return sendReport(ctx, sink, event, "finished event")
}

func sendReport(ctx context.Context, sink reportSink, event define.Event, action string) error {
	if sink == nil {
		return executor.MarkError(errReportFailed, fmt.Errorf("%s is not reported: this entrypoint has no report link", action))
	}
	sendCtx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	if err := sink.Send(sendCtx, event); err != nil {
		return executor.MarkError(errReportFailed, fmt.Errorf("report %s: %w", action, err))
	}
	return nil
}

// ProfileEvent 是 pprof 产物的上报载荷。四个字段都是 Profiling 结果表清洗规则逐条
// 读取的键：app / biz_id / service_name 定位产物归属，type 取 pprof 的
// PeriodType.Type。少任何一个都会让整条记录清洗不出来、停在 Kafka 里不落库。
type ProfileEvent struct {
	DataID      int32
	BizID       int32
	AppName     string
	ServiceName string
	PeriodType  string
	// Profile 是未压缩的 pprof protobuf。JSON 编码为 base64 字符串，与持续
	// Profiling 链路交给 GSE 的形态一致。
	Profile []byte
}

func (e *ProfileEvent) AsMapStr() common.MapStr {
	return common.MapStr{
		"dataid": e.DataID,
		"data": []common.MapStr{{
			"data":         e.Profile,
			"app":          e.AppName,
			"biz_id":       e.BizID,
			"type":         e.PeriodType,
			"service_name": e.ServiceName,
		}},
	}
}

func (e *ProfileEvent) GetType() string { return define.ModuleActiveProfile }

func (e *ProfileEvent) IgnoreCMDBLevel() bool { return true }

// reportProfile 上报本次产物。产物载体由 Result 决定：perf 的在运行目录里，Golang
// 采集的在内存里；上报要求完整字节，因此两种载体都读成一份内存副本。
func reportProfile(
	ctx context.Context,
	sink reportSink,
	conf *configs.ActiveProfileTaskConfig,
	result *executor.Result,
) error {
	reader, err := result.ProfileReader()
	if err != nil {
		return fmt.Errorf("read profile for reporting: %w", err)
	}
	defer reader.Close()
	profile, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("read profile for reporting: %w", err)
	}
	if len(profile) == 0 {
		return fmt.Errorf("execution produced no profile data: %w", executor.ErrEmptyProfile)
	}
	// 采集器没报出 PeriodType 就不发：清洗规则读不到 type，这条产物到不了结果表，
	// 静默发出去只会变成"Kafka 里有数据但查不到"。
	if result.PeriodType == "" {
		return errors.New("execution produced a profile without a period type")
	}

	logger.Infof(
		"[active_profile] report profile dataid=%d bytes=%d type=%s app=%s service_name=%s",
		conf.ProfileDataID, len(profile), result.PeriodType, conf.ProfileAppName, conf.ProfileServiceName,
	)
	return sendReport(ctx, sink, &ProfileEvent{
		DataID:      conf.ProfileDataID,
		BizID:       conf.GetBizID(),
		AppName:     conf.ProfileAppName,
		ServiceName: conf.ProfileServiceName,
		PeriodType:  result.PeriodType,
		Profile:     profile,
	}, "profile")
}
