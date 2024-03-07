// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"strings"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

const (
	EncodingUTF8 = "utf-8"
	EncodingGBK  = "gbk"

	// 任务类型
	TaskTypeRawLog  = "raw_log"
	TaskTypeKeyword = "keyword"

	ConfigTypeKeyword = define.ModuleKeyword

	// 结果聚合发送方式
	OutputFormatEvent      = "event"
	DefaultRetainFileBytes = 1024 * 1024 // 1MB
)

// 日志关键字匹配规则配置
type KeywordConfig struct {
	Name    string `config:"name"`    // 匹配规则名
	Pattern string `config:"pattern"` // 正则匹配规则
}

// 采集下发来源配置说明
type Label struct {
	BkCollectConfigID         string `config:"bk_collect_config_id"`
	BkTargetCloudID           string `config:"bk_target_cloud_id"`
	BkTargetIP                string `config:"bk_target_ip"`
	BKTargetServiceCategoryID string `config:"bk_target_service_category_id"`
	BKTargetServiceInstanceID string `config:"bk_target_service_instance_id"`
	BkTargetTopoID            string `config:"bk_target_topo_id"`
	BkTargetTopoLevel         string `config:"bk_target_topo_level"`
}

func (l *Label) Id() string {
	// 数据比较简单，暂时使用拼接的形式，如果有一些复杂的数据，建议修改这里的实现
	return strings.Join([]string{
		l.BkCollectConfigID,
		l.BkTargetTopoLevel,
		l.BkTargetTopoID,
		l.BkTargetCloudID,
		l.BkTargetIP,
		l.BKTargetServiceCategoryID,
		l.BKTargetServiceInstanceID,
	}, "||")
}

func (l *Label) AsMapStr() common.MapStr {
	return common.MapStr{
		"bk_collect_config_id":          l.BkCollectConfigID,
		"bk_target_topo_level":          l.BkTargetTopoLevel,
		"bk_target_topo_id":             l.BkTargetTopoID,
		"bk_target_cloud_id":            l.BkTargetCloudID,
		"bk_target_ip":                  l.BkTargetIP,
		"bk_target_service_category_id": l.BKTargetServiceCategoryID,
		"bk_target_service_instance_id": l.BKTargetServiceInstanceID,
	}
}

// 是否为按服务实例下发的Label
func (l *Label) IsServiceInstance() bool {
	return l.BKTargetServiceInstanceID != ""
}

// 是否为按主机，动态节点下发的Label
func (l *Label) IsHostDynamicTopoNode() bool {
	return !l.IsServiceInstance() && l.BkTargetTopoLevel != "" && l.BkTargetTopoID != ""
}

// 是否按静态主机下发的Label
func (l *Label) IsHostStaticIp() bool {
	return !l.IsHostDynamicTopoNode() && l.BkTargetIP != ""
}

type KeywordTaskConfig struct {
	BaseTaskParam `config:"_,inline"`
	DataID        int `config:"dataid"` // 上报数据ID
	// Type           string          `config:"type"`           // 任务类型
	Paths         []string      `config:"paths"`          // 采集的文件路径
	ExcludeFiles  []string      `config:"exclude_files"`  // 需要过滤的文件列表，正则表示
	Encoding      string        `config:"encoding"`       // 文件编码类型
	ScanSleep     time.Duration `config:"scan_sleep"`     // 文件扫描休眠时间，用于减少CPU负载，默认1us
	CloseInactive time.Duration `config:"close_inactive"` // 文件未更新需要关闭的超时等待
	// Delimiter      string          `config:"delimiter"`      // 行文件的分隔符
	ReportPeriod    time.Duration   `config:"report_period"`   // 上报周期
	FilterPatterns  []string        `config:"filter_patterns"` // 过滤规则
	KeywordConfigs  []KeywordConfig `config:"keywords"`        // 日志关键字匹配规则
	OutputFormat    string          `config:"output_format"`   // 结果输出格式
	Target          string          `config:"target"`          // 采集目标
	TimeUnit        string          `config:"time_unit"`       // 上报时间单位，默认是ms
	Label           []Label         `config:"labels"`
	RetainFileBytes int64           `config:"retain_file_bytes"` // 保留前置文件的尾部数据
}

func (c *KeywordTaskConfig) InitIdent() error {
	return c.initIdent(c)
}

func (c *KeywordTaskConfig) GetType() string {
	return ConfigTypeKeyword
}

func (c *KeywordTaskConfig) Clean() error {
	err := utils.CleanCompositeParamList(&c.BaseTaskParam)
	if err != nil {
		return err
	}
	if c.Encoding == "" {
		c.Encoding = EncodingUTF8
	}
	if c.ScanSleep == 0 {
		c.ScanSleep = time.Microsecond
	}
	if c.OutputFormat == "" {
		c.OutputFormat = OutputFormatEvent
	}
	for i, path := range c.Paths {
		c.Paths[i] = strings.TrimSpace(path)
	}
	if c.ExcludeFiles == nil || len(c.ExcludeFiles) <= 0 {
		c.ExcludeFiles = []string{".gz$", ".tar$", ".bz2$"}
	}
	if c.CloseInactive == 0 {
		c.CloseInactive = 1 * time.Hour
	}
	if c.TimeUnit == "" {
		c.TimeUnit = "ms"
	}
	if c.RetainFileBytes == 0 {
		c.RetainFileBytes = DefaultRetainFileBytes
	}
	if c.RetainFileBytes < 0 {
		c.RetainFileBytes = 0
	}
	return nil
}

// KeywordTaskMetaConfig
type KeywordTaskMetaConfig struct {
	BaseTaskMetaParam `config:"_,inline"`

	Tasks []*KeywordTaskConfig `config:"tasks"`
}

func (c *KeywordTaskMetaConfig) GetTaskConfigList() []define.TaskConfig {
	count := len(c.Tasks)
	tasks := make([]define.TaskConfig, count)
	for index, task := range c.Tasks {
		tasks[index] = task
	}
	return tasks
}

func (c *KeywordTaskMetaConfig) Clean() error {
	err := utils.CleanCompositeParamList(&c.BaseTaskMetaParam)
	if err != nil {
		return err
	}
	for _, task := range c.Tasks {
		err = c.CleanTask(task)
		if err != nil {
			return err
		}

	}
	return nil
}

// NewKeywordTaskMetaConfig :
func NewKeywordTaskMetaConfig(root *Config) *KeywordTaskMetaConfig {
	config := &KeywordTaskMetaConfig{
		BaseTaskMetaParam: NewBaseTaskMetaParam(),
	}
	config.Tasks = make([]*KeywordTaskConfig, 0)
	root.TaskTypeMapping[ConfigTypeKeyword] = config

	return config
}
