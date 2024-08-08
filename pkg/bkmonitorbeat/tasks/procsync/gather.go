// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procsync

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cespare/xxhash"
	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ProcRawCommonConfig 旧版进程采集配置进程部分
type ProcRawCommonConfig struct {
	MatchPattern         string   `yaml:"match_pattern"`
	ProcessName          string   `yaml:"process_name"`
	ExtractPattern       string   `yaml:"extract_pattern"`
	ExcludePattern       string   `yaml:"exclude_pattern"`
	PIDPath              string   `yaml:"pid_path"`
	ProcMetric           []string `yaml:"proc_metric"`
	PortDetect           bool     `yaml:"port_detect"`
	Ports                []string `yaml:"ports"`
	ListenPortOnly       bool     `yaml:"listen_port_only"`
	ReportUnexpectedPort bool     `yaml:"report_unexpected_port"`
	DisableMapping       bool     `yaml:"disable_mapping"`
}

// ProcRawConfig 旧版进程采集配置
type ProcRawConfig struct {
	Type       string              `yaml:"type"`
	Config     ProcRawCommonConfig `yaml:"config"`
	DataID     int32               `yaml:"dataid"`
	PortDataID int32               `yaml:"port_dataid"`
	Labels     []map[string]string `yaml:"labels"`
	Tags       map[string]string   `yaml:"tags"`
	Period     string              `yaml:"period"`
}

type Gather struct {
	config *configs.ProcSyncConfig
	srcDir string
	tasks.BaseTask
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.ProcSyncConfig)
	gather.srcDir = filepath.Join(filepath.Dir(gather.config.DstDir), "processbeat")

	gather.Init()

	logger.Info("New a ProcSync Task Instance")
	return gather
}

// Run 主入口，同步旧版采集器配置到自定义采集
func (g *Gather) Run(_ context.Context, _ chan<- define.Event) {
	logger.Info("ProcSync is running....")
	srcCfgs, err := g.readSrcCfgs()
	if err != nil {
		logger.Errorf("failed to read custom srcCfgs: %v", err)
		return
	}
	if len(srcCfgs) == 0 {
		logger.Info("empty srcCfgs, skip")
		return
	}

	dstCfgs, err := g.readDstCfgs()
	if err != nil {
		logger.Errorf("failed to read custom dstCfgs: %v", err)
		return
	}

	if g.IsModify(srcCfgs, dstCfgs) {
		g.deleteDstCfgs(dstCfgs)
		g.writeDstCfgs(srcCfgs)
	}
}

// IsModify 对比配置二进制是否有变化
func (g *Gather) IsModify(src, dst []ProcCustomConf) bool {
	if len(src) != len(dst) {
		return true
	}

	if len(src) == 0 {
		return false
	}

	for i := 0; i < len(src); i++ {
		if !(src[i].Name == dst[i].Name && bytes.Compare(src[i].Content, dst[i].Content) == 0) {
			return true
		}
	}

	return false
}

// ProcCustomConf 采集配置二进制
type ProcCustomConf struct {
	Name    string
	Content []byte
}

type File struct {
	Name    string
	content []byte
	Conf    ProcRawConfig
}

// Hash 生成配置内容哈希
func (f *File) Hash() int32 {
	m := xxhash.New()
	m.Write(f.content)

	result := int32(m.Sum64() % uint64(math.MaxInt32))
	result = configs.EnsureProcsyncHash(result)
	return result
}

func (g *Gather) pathExists(filename string) bool {
	if _, err := os.Stat(filename); err == nil {
		return true
	}
	return false
}

// readSrcFiles 旧版配置文件列表
func (g *Gather) readSrcFiles() ([]File, error) {
	var ret []File
	if !g.pathExists(g.srcDir) {
		return ret, nil
	}

	files, err := os.ReadDir(g.srcDir)
	if err != nil {
		return ret, err
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}

		bs, err := os.ReadFile(filepath.Join(g.srcDir, f.Name()))
		if err != nil {
			logger.Errorf("failed to read %s: %v", f.Name(), err)
			continue
		}

		var conf ProcRawConfig
		if err := yaml.Unmarshal(bs, &conf); err != nil {
			logger.Errorf("failed to unmarshal conf: %v", err)
			continue
		}

		ret = append(ret, File{Name: f.Name(), content: bs, Conf: conf})
	}

	return ret, nil
}

// DstConf 新版采集配置
type DstConf struct {
	Name                 string              `yaml:"name"`
	Version              string              `yaml:"version"`
	Type                 string              `yaml:"type"`
	Period               time.Duration       `yaml:"period"`
	DataID               int32               `yaml:"dataid"`
	TaskID               int32               `yaml:"task_id"`
	PortDataID           int32               `yaml:"port_dataid"`
	MatchPattern         string              `yaml:"match_pattern"`
	ProcessName          string              `yaml:"process_name"`
	DimPattern           string              `yaml:"extract_pattern"`
	ExcludePattern       string              `yaml:"exclude_pattern"`
	PIDPath              string              `yaml:"pid_path"`
	ProcMetric           []string            `yaml:"proc_metric"`
	PortDetect           bool                `yaml:"port_detect"`
	Ports                []string            `yaml:"ports"`
	ListenPortOnly       bool                `yaml:"listen_port_only"`
	ReportUnexpectedPort bool                `yaml:"report_unexpected_port"`
	DisableMapping       bool                `yaml:"disable_mapping"`
	Labels               []map[string]string `yaml:"labels"`
	Tags                 map[string]string   `yaml:"tags,omitempty"`
}

// readSrcCfgs 读取旧版采集配置生成新版二进制内容列表
func (g *Gather) readSrcCfgs() ([]ProcCustomConf, error) {
	var ret []ProcCustomConf
	files, err := g.readSrcFiles()
	if err != nil {
		return ret, err
	}

	for _, f := range files {
		dur, err := time.ParseDuration(f.Conf.Period)
		if err != nil {
			logger.Errorf("failed to parse time duration: %v", err)
			continue
		}

		tags := f.Conf.Tags
		if len(tags) == 0 {
			tags = nil
		}

		conf := DstConf{
			Type:                 define.ModuleProcCustom,
			Name:                 "proccustom_task",
			Version:              "1.0.0",
			DataID:               f.Conf.DataID,
			TaskID:               f.Hash(),
			Period:               dur,
			PortDataID:           f.Conf.PortDataID,
			MatchPattern:         f.Conf.Config.MatchPattern,
			ProcessName:          f.Conf.Config.ProcessName,
			DimPattern:           f.Conf.Config.ExtractPattern,
			ExcludePattern:       f.Conf.Config.ExcludePattern,
			PIDPath:              f.Conf.Config.PIDPath,
			ProcMetric:           f.Conf.Config.ProcMetric,
			PortDetect:           f.Conf.Config.PortDetect,
			Ports:                f.Conf.Config.Ports,
			ListenPortOnly:       f.Conf.Config.ListenPortOnly,
			ReportUnexpectedPort: f.Conf.Config.ReportUnexpectedPort,
			DisableMapping:       f.Conf.Config.DisableMapping,
			Labels:               f.Conf.Labels,
			Tags:                 tags,
		}
		bs, err := yaml.Marshal(conf)
		if err != nil {
			logger.Errorf("failed to unmarshal conf: %v", err)
			continue
		}

		ret = append(ret, ProcCustomConf{Name: f.Name, Content: bs})
	}

	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Name < ret[j].Name
	})

	return ret, nil
}

// readDstCfgs 读取新版采集配置内容二进制列表
func (g *Gather) readDstCfgs() ([]ProcCustomConf, error) {
	var ret []ProcCustomConf
	files, err := os.ReadDir(g.config.DstDir)
	if err != nil {
		return ret, err
	}

	for _, f := range files {
		if !strings.HasPrefix(f.Name(), "monitor_process") {
			continue
		}

		bs, err := os.ReadFile(filepath.Join(g.config.DstDir, f.Name()))
		if err != nil {
			logger.Errorf("failed to read file %s: %v", f.Name(), err)
			continue
		}

		ret = append(ret, ProcCustomConf{Name: f.Name(), Content: bs})
	}

	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Name < ret[j].Name
	})

	return ret, nil
}

// deleteDstCfgs 删除多余配置
func (g *Gather) deleteDstCfgs(cfgs []ProcCustomConf) {
	for _, f := range cfgs {
		if err := os.Remove(filepath.Join(g.config.DstDir, f.Name)); err != nil {
			logger.Errorf("failed to remove %s: %v", f.Name, err)
		}
	}
}

// writeDstCfgs 写入新版配置
func (g *Gather) writeDstCfgs(cfgs []ProcCustomConf) {
	for _, f := range cfgs {
		p := filepath.Join(g.config.DstDir, f.Name)
		if err := os.WriteFile(p, f.Content, 0o666); err != nil {
			logger.Errorf("failed to write file: %s, err: %v", f.Name, err)
			return
		}
	}
	beat.ReloadChan <- true // 通知调度器 reload
}
