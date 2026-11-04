// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

const (
	startedEventName  = "active_profiling_started"
	finishedEventName = "active_profiling_finished"

	eventContentSchemaVersion = 1

	// StatusRunning 只出现在 started Event 的 dimension 中。running 与 unknown
	// 作为聚合状态由 SaaS 从 started/finished 派生，不由采集端写回 finished。
	StatusRunning = "running"

	// finished 只上报以下终态。
	StatusSucceed        = "succeed"
	StatusPartialSucceed = "partial_succeed"
	StatusFailed         = "failed"
	StatusTimeout        = "timeout"
	StatusCancelled      = "cancelled"

	// finished 的 error_type 归类失败原因，SaaS 按类型给出展示文案，message 保留原始
	// 错误；成功、超时与取消不归类。
	ErrorTypeScheduleExpired  = "schedule_expired"
	ErrorTypeTaskBusy         = "task_busy"
	ErrorTypeProcessNotFound  = "process_not_found"
	ErrorTypeEmptyProfile     = "empty_profile"
	ErrorTypePerfUnavailable  = "perf_unavailable"
	ErrorTypeGoEndpointFailed = "go_endpoint_failed"
	ErrorTypeMemoryLimit      = "memory_limit"
	ErrorTypeDiskLimit        = "disk_limit"
	ErrorTypeReportFailed     = "report_failed"
	ErrorTypeConfigInvalid    = "config_invalid"
	ErrorTypeProcessesExited  = "processes_exited"
	ErrorTypeCollectFailed    = "collect_failed"

	maxErrorMessageBytes = 2048

	// maxEventContentBytes 是 event.content 除 target_hosts 外序列化后的总字节上限。
	// 超限时丢弃进程列表重建，始终保持是一段合法 JSON。target_hosts 随目标主机数增长，
	// 不计入上限，也不会被丢弃。
	maxEventContentBytes = 32 * 1024
)

// ProcessResult 是 finished Event 中的进程项，严格只包含 pid 与 process_name，
// 不携带进程级状态、阶段、错误码或错误文本。
type ProcessResult struct {
	PID         int32  `json:"pid"`
	ProcessName string `json:"process_name"`
}

// ExecutionOutcome 是一次主机执行收敛后的终态事实，同时供 finished Event 与终态
// 运营指标使用。
type ExecutionOutcome struct {
	Status       string
	Processes    []ProcessResult
	ErrorType    string
	ErrorMessage string
}

// executionIdentity 是 started 与 finished 共有的身份与任务字段。
type executionIdentity struct {
	EventDataID      int32
	TenantID         string
	BizID            int32
	TaskID           int32
	TaskName         string
	Creator          string
	BKHostID         int64
	IP               string
	ScheduleType     string
	ScheduledAt      time.Time
	CollectionMethod string
	CollectionType   string
	StartedAt        time.Time

	config      eventConfigContent
	targetHosts []configs.ActiveProfileTargetHost
}

func newExecutionIdentity(
	conf *configs.ActiveProfileTaskConfig,
	ip string,
	scheduledAt time.Time,
	startedAt time.Time,
) executionIdentity {
	return executionIdentity{
		EventDataID:      conf.EventDataID,
		TenantID:         conf.BKTenantID,
		BizID:            conf.GetBizID(),
		TaskID:           conf.GetTaskID(),
		TaskName:         conf.Name,
		Creator:          conf.Creator,
		BKHostID:         conf.BKHostID,
		IP:               ip,
		ScheduleType:     conf.Schedule.Type,
		ScheduledAt:      scheduledAt,
		CollectionMethod: conf.CollectionMethod,
		CollectionType:   conf.CollectionType,
		StartedAt:        startedAt,
		config:           newEventConfigContent(conf),
		targetHosts:      conf.TargetHosts,
	}
}

// dimensions 只包含页面筛选、分桶和聚合需要的字段。执行参数、错误文本和内部
// 诊断信息一律放入 event.content。
func (e executionIdentity) dimensions(status string) common.MapStr {
	dimension := common.MapStr{
		"bk_tenant_id":      e.TenantID,
		"bk_biz_id":         strconv.FormatInt(int64(e.BizID), 10),
		"task_name":         e.TaskName,
		"creator":           e.Creator,
		"bk_host_id":        strconv.FormatInt(e.BKHostID, 10),
		"schedule_type":     e.ScheduleType,
		"scheduled_at":      strconv.FormatInt(e.ScheduledAt.UnixMilli(), 10),
		"collection_method": e.CollectionMethod,
		"collection_type":   e.CollectionType,
		"status":            status,
		"task_id":           strconv.FormatInt(int64(e.TaskID), 10),
	}
	return dimension
}

// StartedEvent 由常驻 Beat 在进程解析、资源检查和采集器启动之前发送。立即执行的
// 开始由 SaaS 直接写入执行表，轻量 CLI 不发送该事件。
type StartedEvent struct {
	executionIdentity
}

func newStartedEvent(
	conf *configs.ActiveProfileTaskConfig,
	ip string,
	scheduledAt time.Time,
	startedAt time.Time,
) *StartedEvent {
	return &StartedEvent{executionIdentity: newExecutionIdentity(conf, ip, scheduledAt, startedAt)}
}

func (e *StartedEvent) data() common.MapStr {
	content := startedContent{
		SchemaVersion: eventContentSchemaVersion,
		StartedAt:     e.StartedAt.UnixMilli(),
		Config:        e.config,
		TargetHosts:   e.targetHosts,
	}
	return common.MapStr{
		"event_name": startedEventName,
		"event": common.MapStr{
			"content": marshalEventContent(content),
		},
		"target":    e.IP,
		"dimension": e.dimensions(StatusRunning),
		"timestamp": e.StartedAt.UnixMilli(),
	}
}

func (e *StartedEvent) AsMapStr() common.MapStr {
	return common.MapStr{
		"dataid": e.EventDataID,
		"data":   []common.MapStr{e.data()},
	}
}

func (e *StartedEvent) GetType() string { return define.ModuleActiveProfile }

func (e *StartedEvent) IgnoreCMDBLevel() bool { return true }

// FinishedEvent 是页面终态事实。计划任务由常驻 Beat 上报，立即任务由轻量 CLI
// 上报；允许在 started 丢失或乱序时独立生效。
type FinishedEvent struct {
	executionIdentity
	FinishedAt   time.Time
	Status       string
	Processes    []ProcessResult
	ErrorType    string
	ErrorMessage string
}

func newFinishedEvent(
	conf *configs.ActiveProfileTaskConfig,
	ip string,
	scheduledAt time.Time,
	startedAt time.Time,
	finishedAt time.Time,
	outcome ExecutionOutcome,
) *FinishedEvent {
	processes := outcome.Processes
	if processes == nil {
		processes = []ProcessResult{}
	}
	return &FinishedEvent{
		executionIdentity: newExecutionIdentity(conf, ip, scheduledAt, startedAt),
		FinishedAt:        finishedAt,
		Status:            outcome.Status,
		Processes:         processes,
		ErrorType:         outcome.ErrorType,
		ErrorMessage:      truncateString(outcome.ErrorMessage, maxErrorMessageBytes),
	}
}

func (e *FinishedEvent) data() common.MapStr {
	dimension := e.dimensions(e.Status)
	// dimension 的值必须是标量字符串：自定义事件 schema 不接受数组，上报端发送
	// 数组会被直接拒绝。多个进程以分隔符拼接，进程名与 PID 按位置一一对应。
	dimension["process_names"], dimension["process_pids"] = processDimensions(e.Processes)
	content := finishedContent{
		SchemaVersion:       eventContentSchemaVersion,
		StartedAt:           e.StartedAt.UnixMilli(),
		FinishedAt:          e.FinishedAt.UnixMilli(),
		ExecutionDurationMS: e.FinishedAt.Sub(e.StartedAt).Milliseconds(),
		Config:              e.config,
		Processes:           e.Processes,
		ErrorType:           e.ErrorType,
		ErrorMessage:        e.ErrorMessage,
		TargetHosts:         e.targetHosts,
	}
	return common.MapStr{
		"event_name": finishedEventName,
		"event": common.MapStr{
			"content": marshalFinishedContent(content),
		},
		"target":    e.IP,
		"dimension": dimension,
		"timestamp": e.FinishedAt.UnixMilli(),
	}
}

func (e *FinishedEvent) AsMapStr() common.MapStr {
	return common.MapStr{
		"dataid": e.EventDataID,
		"data":   []common.MapStr{e.data()},
	}
}

func (e *FinishedEvent) GetType() string { return define.ModuleActiveProfile }

func (e *FinishedEvent) IgnoreCMDBLevel() bool { return true }

// processKeySeparator 是 process_names 与 process_pids 拼接多个进程时的分隔符。
const processKeySeparator = ","

// processDimensions 把 finished Event 实际匹配到的进程序列化为两个 dimension 值：
// 按 PID 升序、同一 PID 只保留一次，同名进程不去重，进程名与 PID 按位置一一对应，
// SaaS 据此还原两者的对应关系。页面对两个维度都做包含匹配，因此不加首尾分隔符。
func processDimensions(processes []ProcessResult) (names, pids string) {
	unique := make([]ProcessResult, 0, len(processes))
	seen := make(map[int32]struct{}, len(processes))
	for _, process := range processes {
		if _, exists := seen[process.PID]; exists {
			continue
		}
		seen[process.PID] = struct{}{}
		unique = append(unique, process)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i].PID < unique[j].PID })

	nameValues := make([]string, len(unique))
	pidValues := make([]string, len(unique))
	for i, process := range unique {
		nameValues[i] = sanitizeProcessKeyName(process.ProcessName)
		pidValues[i] = strconv.FormatInt(int64(process.PID), 10)
	}
	return strings.Join(nameValues, processKeySeparator), strings.Join(pidValues, processKeySeparator)
}

// sanitizeProcessKeyName 归一化进入 dimension 的进程名。comm 由进程自己通过 prctl 设置，
// 完全可能包含分隔符，直接拼接会让进程名与 PID 的位置错开。归一化只作用于 dimension，
// event.content.processes 仍然保留真实进程名用于展示。
func sanitizeProcessKeyName(name string) string {
	return strings.ReplaceAll(name, processKeySeparator, "_")
}

type startedContent struct {
	SchemaVersion int                               `json:"schema_version"`
	StartedAt     int64                             `json:"started_at"`
	Config        eventConfigContent                `json:"config"`
	TargetHosts   []configs.ActiveProfileTargetHost `json:"target_hosts,omitempty"`
}

type finishedContent struct {
	SchemaVersion       int                               `json:"schema_version"`
	StartedAt           int64                             `json:"started_at"`
	FinishedAt          int64                             `json:"finished_at"`
	ExecutionDurationMS int64                             `json:"execution_duration_ms"`
	Config              eventConfigContent                `json:"config"`
	Processes           []ProcessResult                   `json:"processes"`
	ErrorType           string                            `json:"error_type"`
	ErrorMessage        string                            `json:"message"`
	TargetHosts         []configs.ActiveProfileTargetHost `json:"target_hosts,omitempty"`
}

// eventConfigContent 是本次实际执行配置的快照，供接口查询与表单回填使用。
// 两个 DataID、GSE 凭据和 Demo HTTP Token 都不进入该结构。
type eventConfigContent struct {
	Schedule            scheduleContent      `json:"schedule"`
	TargetConfig        targetConfigContent  `json:"target_config"`
	ProcessSelector     *selectorContent     `json:"process_selector,omitempty"`
	CollectorConfig     collectorContent     `json:"collector_config"`
	ResourceGuardConfig resourceGuardContent `json:"resource_guard_config"`
}

type scheduleContent struct {
	Type             string `json:"type"`
	ExecuteAt        int64  `json:"execute_at,omitempty"`
	CronExpression   string `json:"cron_expression,omitempty"`
	Timezone         string `json:"timezone"`
	EffectiveStartAt int64  `json:"effective_start_at,omitempty"`
	EffectiveEndAt   int64  `json:"effective_end_at,omitempty"`
}

type targetConfigContent struct {
	NodeType string              `json:"node_type"`
	Nodes    []targetNodeContent `json:"nodes"`
}

type targetNodeContent struct {
	BKHostID int64 `json:"bk_host_id"`
}

// selectorContent 与页面提交的 {type, config} 结构一致，每台主机写入同一份完整选择器，
// 供详情直接回填表单。
type selectorContent struct {
	Type   string                `json:"type"`
	Config selectorConfigContent `json:"config"`
}

type selectorConfigContent struct {
	MatchPattern string `json:"match_pattern,omitempty"`
	// 以下两项只属于 command，为空也固定输出，配置快照的形态不随取值变化
	ExcludePattern          *string                        `json:"exclude_pattern,omitempty"`
	EnableDynamicCollection *bool                          `json:"enable_dynamic_collection,omitempty"`
	HostPIDs                []configs.ActiveProfileHostPID `json:"host_pids,omitempty"`
	PIDPath                 string                         `json:"pid_path,omitempty"`
	PIDs                    string                         `json:"pids,omitempty"`
}

// collectorContent 只写入当前采集方式的字段子集。协议要求按 collection_method +
// collection_type 选择完整 Schema，不使用字段并集伪造一条 Event。
type collectorContent struct {
	DurationSec int    `json:"duration_sec,omitempty"`
	FrequencyHz int    `json:"frequency_hz,omitempty"`
	StackUnwind string `json:"stack_unwind,omitempty"`
	IPAddress   string `json:"ip_address,omitempty"`
	Port        int    `json:"port,omitempty"`
}

type resourceGuardContent struct {
	AutoStopOnResourcePressure bool  `json:"auto_stop_on_resource_pressure"`
	MaxDiskUsage               int64 `json:"max_disk_usage"`
	MinMemLevel                int64 `json:"min_mem_level"`
}

func newEventConfigContent(conf *configs.ActiveProfileTaskConfig) eventConfigContent {
	content := eventConfigContent{
		Schedule: scheduleContent{
			Type:     conf.Schedule.Type,
			Timezone: conf.Schedule.Timezone,
		},
		TargetConfig: targetConfigContent{
			NodeType: "INSTANCE",
			Nodes:    []targetNodeContent{{BKHostID: conf.BKHostID}},
		},
		CollectorConfig: newCollectorContent(conf),
		ResourceGuardConfig: resourceGuardContent{
			AutoStopOnResourcePressure: conf.ResourceGuardConfig.AutoStopOnResourcePressure,
			MaxDiskUsage:               conf.ResourceGuardConfig.MaxDiskUsage,
			MinMemLevel:                conf.ResourceGuardConfig.MinMemLevel,
		},
	}
	switch conf.Schedule.Type {
	case configs.ActiveProfileScheduleCron:
		content.Schedule.CronExpression = conf.Schedule.CronExpression
		content.Schedule.EffectiveStartAt = conf.Schedule.EffectiveStartAt
		content.Schedule.EffectiveEndAt = conf.Schedule.EffectiveEndAt
	default:
		content.Schedule.ExecuteAt = conf.Schedule.ExecuteAt
	}
	content.ProcessSelector = newSelectorContent(conf)
	return content
}

func newCollectorContent(conf *configs.ActiveProfileTaskConfig) collectorContent {
	collector := conf.CollectorConfig
	if conf.IsGolang() {
		// go_heap 是瞬时快照，协议中没有 duration_sec；DurationSec 已在配置清洗时
		// 归零，omitempty 因此不会写出该字段。
		return collectorContent{
			IPAddress:   collector.IPAddress,
			Port:        collector.Port,
			DurationSec: collector.DurationSec,
		}
	}
	return collectorContent{
		DurationSec: collector.DurationSec,
		FrequencyHz: collector.FrequencyHz,
		StackUnwind: collector.StackUnwind,
	}
}

func newSelectorContent(conf *configs.ActiveProfileTaskConfig) *selectorContent {
	selector := conf.ProcessSelector
	var config selectorConfigContent
	switch selector.Type {
	case configs.ActiveProfileSelectorCommand:
		excludePattern := selector.ExcludePattern
		enableDynamicCollection := selector.EnableDynamicCollection
		config = selectorConfigContent{
			MatchPattern:            selector.MatchPattern,
			ExcludePattern:          &excludePattern,
			EnableDynamicCollection: &enableDynamicCollection,
			HostPIDs:                selector.HostPIDs,
		}
	case configs.ActiveProfileSelectorPIDFile:
		config = selectorConfigContent{PIDPath: selector.PIDPath}
	case configs.ActiveProfileSelectorPID:
		config = selectorConfigContent{PIDs: selector.PIDs}
	default:
		// system 是本地调试选择器，不属于下发协议，因此不进入配置快照。
		return nil
	}
	return &selectorContent{Type: selector.Type, Config: config}
}

func marshalEventContent(content startedContent) string {
	raw, err := json.Marshal(content)
	if err != nil {
		return fmt.Sprintf(`{"schema_version":%d}`, eventContentSchemaVersion)
	}
	return string(raw)
}

// marshalFinishedContent 保证 event.content 除 target_hosts 外不超过字节上限，且降级
// 结果仍是一段可解析的合法 JSON：超限时丢弃进程列表。配置快照受选择器大小校验约束，
// 丢弃进程列表后一定放得下，因此不会被丢弃；target_hosts 不计入上限，始终原样保留。
func marshalFinishedContent(content finishedContent) string {
	targetHosts := content.TargetHosts
	content.TargetHosts = nil
	raw, err := json.Marshal(content)
	if err != nil {
		return fmt.Sprintf(`{"schema_version":%d}`, eventContentSchemaVersion)
	}
	if len(raw) > maxEventContentBytes {
		content.Processes = []ProcessResult{}
		content.ErrorMessage = truncateString(
			appendDegradeReason(content.ErrorMessage, "event content exceeded size limit: processes omitted"),
			maxErrorMessageBytes,
		)
	}
	content.TargetHosts = targetHosts
	full, err := json.Marshal(content)
	if err != nil {
		return fmt.Sprintf(`{"schema_version":%d}`, eventContentSchemaVersion)
	}
	return string(full)
}

func appendDegradeReason(message, reason string) string {
	if message == "" {
		return reason
	}
	return message + "; " + reason
}

func truncateString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
