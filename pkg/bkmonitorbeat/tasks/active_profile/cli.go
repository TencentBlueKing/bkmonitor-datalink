// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/active_profile/executor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// 轻量 CLI 由 BK-JOB 在目标主机上拉起。`-active-profile-run` 只负责立即执行；
// `-active-profile-cancel` 对立即执行和计划任务通用，按执行键写入取消标记。
// 两个子命令都需要显式 `-c` 指向主配置，运行目录由其中的 path.data 推导。

const maxCLIPayloadBytes = 1 << 20

// cliRunRequest 对应技术基线定义的单机执行配置。立即执行的 task_id 是 SaaS 建的执行行 ID。
type cliRunRequest struct {
	BKTenantID string `json:"bk_tenant_id"`
	BizID      int32  `json:"bk_biz_id"`
	BKHostID   int64  `json:"bk_host_id"`
	TaskID     int32  `json:"task_id"`
	Name       string `json:"name"`
	Creator    string `json:"creator"`

	CollectionMethod string `json:"collection_method"`
	CollectionType   string `json:"collection_type"`

	Schedule            configs.ActiveProfileScheduleConfig      `json:"schedule"`
	ProcessSelector     configs.ActiveProfileProcessSelector     `json:"process_selector"`
	CollectorConfig     configs.ActiveProfileCollectorConfig     `json:"collector_config"`
	ResourceGuardConfig configs.ActiveProfileResourceGuardConfig `json:"resource_guard_config"`

	EventDataID   int32 `json:"event_data_id"`
	ProfileDataID int32 `json:"profile_data_id"`

	// Beat 本地运行参数，不属于下发协议。运行目录不在此列：它由主配置的 path.data
	// 推导，平台不下发也不能覆盖。
	PerfPath            string `json:"perf_path"`
	KeepArtifacts       bool   `json:"keep_artifacts"`
	InlineSymbolization string `json:"inline_symbolization"`

	// 产物在 Profiling/BKBase 侧的归属，与两个 DataID 一样由平台注入。
	ProfileAppName     string `json:"profile_app_name"`
	ProfileServiceName string `json:"profile_service_name"`
}

// cliMatchRequest 只携带进程定位方式：匹配预览不执行采集，因此不需要执行身份、
// DataID 与采集参数。
type cliMatchRequest struct {
	ProcessSelector configs.ActiveProfileProcessSelector `json:"process_selector"`
}

// cliMatchResponse 是匹配预览的输出。固定一层 processes，匹配不到时是空数组。
type cliMatchResponse struct {
	Processes []MatchedProcess `json:"processes"`
}

// cliMatchFailure 是匹配预览失败时的输出，退出码仍为非 0。error_type 与 finished
// 同一套取值，SaaS 据此给出展示文案；message 保留原始错误。
type cliMatchFailure struct {
	ErrorType string `json:"error_type"`
	Message   string `json:"message"`
}

// cliCancelRequest 只携带定位一次主机执行所需的字段，三类执行都必须携带 task_id。
// 运行目录由主配置的 path.data 推导，不由请求提供。
type cliCancelRequest struct {
	BKTenantID   string `json:"bk_tenant_id"`
	BizID        int32  `json:"bk_biz_id"`
	BKHostID     int64  `json:"bk_host_id"`
	TaskID       int32  `json:"task_id"`
	ScheduleType string `json:"schedule_type"`
	ScheduledAt  int64  `json:"scheduled_at"`
}

// RunCLI 执行一次立即采集。配置从 stdin 读入，避免命令行长度限制，也不会把 Token
// 残留在 ps 输出中。
func RunCLI(reader io.Reader) error {
	payload, err := readCLIPayload(reader)
	if err != nil {
		return err
	}
	var request cliRunRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return fmt.Errorf("decode active profile run config: %w", err)
	}
	// 先按原始请求拒绝非立即调度，再解析配置：计划任务由常驻 Beat 执行，不该为它
	// 读主配置或创建运行目录。
	if request.Schedule.Type != configs.ActiveProfileScheduleImmediate {
		return fmt.Errorf(
			"the lightweight CLI only executes immediate schedules, got %q",
			request.Schedule.Type,
		)
	}
	conf, err := request.taskConfig()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stopWatching := watchTerminationSignals(cancel)
	defer stopWatching()

	// 上报链路在采集之前建立：连不上本机 GSE Agent 时 finished 与产物都发不出去，
	// 先失败比先采集再报错更省主机资源，JOB 日志里也能直接看到原因。
	sink, err := newGSEAgentSink(ctx)
	if err != nil {
		return err
	}
	defer sink.Close()

	scheduledAt := conf.ScheduledAt(time.Now())
	Execute(ctx, conf, scheduledAt, ExecuteOptions{
		// 立即执行的 started 由 SaaS 在 JOB 受理后上报，CLI 不重复发送。
		ReportStarted: false,
		Sink:          sink,
	})
	return nil
}

// MatchCLI 按 process_selector 在本机匹配进程，把结果以 JSON 写入 writer，供页面
// 在提交前确认采集目标。它与采集共用 resolveProcesses 和选择器校验，所以预览结果
// 与真正执行时的匹配规则不可能分叉——这也是它做成 Beat 子命令而不是外部脚本的原因。
//
// 只读不写：不解析主配置、不创建运行目录、不连 GSE，因此不需要 -c。
//
// 命令行模式的预览只是当时的快照。协议要求 command 每次执行重新匹配，页面真正冻结
// 下来的是用户勾选后的 pid 列表。
func MatchCLI(reader io.Reader, writer io.Writer) error {
	// 请求由 SaaS 生成，读不出或校验不过多为 SaaS 与采集器版本不一致
	payload, err := readCLIPayload(reader)
	if err != nil {
		return executor.MarkError(executor.ErrInvalidConfig, err)
	}
	var request cliMatchRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return executor.MarkError(executor.ErrInvalidConfig, fmt.Errorf("decode active profile match request: %w", err))
	}
	// 预览只带匹配条件，命令行模式总是按规则重新匹配
	request.ProcessSelector.EnableDynamicCollection = true
	if err := request.ProcessSelector.Clean(); err != nil {
		return executor.MarkError(executor.ErrInvalidConfig, err)
	}

	processes, err := resolveProcesses(&request.ProcessSelector)
	// 一个都没匹配上是合法的预览结果，页面要能显示"没有匹配到进程"；采集端把它当
	// 失败是因为那时已经没有可采对象，两者的语义不同。
	if err != nil && !errors.Is(err, errNoProcessMatched) {
		return err
	}
	response := cliMatchResponse{Processes: describeProcesses(processes)}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		return fmt.Errorf("write active profile match result: %w", err)
	}
	return nil
}

// WriteMatchFailure 输出匹配预览的失败原因，stdout 在失败时同样只有一个 JSON 对象。
func WriteMatchFailure(writer io.Writer, err error) {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(cliMatchFailure{ErrorType: errorTypeFor(err), Message: err.Error()})
}

// watchTerminationSignals 把 JOB 终止本进程的信号转换成执行 context 的取消原因，
// 让采集层回收 perf 子进程组并尽力上报 cancelled。用户触发的取消走取消标记，
// 不依赖信号。
func watchTerminationSignals(cancel context.CancelCauseFunc) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	done := make(chan struct{})
	go func() {
		select {
		case received := <-signals:
			logger.Warnf("[active_profile] received %s, cancelling the current execution", received)
			cancel(ErrExecutionCancelled)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}

// CancelCLI 按执行键取消一次主机执行，立即执行与计划任务通用。它只往运行目录写一个
// 取消标记，不向任何进程发信号，因此不可能影响常驻 Beat 或同进程的其他采集任务。
// 重复取消和目标已结束都是安全无操作。
func CancelCLI(reader io.Reader) error {
	payload, err := readCLIPayload(reader)
	if err != nil {
		return err
	}
	var request cliCancelRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return fmt.Errorf("decode active profile cancel request: %w", err)
	}
	key, err := request.executionKey()
	if err != nil {
		return err
	}
	outputDir, err := configs.ActiveProfileOutputRoot()
	if err != nil {
		return err
	}

	marker, err := requestCancel(outputDir, key)
	if err != nil {
		// 写不进去必须明确报错：JOB 执行账号对运行目录没有写权限时，静默失败会让
		// 用户以为已经取消。
		return fmt.Errorf("request cancel for %s: %w", key, err)
	}
	fmt.Printf("cancel requested for execution %s\n", key)
	fmt.Printf("marker = %s\n", marker)
	return nil
}

// executionKey 校验取消请求并推导执行键，必须与采集端的 executionKey 对同一次执行
// 得到相同结果。
func (r cliCancelRequest) executionKey() (string, error) {
	if r.BKTenantID == "" || r.BizID == 0 || r.BKHostID <= 0 || r.ScheduledAt <= 0 {
		return "", fmt.Errorf("cancel request requires bk_tenant_id, bk_biz_id, bk_host_id and scheduled_at")
	}
	switch r.ScheduleType {
	case configs.ActiveProfileScheduleImmediate, configs.ActiveProfileScheduleOnceAt, configs.ActiveProfileScheduleCron:
		if r.TaskID <= 0 {
			return "", fmt.Errorf("task_id is required when schedule_type=%s", r.ScheduleType)
		}
	default:
		return "", fmt.Errorf("unsupported schedule_type %q", r.ScheduleType)
	}
	return buildExecutionKey(r.BKTenantID, r.BizID, r.TaskID, r.ScheduledAt, r.BKHostID), nil
}

func readCLIPayload(reader io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maxCLIPayloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read active profile payload: %w", err)
	}
	if len(payload) > maxCLIPayloadBytes {
		return nil, fmt.Errorf("active profile payload exceeds %d bytes", maxCLIPayloadBytes)
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("active profile payload is empty")
	}
	return payload, nil
}

func (r cliRunRequest) taskConfig() (*configs.ActiveProfileTaskConfig, error) {
	conf := configs.NewActiveProfileTaskConfig()
	conf.BizID = r.BizID
	conf.TaskID = r.TaskID
	conf.BKTenantID = r.BKTenantID
	conf.BKHostID = r.BKHostID
	conf.Name = r.Name
	conf.Creator = r.Creator
	conf.CollectionMethod = r.CollectionMethod
	conf.CollectionType = r.CollectionType
	conf.Schedule = r.Schedule
	conf.ProcessSelector = r.ProcessSelector
	conf.CollectorConfig = r.CollectorConfig
	conf.ResourceGuardConfig = r.ResourceGuardConfig
	conf.EventDataID = r.EventDataID
	conf.ProfileDataID = r.ProfileDataID
	conf.PerfPath = r.PerfPath
	conf.KeepArtifacts = r.KeepArtifacts
	conf.InlineSymbolization = r.InlineSymbolization
	conf.ProfileAppName = r.ProfileAppName
	conf.ProfileServiceName = r.ProfileServiceName

	if err := conf.Clean(); err != nil {
		return nil, fmt.Errorf("invalid active profile run config: %w", err)
	}
	if err := conf.InitIdent(); err != nil {
		return nil, fmt.Errorf("init active profile config ident: %w", err)
	}
	return conf, nil
}
