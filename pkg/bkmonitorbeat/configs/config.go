// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Config is put into a different package to prevent cyclic imports in case
// it is needed in several locations

package configs

import (
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tenant"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var isContainerMode = false

var isContainerModeLock sync.RWMutex

var DisableNetlink bool // 是否禁用 netlink 禁用：true 不禁用 false，默认 false

func SetContainerMode(v bool) {
	isContainerModeLock.Lock()
	defer isContainerModeLock.Unlock()
	isContainerMode = v
}

func IsContainerMode() bool {
	isContainerModeLock.RLock()
	defer isContainerModeLock.RUnlock()
	return isContainerMode
}

// TaskConcurrencyLimitConfig 任务并发限制配置
type TaskConcurrencyLimitConfig struct {
	PerInstanceLimit int64 `config:"per_instance"` // 全局限制
	PerTaskLimit     int64 `config:"per_task"`     // 单任务限制
}

// Clean 初始化参数
func (tcc *TaskConcurrencyLimitConfig) Clean() {
	if tcc.PerInstanceLimit == 0 {
		tcc.PerInstanceLimit = define.DefaultTaskConcurrencyLimitPerInstance
	}
	if tcc.PerTaskLimit == 0 {
		tcc.PerInstanceLimit = define.DefaultTaskConcurrencyLimitPerTask
	}
}

// ConcurrencyLimitConfig 并发限制配置
type ConcurrencyLimitConfig struct {
	Task map[string]*TaskConcurrencyLimitConfig `config:"task"`
}

// Clean 初始化参数
func (cc *ConcurrencyLimitConfig) Clean() {
	for _, tcc := range cc.Task {
		tcc.Clean()
	}
}

// Config : global config
type Config struct {
	TaskTypeMapping map[string]define.TaskMetaConfig `config:"_"`

	CheckInterval    time.Duration `config:"check_interval" validate:"positive"`
	CleanUpTimeout   time.Duration `config:"clean_up_timeout" validate:"min=1s"`
	EventBufferSize  int           `config:"event_buffer_size" validate:"positive"`
	Mode             string        `config:"mode" validate:"regexp=(daemon|check|cron)"`
	KeepOneDimension bool          `config:"keep_one_dimension"`
	MetricsBatchSize int           `config:"metrics_batch_size"`
	// 最大批次数，单个任务最大只能上报该批次*batch_size量级的数据
	MaxMetricBatches int    `config:"max_metric_batches"`
	AdminAddr        string `config:"admin_addr"`
	// 并发限制配置
	ConcurrencyLimit ConcurrencyLimitConfig `config:"concurrency_limit"`
	JsonLib          string                 `config:"jsonlib"`

	EnableMultiTenant  bool     `config:"enable_multi_tenant"`  // 是否启用多租户模式
	MultiTenantTasks   []string `config:"multi_tenant_tasks"`   // 多租户场景下需要映射的 task 列表
	GseMessageEndpoint string   `config:"gse_message_endpoint"` // gseagent 消息通信地址

	MetricbeatWorkers        int  `config:"metricbeat_workers"`
	MetricbeatSpreadWorkload bool `config:"metricbeat_spread_workload"`
	MetricbeatAlignTs        bool `config:"metricbeat_align_ts"`

	NodeID             string `config:"node_id" validate:"required"`
	IP                 string `config:"ip" validate:"nonzero"`
	BizID              int32  `config:"bk_biz_id" validate:"required"`
	CloudID            int32  `config:"bk_cloud_id" validate:"required"`
	HostIDPath         string `config:"host_id_path"`
	CmdbLevelMaxLength int    `config:"cmdb_level_max_length"`
	IgnoreCmdbLevel    bool   `config:"ignore_cmdb_level"`
	MustHostIDExist    bool   `config:"must_host_id_exist"`
	DisableNetLink     bool   `config:"disable_netlink"`

	TCPTask            *TCPTaskMetaConfig     `config:"tcp_task"`
	HeartBeat          *HeartBeatConfig       `config:"heart_beat"`
	GatherUpBeat       *GatherUpBeatConfig    `config:"gather_up_beat"`
	UDPTask            *UDPTaskMetaConfig     `config:"udp_task"`
	HTTPTask           *HTTPTaskMetaConfig    `config:"http_task"`
	ScriptTask         *ScriptTaskMetaConfig  `config:"script_task"`
	PingTask           *PingTaskMetaConfig    `config:"ping_task"`
	MetricTask         *MetricBeatMetaConfig  `config:"metricbeat_task"`
	KeywordTask        *KeywordTaskMetaConfig `config:"keyword_task"`
	TrapTask           *TrapMetaConfig        `config:"trap_task"`
	StaticTask         *StaticTaskMetaConfig  `config:"static_task"`
	BaseReportTask     *BasereportConfig      `config:"basereport_task"`
	ExceptionBeatTask  *ExceptionBeatConfig   `config:"exceptionbeat_task"`
	KubeeventTask      *KubeEventConfig       `config:"kubeevent_task"`
	ProcessBeatTask    *ProcessbeatConfig     `config:"processbeat_task"`
	ProcConfTask       *ProcConfig            `config:"procconf_task"`
	ProcCustomTask     *ProcCustomConfig      `config:"proccustom_task"`
	ProcSyncTask       *ProcSyncConfig        `config:"procsync_task"`
	ProcStatusTask     *ProcStatusConfig      `config:"procstatus_task"`
	LoginLogTask       *LoginLogConfig        `config:"loginlog_task"`
	ProcSnapshotTask   *ProcSnapshotConfig    `config:"procsnapshot_task"`
	ProcBinTask        *ProcBinConfig         `config:"procbin_task"`
	SocketSnapshotTask *SocketSnapshotConfig  `config:"socketsnapshot_task"`
	ShellHistoryTask   *ShellHistoryConfig    `config:"shellhistory_task"`
	RpmPackageTask     *RpmPackageConfig      `config:"rpmpackage_task"`
	TimeSyncTask       *TimeSyncConfig        `config:"timesync_task"`
	DmesgTask          *DmesgConfig           `config:"dmesg_task"`
	SelfStatsTask      *SelfStatsConfig       `config:"selfstats_task"`
}

// NewConfig : new config struct
func NewConfig() *Config {
	config := &Config{
		TaskTypeMapping:  make(map[string]define.TaskMetaConfig),
		CheckInterval:    500 * time.Millisecond,
		CleanUpTimeout:   time.Second,
		EventBufferSize:  10,
		Mode:             "check",
		KeepOneDimension: false,
		HeartBeat:        NewHeartBeatConfig(),
		GatherUpBeat:     NewGatherUpBeatConfig(),
	}
	config.TCPTask = NewTCPTaskMetaConfig(config)
	config.UDPTask = NewUDPTaskMetaConfig(config)
	config.HTTPTask = NewHTTPTaskMetaConfig(config)
	config.ScriptTask = NewScriptTaskMetaConfig(config)
	config.PingTask = NewPingTaskMetaConfig(config)
	config.MetricTask = NewMetricBeatMetaConfig(config)
	config.KeywordTask = NewKeywordTaskMetaConfig(config)
	config.TrapTask = NewTrapMetaConfig(config)
	config.StaticTask = NewStaticTaskMetaConfig(config)
	config.BaseReportTask = NewBasereportConfig(config)
	config.ExceptionBeatTask = NewExceptionBeatConfig(config)
	config.KubeeventTask = NewKubeEventConfig(config)
	config.ProcessBeatTask = NewProcessbeatConfig(config)
	config.ProcConfTask = NewProcConf(config)
	config.ProcCustomTask = NewProcCustomConfig(config)
	config.ProcSyncTask = NewProcSyncConfig(config)
	config.ProcStatusTask = NewProcStatusConfig(config)
	config.LoginLogTask = NewLoginLogConfig(config)
	config.ProcSnapshotTask = NewProcSnapshotConfig(config)
	config.ProcBinTask = NewProcBinConfig(config)
	config.SocketSnapshotTask = NewSocketSnapshotConfig(config)
	config.ShellHistoryTask = NewShellHistoryConfig(config)
	config.RpmPackageTask = NewRpmPackageConfig(config)
	config.TimeSyncTask = NewTimeSyncConfig(config)
	config.DmesgTask = NewDmesgConfig(config)
	config.SelfStatsTask = NewSelfStatsConfig(config)

	return config
}

// GetTaskTypeMapping :
func (c *Config) GetTaskTypeMapping() map[string]define.TaskMetaConfig {
	return c.TaskTypeMapping
}

// Clean :
func (c *Config) Clean() error {
	confs := c.GetTaskTypeMapping()
	for confType, conf := range confs {
		err := conf.Clean()
		if err != nil {
			logger.Errorf("clean config[%v] error: %v", confType, err)
			return err
		}
	}

	// 检查全局心跳配置，此处没有和上面一起用clean，是由于实现接口需要诸多开发，成本较高
	// 此处只是做一个心跳data id是否存在配置而已
	if c.Mode == "daemon" && c.HeartBeat.GlobalDataID == 0 && c.HeartBeat.DataID == 0 {
		logger.Errorf("failed to get heart_beat data_id, please check config.")
		return define.ErrUnpackCfg
	}

	c.ConcurrencyLimit.Clean()
	return nil
}

// GetTaskConfigListByType :
func (c *Config) GetTaskConfigListByType(configType string) []define.TaskConfig {
	confs := c.GetTaskTypeMapping()
	conf, ok := confs[configType]
	if !ok {
		panic(define.ErrType)
	}
	return conf.GetTaskConfigList()
}

// GetTaskConfigList :
func (c *Config) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, 0)
	confs := c.GetTaskTypeMapping()

	for confType := range confs {
		typeTasks := c.GetTaskConfigListByType(confType)
		for _, task := range typeTasks {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func (c *Config) GetGatherUpDataID() int32 {
	storage := tenant.DefaultStorage()
	if v, ok := storage.GetTaskDataID(define.ModuleGatherUpBeat); ok {
		return v
	}
	return c.GatherUpBeat.DataID
}
