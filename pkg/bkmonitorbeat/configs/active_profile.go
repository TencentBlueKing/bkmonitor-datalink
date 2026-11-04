// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package configs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

const (
	ConfigTypeActiveProfile = define.ModuleActiveProfile

	// 一期接受 perf / native_cpu 与 golang / go_cpu、go_heap。Java 采集方式协议已
	// 定义，实现待后续阶段补齐。
	ActiveProfileMethodPerf   = "perf"
	ActiveProfileMethodGolang = "golang"

	ActiveProfileTypeNativeCPU = "native_cpu"
	ActiveProfileTypeGoCPU     = "go_cpu"
	ActiveProfileTypeGoHeap    = "go_heap"

	ActiveProfileScheduleImmediate = "immediate"
	ActiveProfileScheduleOnceAt    = "once_at"
	ActiveProfileScheduleCron      = "cron"

	ActiveProfileSelectorCommand = "command"
	ActiveProfileSelectorPIDFile = "pid_file"
	ActiveProfileSelectorPID     = "pid"
	// ActiveProfileSelectorSystem 不属于下发协议，仅供 Beat 本地调试使用。
	// 平台不会下发该类型，也不能在生产任务中开放。
	ActiveProfileSelectorSystem = "system"

	ActiveProfileStackUnwindFP    = "fp"
	ActiveProfileStackUnwindDWARF = "dwarf"

	ActiveProfileInlineAuto = "auto"
	ActiveProfileInlineOff  = "off"

	ActiveProfileDefaultDurationSec = 30
	ActiveProfileMaxDurationSec     = 600
	ActiveProfileDefaultFrequencyHz = 99
	ActiveProfileMaxFrequencyHz     = 999

	// 选择器会进入每条 Event 的配置快照，event.content 有字节上限。限制选择器大小
	// 保证快照总能完整上报，SaaS 提交时按同样的上限校验。
	ActiveProfileMaxSelectorPIDs          = 1000
	ActiveProfileMaxSelectorTextLength    = 1024
	ActiveProfileMaxSelectorPIDPaths      = 20
	ActiveProfileMaxSelectorPIDPathLength = 4096
	ActiveProfileMaxSelectorHostPIDs      = 500

	// ActiveProfileSelectorSeparator 分隔 pid_path 中的多个文件路径与 pids 中的多个 PID。
	ActiveProfileSelectorSeparator = ";"

	// ActiveProfilePostProcessAllowance is the bounded budget for preflight,
	// perf script conversion and native symbolization after perf record's
	// sampling window. Active Profile derives its execution timeout locally;
	// a downlinked BaseTaskParam.timeout is deliberately ignored.
	ActiveProfilePostProcessAllowance = 5 * time.Minute
)

// ActiveProfileScheduleConfig 是下发协议中的 schedule 段。immediate 与 once_at
// 使用 execute_at，cron 使用表达式与可选生效窗口。
type ActiveProfileScheduleConfig struct {
	Type             string `config:"type" json:"type"`
	ExecuteAt        int64  `config:"execute_at" json:"execute_at"`
	CronExpression   string `config:"cron_expression" json:"cron_expression"`
	Timezone         string `config:"timezone" json:"timezone"`
	EffectiveStartAt int64  `config:"effective_start_at" json:"effective_start_at"`
	EffectiveEndAt   int64  `config:"effective_end_at" json:"effective_end_at"`
}

// ActiveProfileHostPID 是关闭动态采集的 command 勾选的一个进程。
type ActiveProfileHostPID struct {
	BKHostID int64 `config:"bk_host_id" json:"bk_host_id"`
	PID      int32 `config:"pid" json:"pid"`
}

// ActiveProfileTargetHost 是目标主机快照中的一项，ip 为下发时 CMDB 中的内网 IP，查不到时为空。
type ActiveProfileTargetHost struct {
	BKHostID int64  `config:"bk_host_id" json:"bk_host_id"`
	IP       string `config:"ip" json:"ip"`
}

// ActiveProfileProcessSelector 是下发协议中的 process_selector 段。SaaS 向每台目标主机
// 下发同一份选择器：关闭动态采集的 command 带全部主机勾选的 host_pids，由采集端按本机
// bk_host_id 取出自己的 PID；pid_path 与 pids 是分号分隔的字符串，在每台主机上原样生效。
type ActiveProfileProcessSelector struct {
	Type                    string                 `config:"type" json:"type"`
	MatchPattern            string                 `config:"match_pattern" json:"match_pattern"`
	ExcludePattern          string                 `config:"exclude_pattern" json:"exclude_pattern"`
	EnableDynamicCollection bool                   `config:"enable_dynamic_collection" json:"enable_dynamic_collection"`
	HostPIDs                []ActiveProfileHostPID `config:"host_pids" json:"host_pids"`
	PIDPath                 string                 `config:"pid_path" json:"pid_path"`
	PIDs                    string                 `config:"pids" json:"pids"`

	excludeRegexp *regexp.Regexp
	pidPaths      []string
	targetPIDs    []int32
}

// ExcludeRegexp 返回已编译的排除正则；未配置排除条件时返回 nil。
func (s *ActiveProfileProcessSelector) ExcludeRegexp() *regexp.Regexp {
	return s.excludeRegexp
}

// PIDPaths 返回 pid_path 拆分后的文件路径。
func (s *ActiveProfileProcessSelector) PIDPaths() []string {
	return s.pidPaths
}

// TargetPIDs 返回按 PID 采集时本机要采集的 PID：pid 模式来自 pids，关闭动态采集的
// command 来自 host_pids 中属于本机的条目，没有勾选本机进程时为空。
func (s *ActiveProfileProcessSelector) TargetPIDs() []int32 {
	return s.targetPIDs
}

// ActiveProfileCollectorConfig 只描述采集器自身参数，不含目标、进程选择、调度与资源保护。
// 协议按 collection_method + collection_type 选择字段子集：perf 使用 duration_sec、
// frequency_hz 与 stack_unwind；golang 使用 ip_address、port，go_cpu 再加 duration_sec。
type ActiveProfileCollectorConfig struct {
	DurationSec int    `config:"duration_sec" json:"duration_sec"`
	FrequencyHz int    `config:"frequency_hz" json:"frequency_hz"`
	StackUnwind string `config:"stack_unwind" json:"stack_unwind"`

	// IPAddress 与 Port 指向目标主机上的 net/http/pprof endpoint，不是任务目标
	// 主机身份，也不参与进程选择。
	IPAddress string `config:"ip_address" json:"ip_address"`
	Port      int    `config:"port" json:"port"`
}

// ActiveProfileResourceGuardConfig 是平台下发的资源保护参数，两个阈值单位均为 MB。
// MaxDiskUsage 限制本次采集临时文件占用，MinMemLevel 是主机可用内存低水位。
type ActiveProfileResourceGuardConfig struct {
	AutoStopOnResourcePressure bool  `config:"auto_stop_on_resource_pressure" json:"auto_stop_on_resource_pressure"`
	MaxDiskUsage               int64 `config:"max_disk_usage" json:"max_disk_usage"`
	MinMemLevel                int64 `config:"min_mem_level" json:"min_mem_level"`
}

// ActiveProfileTaskConfig 对应技术基线定义的单机执行配置。计划任务由 NodeMan 下发，
// 立即任务由 JOB 通过轻量 CLI 传入，两者结构一致，仅 task_id 是否存在不同。
type ActiveProfileTaskConfig struct {
	BaseTaskParam `config:"_,inline"`

	BKTenantID string `config:"bk_tenant_id"`
	BKHostID   int64  `config:"bk_host_id"`
	Name       string `config:"name"`
	Creator    string `config:"creator"`

	CollectionMethod string `config:"collection_method"`
	CollectionType   string `config:"collection_type"`

	Schedule            ActiveProfileScheduleConfig      `config:"schedule"`
	ProcessSelector     ActiveProfileProcessSelector     `config:"process_selector"`
	CollectorConfig     ActiveProfileCollectorConfig     `config:"collector_config"`
	ResourceGuardConfig ActiveProfileResourceGuardConfig `config:"resource_guard_config"`

	// TargetHosts 是本次执行全部目标主机的快照，由平台注入，不参与本机采集，只原样写入
	// started/finished 的 event.content，没有上报 Event 的主机由下游据此补出来。
	TargetHosts []ActiveProfileTargetHost `config:"target_hosts"`

	// 两个 DataID 由平台注入，只用于上报路由，不写入 Event config。
	EventDataID   int32 `config:"event_data_id"`
	ProfileDataID int32 `config:"profile_data_id"`

	// ProfileAppName 与 ProfileServiceName 同样由平台注入，只写入 pprof 上报载荷的
	// app / service_name，用于产物在 Profiling/BKBase 侧定位；用户不填，也不进入
	// Event config。
	ProfileAppName     string `config:"profile_app_name"`
	ProfileServiceName string `config:"profile_service_name"`

	// 以下字段是 Beat 本地运行参数，不属于下发协议，平台不会提供。注意这是约定而不是
	// 强制：它们的 config tag 仍然会接受子配置里的值（对比 OutputDir 用 ignore 强制
	// 拒绝），因为它们需要按主机差异调整，而 OutputDir 必须两个进程算出同一个值。
	//
	// PerfPath 缺省为 "perf"，由 preflight 用 exec.LookPath 解析，所以绝大多数主机
	// 不需要填。保留覆盖能力是因为 perf 并不总在 PATH 里：Debian/Ubuntu 的
	// /usr/bin/perf 是个按内核版本分发的 wrapper，缺对应 linux-tools 包时会直接失败，
	// 真正可用的二进制在 /usr/lib/linux-tools/<uname -r>/perf 这类带版本的路径下；
	// 也有镜像把它放在 /usr/libexec/perf。Golang 采集不使用该字段。
	PerfPath            string `config:"perf_path"`
	KeepArtifacts       bool   `config:"keep_artifacts"`
	InlineSymbolization string `config:"inline_symbolization"`

	// OutputDir 不是配置项：它由主配置的 path.data 推导为 <path.data>/active_profile，
	// 常驻 Beat 与轻量 CLI 因此得到同一个绝对路径，取消命令才能按执行键定位运行目录。
	// 不接受下发也不接受本地覆盖，避免两个进程算出不同的目录。
	OutputDir string `config:"output_dir,ignore"`

	executeAt        time.Time
	effectiveStartAt time.Time
	effectiveEndAt   time.Time
	cronSchedule     cron.Schedule
}

func (c *ActiveProfileTaskConfig) InitIdent() error {
	return c.initIdent(c)
}

func (c *ActiveProfileTaskConfig) GetType() string {
	return ConfigTypeActiveProfile
}

func NewActiveProfileTaskConfig() *ActiveProfileTaskConfig {
	conf := &ActiveProfileTaskConfig{
		BaseTaskParam:       NewBaseTaskParam(),
		CollectionMethod:    ActiveProfileMethodPerf,
		CollectionType:      ActiveProfileTypeNativeCPU,
		InlineSymbolization: ActiveProfileInlineAuto,
		PerfPath:            "perf",
		Schedule: ActiveProfileScheduleConfig{
			Type:     ActiveProfileScheduleOnceAt,
			Timezone: "UTC",
		},
		CollectorConfig: ActiveProfileCollectorConfig{
			DurationSec: ActiveProfileDefaultDurationSec,
			FrequencyHz: ActiveProfileDefaultFrequencyHz,
			StackUnwind: ActiveProfileStackUnwindFP,
		},
	}
	conf.Timeout = conf.ExecutionTimeout()
	conf.AvailableDuration = conf.Timeout
	conf.Period = time.Hour
	return conf
}

func (c *ActiveProfileTaskConfig) Clean() error {
	if err := utils.CleanCompositeParamList(&c.BaseTaskParam); err != nil {
		return err
	}
	c.BKTenantID = strings.TrimSpace(c.BKTenantID)
	if c.BKTenantID == "" {
		return fmt.Errorf("bk_tenant_id is required")
	}
	// bk_biz_id 在 Beat 侧只作定位/上报键，原样写入 Event 与 pprof 标签。
	// SaaS 查询时会把负数转成空间 ID；这里不做转换，也不要求必须为正。
	// 0 仍不是合法业务或空间，因此只拒绝零值。
	if c.BizID == 0 {
		return fmt.Errorf("bk_biz_id is required")
	}
	if c.BKHostID <= 0 {
		return fmt.Errorf("bk_host_id must be positive")
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if err := c.cleanCollection(); err != nil {
		return err
	}
	if err := c.cleanSchedule(); err != nil {
		return err
	}
	// task_id 的约束依赖已校验的 schedule.type，因此必须放在 cleanSchedule 之后。
	if err := c.cleanTaskID(); err != nil {
		return err
	}
	if err := c.cleanProcessSelector(); err != nil {
		return err
	}
	if err := c.cleanCollectorConfig(); err != nil {
		return err
	}
	if err := c.cleanResourceGuardConfig(); err != nil {
		return err
	}
	if c.EventDataID <= 0 {
		return fmt.Errorf("event_data_id must be positive")
	}
	if c.ProfileDataID <= 0 {
		return fmt.Errorf("profile_data_id must be positive")
	}
	if c.InlineSymbolization == "" {
		c.InlineSymbolization = ActiveProfileInlineAuto
	}
	if c.InlineSymbolization != ActiveProfileInlineAuto && c.InlineSymbolization != ActiveProfileInlineOff {
		return fmt.Errorf("unsupported inline_symbolization %q", c.InlineSymbolization)
	}
	if c.PerfPath == "" {
		c.PerfPath = "perf"
	}
	// 运行目录由主配置的 path.data 推导，不接受下发也不接受本地覆盖。测试可以预先
	// 注入一个临时目录来跳过这次解析。
	if c.OutputDir == "" {
		outputRoot, err := ActiveProfileOutputRoot()
		if err != nil {
			return err
		}
		c.OutputDir = outputRoot
	}
	if err := c.cleanProfileTarget(); err != nil {
		return err
	}

	// The platform only specifies the sampling duration. Keep timeout and the
	// generic task period internally consistent so BaseTaskMetaParam cannot
	// shorten a valid Active Profile run because timeout was omitted or stale.
	c.Timeout = c.ExecutionTimeout()
	c.AvailableDuration = c.Timeout
	if c.Period < c.Timeout {
		c.Period = c.Timeout
	}
	return nil
}

func (c *ActiveProfileTaskConfig) cleanCollection() error {
	if c.CollectionMethod == "" {
		c.CollectionMethod = ActiveProfileMethodPerf
	}
	if c.CollectionType == "" {
		c.CollectionType = ActiveProfileTypeNativeCPU
	}
	switch {
	case c.CollectionMethod == ActiveProfileMethodPerf && c.CollectionType == ActiveProfileTypeNativeCPU:
	case c.CollectionMethod == ActiveProfileMethodGolang &&
		(c.CollectionType == ActiveProfileTypeGoCPU || c.CollectionType == ActiveProfileTypeGoHeap):
	default:
		return fmt.Errorf(
			"unsupported active_profile collection_method=%q collection_type=%q",
			c.CollectionMethod,
			c.CollectionType,
		)
	}
	return nil
}

// IsGolang 表示本次采集读取目标进程的 net/http/pprof endpoint，而不是拉起 perf。
func (c *ActiveProfileTaskConfig) IsGolang() bool {
	return c.CollectionMethod == ActiveProfileMethodGolang
}

// cleanTaskID 校验 task_id。这组约束必须与取消入口的 cliCancelRequest.executionKey
// 完全一致：两侧对同一次执行推导出的执行键不能分叉。
//
// 三类执行都必须有正数 task_id：once_at/cron 是计划 ID，immediate 是 SaaS 提交时建的
// 执行行 ID。执行键、Event 维度与 pprof 标签都带上它，预计算按它定位执行行；计划执行
// 缺了它，任务锁、调度持久化与取消定位也会失去隔离。
func (c *ActiveProfileTaskConfig) cleanTaskID() error {
	if c.TaskID <= 0 {
		return fmt.Errorf("task_id must be positive when schedule.type=%s", c.Schedule.Type)
	}
	return nil
}

// ExecutionTimeout returns Active Profile's internal end-to-end execution
// budget. It intentionally does not read BaseTaskParam.Timeout.
// go_heap 没有采样窗口，DurationSec 为 0，预算即后处理余量。
func (c *ActiveProfileTaskConfig) ExecutionTimeout() time.Duration {
	return time.Duration(c.CollectorConfig.DurationSec)*time.Second + ActiveProfilePostProcessAllowance
}

func (c *ActiveProfileTaskConfig) cleanCollectorConfig() error {
	if c.IsGolang() {
		return c.cleanGolangCollectorConfig()
	}
	return c.cleanPerfCollectorConfig()
}

// cleanGolangCollectorConfig 校验 pprof endpoint 参数。frequency_hz 与 stack_unwind
// 属于 perf，构造函数按 perf 预置了默认值，这里清零，避免 perf 专有参数进入 Event
// 配置快照与上传参数。
func (c *ActiveProfileTaskConfig) cleanGolangCollectorConfig() error {
	collector := &c.CollectorConfig
	collector.FrequencyHz = 0
	collector.StackUnwind = ""

	collector.IPAddress = strings.TrimSpace(collector.IPAddress)
	if collector.IPAddress == "" {
		return fmt.Errorf("collector_config.ip_address is required when collection_method=%s", ActiveProfileMethodGolang)
	}
	if collector.Port < 1 || collector.Port > 65535 {
		return fmt.Errorf("collector_config.port must be between 1 and 65535")
	}
	// go_heap 取的是瞬时快照，协议中没有 duration_sec；执行预算退化为后处理余量。
	if c.CollectionType == ActiveProfileTypeGoHeap {
		collector.DurationSec = 0
		return nil
	}
	if collector.DurationSec <= 0 {
		collector.DurationSec = ActiveProfileDefaultDurationSec
	}
	if collector.DurationSec > ActiveProfileMaxDurationSec {
		return fmt.Errorf("collector_config.duration_sec must not exceed %d", ActiveProfileMaxDurationSec)
	}
	return nil
}

func (c *ActiveProfileTaskConfig) cleanPerfCollectorConfig() error {
	if c.CollectorConfig.DurationSec <= 0 {
		c.CollectorConfig.DurationSec = ActiveProfileDefaultDurationSec
	}
	if c.CollectorConfig.DurationSec > ActiveProfileMaxDurationSec {
		return fmt.Errorf("collector_config.duration_sec must not exceed %d", ActiveProfileMaxDurationSec)
	}
	if c.CollectorConfig.FrequencyHz <= 0 {
		c.CollectorConfig.FrequencyHz = ActiveProfileDefaultFrequencyHz
	}
	if c.CollectorConfig.FrequencyHz > ActiveProfileMaxFrequencyHz {
		return fmt.Errorf("collector_config.frequency_hz must not exceed %d", ActiveProfileMaxFrequencyHz)
	}
	// stack_unwind 是 native_cpu 的必填项，Beat 不静默补默认值。
	switch c.CollectorConfig.StackUnwind {
	case ActiveProfileStackUnwindFP, ActiveProfileStackUnwindDWARF:
	default:
		return fmt.Errorf("collector_config.stack_unwind must be %q or %q",
			ActiveProfileStackUnwindFP, ActiveProfileStackUnwindDWARF)
	}
	return nil
}

// cleanResourceGuardConfig 只做类型与范围校验。开关关闭或任一阈值为 0 时整套保护
// 关闭，Beat 不写死非零默认值。
//
// 整段可以不下发：两个阈值缺省为 0，开关缺省为 false，等价于不启用保护。
func (c *ActiveProfileTaskConfig) cleanResourceGuardConfig() error {
	if c.ResourceGuardConfig.MaxDiskUsage < 0 {
		return fmt.Errorf("resource_guard_config.max_disk_usage must be a non-negative integer")
	}
	if c.ResourceGuardConfig.MinMemLevel < 0 {
		return fmt.Errorf("resource_guard_config.min_mem_level must be a non-negative integer")
	}
	return nil
}

// DiskQuotaEnabled 表示是否限制本次采集临时文件占用。
func (c *ActiveProfileTaskConfig) DiskQuotaEnabled() bool {
	return c.ResourceGuardConfig.AutoStopOnResourcePressure && c.ResourceGuardConfig.MaxDiskUsage > 0
}

// MemoryGuardEnabled 表示是否监控主机可用内存低水位。
func (c *ActiveProfileTaskConfig) MemoryGuardEnabled() bool {
	return c.ResourceGuardConfig.AutoStopOnResourcePressure && c.ResourceGuardConfig.MinMemLevel > 0
}

func (c *ActiveProfileTaskConfig) cleanProcessSelector() error {
	// Go 不做进程匹配，协议约定配置中省略 process_selector。进程标签改为在采集后
	// 按 endpoint 端口反查监听进程补齐。
	if c.IsGolang() {
		c.ProcessSelector.excludeRegexp = nil
		if c.ProcessSelector.Type != "" {
			return fmt.Errorf("process_selector must be omitted when collection_method=%s", ActiveProfileMethodGolang)
		}
		return nil
	}
	if err := c.ProcessSelector.Clean(); err != nil {
		return err
	}
	c.ProcessSelector.bindHost(c.BKHostID)
	return nil
}

// bindHost 从 host_pids 取出属于本机的 PID。其他主机的条目在本机匹配不上，按未匹配到
// 进程处理。
func (s *ActiveProfileProcessSelector) bindHost(bkHostID int64) {
	if s.Type != ActiveProfileSelectorCommand || s.EnableDynamicCollection {
		return
	}
	s.targetPIDs = nil
	for _, item := range s.HostPIDs {
		if item.BKHostID == bkHostID {
			s.targetPIDs = append(s.targetPIDs, item.PID)
		}
	}
}

// Clean 校验并归一化进程选择器。它是三种进程定位方式的唯一校验入口：下发配置的
// Clean 与匹配预览命令都经由它，两者不可能对同一份选择器给出不同判断。
//
// 注意 exclude_pattern 用 Go 的 regexp 编译，即 RE2 语法——不支持 lookahead 与反向
// 引用。平台侧校验必须按同一套语法，否则通过了平台校验的表达式会在这里编译失败。
func (s *ActiveProfileProcessSelector) Clean() error {
	s.excludeRegexp = nil
	s.pidPaths = nil
	s.targetPIDs = nil
	switch s.Type {
	case ActiveProfileSelectorCommand:
		s.MatchPattern = strings.TrimSpace(s.MatchPattern)
		if s.MatchPattern == "" {
			return fmt.Errorf("process_selector.match_pattern is required when type=command")
		}
		if utf8.RuneCountInString(s.MatchPattern) > ActiveProfileMaxSelectorTextLength {
			return fmt.Errorf("process_selector.match_pattern must not exceed %d characters", ActiveProfileMaxSelectorTextLength)
		}
		s.ExcludePattern = strings.TrimSpace(s.ExcludePattern)
		if utf8.RuneCountInString(s.ExcludePattern) > ActiveProfileMaxSelectorTextLength {
			return fmt.Errorf("process_selector.exclude_pattern must not exceed %d characters", ActiveProfileMaxSelectorTextLength)
		}
		if s.ExcludePattern != "" {
			compiled, err := regexp.Compile(s.ExcludePattern)
			if err != nil {
				return fmt.Errorf("compile process_selector.exclude_pattern: %w", err)
			}
			s.excludeRegexp = compiled
		}
		if !s.EnableDynamicCollection {
			return s.cleanHostPIDs()
		}
	case ActiveProfileSelectorPIDFile:
		return s.cleanPIDPath()
	case ActiveProfileSelectorPID:
		return s.cleanPIDs()
	case ActiveProfileSelectorSystem:
		// 本地调试用的 system 范围采集，不属于下发协议。
	default:
		return fmt.Errorf("unsupported process_selector.type %q", s.Type)
	}
	return nil
}

// splitSelectorValues 按分号拆分，去掉首尾空白与空项。
func splitSelectorValues(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ActiveProfileSelectorSeparator) {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// cleanPIDPath 校验并拆分 pid_path。选择器原样进入每条 Event 的配置快照，路径按字符
// 计数、JSON 转义后最坏每字符 6 字节，整串长度上限保证快照放得进 event.content。
func (s *ActiveProfileProcessSelector) cleanPIDPath() error {
	if utf8.RuneCountInString(s.PIDPath) > ActiveProfileMaxSelectorPIDPathLength {
		return fmt.Errorf("process_selector.pid_path must not exceed %d characters", ActiveProfileMaxSelectorPIDPathLength)
	}
	s.pidPaths = splitSelectorValues(s.PIDPath)
	if len(s.pidPaths) == 0 {
		return fmt.Errorf("process_selector.pid_path is required when type=pid_file")
	}
	if len(s.pidPaths) > ActiveProfileMaxSelectorPIDPaths {
		return fmt.Errorf("process_selector.pid_path must not contain more than %d paths", ActiveProfileMaxSelectorPIDPaths)
	}
	for _, pidPath := range s.pidPaths {
		if utf8.RuneCountInString(pidPath) > ActiveProfileMaxSelectorTextLength {
			return fmt.Errorf("process_selector.pid_path item must not exceed %d characters", ActiveProfileMaxSelectorTextLength)
		}
	}
	return nil
}

// cleanPIDs 校验并拆分 pids。
func (s *ActiveProfileProcessSelector) cleanPIDs() error {
	items := splitSelectorValues(s.PIDs)
	if len(items) == 0 {
		return fmt.Errorf("process_selector.pids is required when type=pid")
	}
	if len(items) > ActiveProfileMaxSelectorPIDs {
		return fmt.Errorf("process_selector.pids must not exceed %d", ActiveProfileMaxSelectorPIDs)
	}
	s.targetPIDs = make([]int32, 0, len(items))
	for _, item := range items {
		pid, err := strconv.ParseInt(item, 10, 32)
		if err != nil || pid <= 0 {
			return fmt.Errorf("process_selector.pids must all be positive integers, got %q", item)
		}
		s.targetPIDs = append(s.targetPIDs, int32(pid))
	}
	return nil
}

// cleanHostPIDs 校验关闭动态采集的 command 勾选的进程。host_pids 原样进入每条 Event
// 的配置快照，数量受 event.content 上限约束。
func (s *ActiveProfileProcessSelector) cleanHostPIDs() error {
	if len(s.HostPIDs) == 0 {
		return fmt.Errorf("process_selector.host_pids is required when enable_dynamic_collection=false")
	}
	if len(s.HostPIDs) > ActiveProfileMaxSelectorHostPIDs {
		return fmt.Errorf("process_selector.host_pids must not exceed %d", ActiveProfileMaxSelectorHostPIDs)
	}
	for _, item := range s.HostPIDs {
		if item.BKHostID <= 0 || item.PID <= 0 {
			return fmt.Errorf("process_selector.host_pids must all have positive bk_host_id and pid")
		}
	}
	return nil
}

func (c *ActiveProfileTaskConfig) cleanSchedule() error {
	schedule := &c.Schedule
	if schedule.Timezone == "" {
		schedule.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(schedule.Timezone); err != nil {
		return fmt.Errorf("load timezone %q: %w", schedule.Timezone, err)
	}
	c.executeAt = time.Time{}
	c.effectiveStartAt = time.Time{}
	c.effectiveEndAt = time.Time{}
	c.cronSchedule = nil

	switch schedule.Type {
	case ActiveProfileScheduleImmediate, ActiveProfileScheduleOnceAt:
		if schedule.ExecuteAt <= 0 {
			return fmt.Errorf("schedule.execute_at must be positive when type=%s", schedule.Type)
		}
		if schedule.CronExpression != "" {
			return fmt.Errorf("schedule.cron_expression must be empty when type=%s", schedule.Type)
		}
		c.executeAt = time.UnixMilli(schedule.ExecuteAt)
	case ActiveProfileScheduleCron:
		if schedule.CronExpression == "" {
			return fmt.Errorf("schedule.cron_expression is required when type=cron")
		}
		if schedule.ExecuteAt != 0 {
			return fmt.Errorf("schedule.execute_at must be empty when type=cron")
		}
		schedule.CronExpression = strings.Join(strings.Fields(schedule.CronExpression), " ")
		parsed, err := cron.ParseStandard("CRON_TZ=" + schedule.Timezone + " " + schedule.CronExpression)
		if err != nil {
			return fmt.Errorf("parse schedule.cron_expression: %w", err)
		}
		c.cronSchedule = parsed
		if schedule.EffectiveStartAt < 0 || schedule.EffectiveEndAt < 0 {
			return fmt.Errorf("schedule effective window must not be negative")
		}
		if schedule.EffectiveStartAt > 0 {
			c.effectiveStartAt = time.UnixMilli(schedule.EffectiveStartAt)
		}
		if schedule.EffectiveEndAt > 0 {
			c.effectiveEndAt = time.UnixMilli(schedule.EffectiveEndAt)
		}
		if !c.effectiveStartAt.IsZero() && !c.effectiveEndAt.IsZero() &&
			c.effectiveEndAt.Before(c.effectiveStartAt) {
			return fmt.Errorf("schedule.effective_end_at must not be earlier than effective_start_at")
		}
	default:
		return fmt.Errorf("unsupported schedule.type %q", schedule.Type)
	}
	return nil
}

// cleanProfileTarget 校验 pprof 上报载荷里的产物归属。两个值与 DataID 一样由平台
// 注入，缺失时产物无法在 Profiling/BKBase 侧定位，因此在入口就拒绝。
func (c *ActiveProfileTaskConfig) cleanProfileTarget() error {
	c.ProfileAppName = strings.TrimSpace(c.ProfileAppName)
	if c.ProfileAppName == "" {
		return fmt.Errorf("profile_app_name is required")
	}
	c.ProfileServiceName = strings.TrimSpace(c.ProfileServiceName)
	if c.ProfileServiceName == "" {
		return fmt.Errorf("profile_service_name is required")
	}
	return nil
}

// IsPlanned 表示该任务由常驻 Beat 调度，其 started 与 finished 都由 Beat 上报。
func (c *ActiveProfileTaskConfig) IsPlanned() bool {
	return c.Schedule.Type == ActiveProfileScheduleOnceAt || c.Schedule.Type == ActiveProfileScheduleCron
}

// GetScheduleIdent identifies the schedule independently from the full config
// ident used by reload diffing. Credentials and sampling parameters are
// deliberately excluded so changing them cannot repeat a dispatched occurrence.
func (c *ActiveProfileTaskConfig) GetScheduleIdent() string {
	prefix := fmt.Sprintf(
		"active_profile|bk_tenant_id=%s|bk_biz_id=%d|task_id=%d|schedule_type=%s",
		c.BKTenantID,
		c.BizID,
		c.TaskID,
		c.Schedule.Type,
	)
	switch c.Schedule.Type {
	case ActiveProfileScheduleOnceAt:
		return fmt.Sprintf("%s|execute_at=%d", prefix, c.Schedule.ExecuteAt)
	case ActiveProfileScheduleCron:
		return fmt.Sprintf(
			"%s|cron_expression=%s|timezone=%s|effective_start_at=%d|effective_end_at=%d",
			prefix,
			c.Schedule.CronExpression,
			c.Schedule.Timezone,
			c.Schedule.EffectiveStartAt,
			c.Schedule.EffectiveEndAt,
		)
	default:
		return prefix
	}
}

// ScheduleExpired identifies a once_at task which Beat first loaded after its
// absolute execution time. The scheduler still dispatches it once so Gather can
// report a terminal failed Event instead of leaving an empty execution history.
func (c *ActiveProfileTaskConfig) ScheduleExpired(now time.Time) bool {
	return c.Schedule.Type == ActiveProfileScheduleOnceAt && !c.executeAt.After(now)
}

// ScheduledAt is a fallback for execution paths which do not provide the exact
// planned occurrence in context. immediate 与 once_at 的 scheduled_at 与其
// execute_at 数值相同；cron 每轮由调度器计算。
func (c *ActiveProfileTaskConfig) ScheduledAt(startedAt time.Time) time.Time {
	if !c.executeAt.IsZero() {
		return c.executeAt
	}
	return startedAt
}

func (c *ActiveProfileTaskConfig) InitialRunTime(now time.Time) (time.Time, bool) {
	switch c.Schedule.Type {
	case ActiveProfileScheduleOnceAt:
		return c.executeAt, true
	case ActiveProfileScheduleCron:
		from := now
		if !c.effectiveStartAt.IsZero() && c.effectiveStartAt.After(from) {
			from = c.effectiveStartAt
		}
		return c.nextCronRunTime(from)
	default:
		return time.Time{}, false
	}
}

func (c *ActiveProfileTaskConfig) NextRunTime(previous time.Time) (time.Time, bool) {
	if c.Schedule.Type != ActiveProfileScheduleCron || c.cronSchedule == nil {
		return time.Time{}, false
	}
	return c.nextCronRunTime(previous)
}

// nextCronRunTime 在生效窗口内计算下一次触发时间；越过结束时间即表示该 Cron 结束。
func (c *ActiveProfileTaskConfig) nextCronRunTime(from time.Time) (time.Time, bool) {
	next := c.cronSchedule.Next(from)
	if next.IsZero() {
		return time.Time{}, false
	}
	if !c.effectiveEndAt.IsZero() && next.After(c.effectiveEndAt) {
		return time.Time{}, false
	}
	return next, true
}

type ActiveProfileMetaConfig struct {
	BaseTaskMetaParam `config:"_,inline"`
	Tasks             []*ActiveProfileTaskConfig `config:"tasks"`
}

func (c *ActiveProfileMetaConfig) Clean() error {
	if err := c.BaseTaskMetaParam.CleanParams(); err != nil {
		return err
	}
	// max_timeout is a generic child-config field. SaaS does not need to send
	// it for Active Profile, and a manually supplied smaller value must not
	// truncate the internally derived per-run timeout.
	minimumMaxTimeout := time.Duration(ActiveProfileMaxDurationSec)*time.Second +
		ActiveProfilePostProcessAllowance
	if c.MaxTimeout < minimumMaxTimeout {
		c.MaxTimeout = minimumMaxTimeout
	}
	for _, task := range c.Tasks {
		if err := c.CleanTask(task); err != nil {
			return err
		}
	}
	return nil
}

func (c *ActiveProfileMetaConfig) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, len(c.Tasks))
	for i, task := range c.Tasks {
		tasks[i] = task
	}
	return tasks
}

func NewActiveProfileMetaConfig(root *Config) *ActiveProfileMetaConfig {
	config := &ActiveProfileMetaConfig{
		BaseTaskMetaParam: NewBaseTaskMetaParam(),
		Tasks:             make([]*ActiveProfileTaskConfig, 0),
	}
	config.MaxTimeout = 2 * time.Hour
	config.MinPeriod = time.Minute
	root.TaskTypeMapping[ConfigTypeActiveProfile] = config
	return config
}
