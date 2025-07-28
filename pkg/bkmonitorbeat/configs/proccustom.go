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
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ProcCustomConfig struct {
	BaseTaskParam        `config:"_,inline"`
	PortDataID           int      `config:"port_dataid"`
	MatchPattern         string   `config:"match_pattern"`   // 匹配正则，只做匹配工作，不提取维度
	ProcessName          string   `config:"process_name"`    // 进程名匹配规则,作为多进程时的区分维度
	DimPattern           string   `config:"extract_pattern"` // 额外维度提取正则，只提取维度，不做匹配工作
	ExcludePattern       string   `config:"exclude_pattern"` // 除外正则，匹配该正则的进程被除外不上报
	PIDPath              string   `config:"pid_path"`
	ProcMetric           []string `config:"proc_metric"`
	PortDetect           bool     `config:"port_detect"`
	Ports                []string `config:"ports"`
	ListenPortOnly       bool     `config:"listen_port_only"`       // 只采集监听端口
	ReportUnexpectedPort bool     `config:"report_unexpected_port"` // 如果配置了指定端口，是否对额外端口信息进行上报
	DisableMapping       bool     `config:"disable_mapping"`        // 不进行 pid 映射

	nameRegx    *regexp.Regexp
	dimsRegx    *regexp.Regexp
	excludeRegx *regexp.Regexp
	matchRegx   *regexp.Regexp
}

func NewProcCustomConfig(root *Config) *ProcCustomConfig {
	config := &ProcCustomConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[define.ModuleProcCustom] = config
	return config
}

func (c *ProcCustomConfig) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, 0)
	// 如果不存在采集 dataid 则没必要生成采集配置
	if c.DataID == 0 {
		return tasks
	}

	tasks = append(tasks, c)
	return tasks
}

func (c *ProcCustomConfig) Setup() {
	var err error
	if c.ProcessName != "" {
		c.nameRegx, err = regexp.Compile(c.ProcessName)
		if err != nil {
			logger.Errorf("failed to compile ProcessName regex:%v, err:%v", c.ProcessName, err)
		}
	}

	if c.ExcludePattern != "" {
		c.excludeRegx, err = regexp.Compile(c.ExcludePattern)
		if err != nil {
			logger.Errorf("failed to compile ExcludePattern regex:%v, err:%v", c.ExcludePattern, err)
		}
	}

	if c.DimPattern != "" {
		c.dimsRegx, err = regexp.Compile(c.DimPattern)
		if err != nil {
			logger.Errorf("failed to compile DimPattern regex:%v, err:%v", c.DimPattern, err)
		}
	}
	if c.MatchPattern != "" {
		c.matchRegx, err = regexp.Compile(c.MatchPattern)
		if err != nil {
			logger.Errorf("failed to compile MatchPattern regex:%v, err:%v", c.MatchPattern, err)
		}
	}
}

func (c *ProcCustomConfig) EnablePortCollected() bool { return c.PortDetect && c.PortDataID > 0 }

func (c *ProcCustomConfig) EnablePerfCollected() bool { return c.DataID > 0 }

func (c *ProcCustomConfig) GetPeriod() time.Duration { return c.Period }

func (c *ProcCustomConfig) InitIdent() error { return c.initIdent(c) }

func (c *ProcCustomConfig) GetType() string { return define.ModuleProcCustom }

func (c *ProcCustomConfig) Clean() error { return c.InitIdent() }

func (c *ProcCustomConfig) Match(procs []define.ProcStat) []define.ProcStat {
	var ret []define.ProcStat
	for _, p := range procs {
		if c.match(p.Cmd) {
			ret = append(ret, p)
		}
	}

	return ret
}

func (c *ProcCustomConfig) match(name string) bool {
	// 如果匹配到了除外正则，则跳过该进程
	if c.excludeRegx != nil && c.excludeRegx.MatchString(name) {
		// 如果匹配到了除外正则，则跳过该进程
		logger.Debug("proccustom: exclude case matched.")
		return false
	}

	if c.matchRegx == nil {
		return false
	}
	return c.matchRegx.MatchString(name)
}

func (c *ProcCustomConfig) ExtractDimensions(name string) map[string]string {
	ret := make(map[string]string)
	if c.dimsRegx == nil {
		return ret
	}

	names := c.dimsRegx.SubexpNames()
	logger.Debugf("proccustom: dimension regex subnames: %v", names)
	// 获取所有维度分组，并取最后匹配到的不为空的字符串作为实际上报的信息
	subMatches := c.dimsRegx.FindAllStringSubmatch(name, -1)
	for _, subMatch := range subMatches {
		for index, matchInstance := range subMatch {
			// 第一个匹配项略过
			if index == 0 {
				continue
			}
			// 根据维度名对应关系，填充额外维度信息
			if names[index] != "" {
				ret[names[index]] = matchInstance
			}
		}
	}
	return ret
}

func (c *ProcCustomConfig) ExtractProcessName(name string) string {
	// 如果未配置 process_name 则获取基础二进制名上报
	if c.nameRegx == nil {
		fields := strings.Fields(name)
		baseName := filepath.Base(fields[0])
		return baseName
	}

	// 如果没有传入分组，则将配置提供的字符串作为 process_name 维度上报
	if c.nameRegx.NumSubexp() == 0 {
		return c.nameRegx.String()
	}

	var last string
	// 获取所有维度分组，并取最后一个
	subMatches := c.nameRegx.FindAllStringSubmatch(name, -1)
	for _, subMatch := range subMatches {
		for _, matchInstance := range subMatch {
			last = matchInstance
		}
	}
	return last
}
