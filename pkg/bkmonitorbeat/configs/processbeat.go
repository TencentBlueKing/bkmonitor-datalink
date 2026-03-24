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
	"crypto/md5"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tenant"
)

type ProcessbeatConfig struct {
	BaseTaskParam        `config:"_,inline" yaml:"-"`
	Type                 string                  `config:"type" yaml:"type"`
	Name                 string                  `config:"name" yaml:"name"`
	TaskId               int32                   `config:"task_id" yaml:"task_id"`
	Version              string                  `config:"version" yaml:"version"`
	PortDataId           int32                   `config:"portdataid" yaml:"portdataid"`
	TopDataId            int32                   `config:"topdataid" yaml:"topdataid"`
	PerfDataId           int32                   `config:"perfdataid" yaml:"perfdataid"`
	Period               time.Duration           `config:"period" yaml:"period"`
	HostFilePath         string                  `config:"hostfilepath" yaml:"hostfilepath"`
	Processes            []ProcessbeatPortConfig `config:"processes" yaml:"processes"`
	CmdbLevelMaxLength   int                     `config:"cmdb_level_max_length" yaml:"cmdb_level_max_length"`
	IgnoreCmdbLevel      bool                    `config:"ignore_cmdb_level" yaml:"ignore_cmdb_level"`
	MustHostIDExist      bool                    `config:"must_host_id_exist" yaml:"must_host_id_exist"`
	MonitorCollectorPath string                  `config:"monitor_collector_path" yaml:"monitor_collector_path"`
	ConvergePID          bool                    `config:"converge_pid" yaml:"converge_pid"`
	MaxNoListenPorts     int                     `config:"max_nolisten_ports" yaml:"max_nolisten_ports`
	Disable              bool                    `config:"disable" yaml:"disable"`

	namestore map[string][]ProcessbeatPortConfig // name -> configs
	confs     map[string]ProcessbeatPortConfig   // id -> config
}

func NewProcessbeatConfig(root *Config) *ProcessbeatConfig {
	config := &ProcessbeatConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[define.ModuleProcessbeat] = config
	return config
}

func (c *ProcessbeatConfig) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, 0)
	// 如果禁用或不存在采集 dataid 则没必要生成采集配置
	if c.Disable || (c.PortDataId == 0 && c.TopDataId == 0 && c.PerfDataId == 0) {
		return tasks
	}

	tasks = append(tasks, c)
	return tasks
}

func (c *ProcessbeatConfig) EnableTopCollected() bool { return c.TopDataId != 0 }

func (c *ProcessbeatConfig) EnablePortCollected() bool { return c.PortDataId != 0 }

func (c *ProcessbeatConfig) EnablePerfCollected() bool { return c.PerfDataId != 0 }

func (c *ProcessbeatConfig) GetPeriod() time.Duration { return c.Period }

func (c *ProcessbeatConfig) InitIdent() error {
	// 需要在计算 indent 之前进行 dataid 替换 否则 reload 不会生效

	storage := tenant.DefaultStorage()
	if v, ok := storage.GetTaskDataID(define.ModuleProcessbeat + "_port"); ok {
		c.PortDataId = v
	}
	if v, ok := storage.GetTaskDataID(define.ModuleProcessbeat + "_top"); ok {
		c.TopDataId = v
	}
	if v, ok := storage.GetTaskDataID(define.ModuleProcessbeat + "_perf"); ok {
		c.PerfDataId = v
	}

	return c.initIdent(c)
}

func (c *ProcessbeatConfig) GetType() string { return define.ModuleProcessbeat }

func (c *ProcessbeatConfig) Clean() error { return c.InitIdent() }

func (c *ProcessbeatConfig) GetConfigByID(id string) ProcessbeatPortConfig {
	return c.confs[id]
}

func (c *ProcessbeatConfig) Setup() {
	c.namestore = map[string][]ProcessbeatPortConfig{}
	c.confs = map[string]ProcessbeatPortConfig{}
	for _, p := range c.Processes {
		c.namestore[p.Name] = append(c.namestore[p.Name], p)
		c.confs[p.ID()] = p
	}
}

func (c *ProcessbeatConfig) MatchNotExists(exists map[string]struct{}) []ProcessbeatPortConfig {
	var notExists []ProcessbeatPortConfig
	for _, p := range c.Processes {
		if _, ok := exists[p.ID()]; !ok {
			notExists = append(notExists, p)
		}
	}

	return notExists
}

// MatchRegex 匹配进程的信息和参数是否一致
// names: key 为进程的不同信息，包括: exe/exe 的 base name/cmdline 的 base name 等，具体参考 MatchNames 函数
// param: 具体匹配进程的参数信息
func (c *ProcessbeatConfig) MatchRegex(names map[string]struct{}, param string) []ProcessbeatPortConfig {
	var ret []ProcessbeatPortConfig
	for name := range names {
		for _, p := range c.namestore[name] {
			if p.ParamRegex != "" {
				matched, err := regexp.MatchString(p.ParamRegex, param)
				if err != nil || !matched {
					continue
				}
			}
			ret = append(ret, p)
		}
	}
	return ret
}

// MatchNames 分析进程的信息，将进程的启动 exe 名等提取为 map，方便后面判断使用
func (c *ProcessbeatConfig) MatchNames(proc common.MapStr) map[string]struct{} {
	names := make(map[string]struct{})
	names[proc["name"].(string)] = struct{}{}

	exe := proc["exe"].(string)
	names[exe] = struct{}{}
	names[filepath.Base(exe)] = struct{}{}

	cmd := proc["cmdline"].(string)
	var cmdline []string
	for _, part := range strings.Split(cmd, " ") {
		cmdline = append(cmdline, part)
	}
	if len(cmdline) > 0 {
		names[cmdline[0]] = struct{}{}
		names[filepath.Base(cmdline[0])] = struct{}{}
	}

	return names
}

type ProcessbeatPortConfig struct {
	Name         string                `config:"name" yaml:"name"`
	DisplayName  string                `config:"displayname" yaml:"displayname"`
	Protocol     string                `config:"protocol" yaml:"protocol"`
	Ports        []uint16              `config:"ports" yaml:"ports"`
	ParamRegex   string                `config:"paramregex" yaml:"paramregex"`
	BindIP       string                `config:"bindip" yaml:"bindip"`
	BindInfoList []ProcessbeatBindInfo `config:"bind_info" yaml:"bind_info"`
}

type ProcessbeatBindInfo struct {
	IP       string   `config:"ip" yaml:"ip"`
	Ports    []uint16 `config:"ports" yaml:"ports"`
	Protocol string   `config:"protocol" yaml:"protocol"`
}

func (c ProcessbeatPortConfig) ID() string {
	bs, _ := json.Marshal(c)
	return fmt.Sprintf("%x", md5.Sum(bs))
}

func (c ProcessbeatPortConfig) GetBindDetailed() []ProcessbeatBindInfo {
	portconfs := make([]ProcessbeatBindInfo, 0)
	v1conf := c.BindIP != "" && c.Protocol != "" && len(c.Ports) != 0
	if v1conf {
		portconfs = append(portconfs, ProcessbeatBindInfo{
			IP:       c.BindIP,
			Ports:    c.Ports,
			Protocol: c.Protocol,
		})
	}

	portconfs = append(portconfs, c.BindInfoList...)
	portconfs = groupProcessbeatBindInfo(portconfs)

	return portconfs
}

func groupProcessbeatBindInfo(infos []ProcessbeatBindInfo) []ProcessbeatBindInfo {
	// group by ip and protocol
	ipAndProtocolMap := make(map[string]map[string]ProcessbeatBindInfo)
	for _, info := range infos {
		if protocolMap, ok := ipAndProtocolMap[info.IP]; ok {
			if existInfo, ok2 := protocolMap[info.Protocol]; ok2 {
				existInfo.Ports = append(existInfo.Ports, info.Ports...)
				protocolMap[info.Protocol] = existInfo
			} else {
				protocolMap[info.Protocol] = info
			}
		} else {
			ipAndProtocolMap[info.IP] = map[string]ProcessbeatBindInfo{
				info.Protocol: info,
			}
		}
	}
	// format ports
	result := make([]ProcessbeatBindInfo, 0)
	for _, m := range ipAndProtocolMap {
		for _, info := range m {
			info.Ports = formatPorts(info.Ports)
			result = append(result, info)
		}
	}
	return result
}

func formatPorts(s []uint16) []uint16 {
	if len(s) < 2 {
		return s
	}
	// sort
	sort.Slice(s, func(i, j int) bool {
		return s[i] < s[j]
	})
	// remove duplicate
	tmp := make([]uint16, 0, len(s))
	for i := 0; i < len(s); i++ {
		if i == 0 || s[i] != s[i-1] {
			tmp = append(tmp, s[i])
		}
	}
	return tmp
}
