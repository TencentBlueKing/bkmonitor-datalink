// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// parseProcessors 解析 Processors 配置
func parseProcessors(typ string, conf *confengine.Config, subConfigs map[string][]processor.SubConfigProcessor) (map[string]processor.Instance, error) {
	var processorConfigs processor.Configs
	if err := conf.UnpackChild(define.ConfigFieldProcessor, &processorConfigs); err != nil {
		return nil, err
	}

	for i := 0; i < len(processorConfigs); i++ {
		pcf := processorConfigs[i]
		logger.Debugf("%s processor config: %+v", typ, pcf)
	}

	processors := map[string]processor.Instance{}
	for i := 0; i < len(processorConfigs); i++ {
		cfg := processorConfigs[i]
		if cfg.Name == "" {
			logger.Errorf("empty processor name is illegal: %+v", cfg)
			continue
		}
		if _, ok := processors[cfg.Name]; ok {
			logger.Errorf("duplicated processor name: %v", cfg.Name)
			continue
		}

		createFunc := processor.GetProcessorCreator(cfg.Name)
		if createFunc == nil {
			logger.Errorf("unknown processor type: %v", cfg.Name)
			continue
		}

		p, err := createFunc(cfg.Config, subConfigs[cfg.Name])
		if err != nil {
			logger.Errorf("failed to create processor instance %+v: %v", cfg, err)
			continue
		}
		processors[cfg.Name] = processor.NewInstance(cfg.Name, p)
	}
	return processors, nil
}

// parsePipelines 解析 pipelines 配置
func parsePipelines(typ string, conf *confengine.Config, processors map[string]processor.Instance) (map[define.RecordType]Pipeline, error) {
	var pipelineConf PipelineConfigs
	if err := conf.UnpackChild(define.ConfigFieldPipeline, &pipelineConf); err != nil {
		return nil, err
	}
	for _, pcf := range pipelineConf {
		logger.Infof("%s pipeline config: %+v", typ, pcf)
	}

	pipelines := map[define.RecordType]Pipeline{}
	for i := 0; i < len(pipelineConf); i++ {
		plc := pipelineConf[i]
		if plc.Name == "" {
			logger.Errorf("empty pipeline name is illegal: %+v", plc)
			continue
		}

		// 每个 pipelines 类型只能有唯一 pipeline
		rtype, derived := define.IntoRecordType(plc.Type)
		if _, ok := pipelines[rtype]; ok {
			logger.Errorf("duplicated pipeline type: %+v", rtype)
			continue
		}

		var instances []processor.Instance
		for _, name := range plc.Processors {
			if rtype == define.RecordUndefined {
				logger.Errorf("unknown record type: %v", plc.Type)
				break
			}
			p, ok := processors[name]
			if !ok {
				logger.Errorf("unknown processor: %v", name)
				break
			}

			// 派生类型的 pipeline 如果允许存在 IsDerived 为 true 的 processor【可能】会有问题
			// 仅做 warning 提示
			if derived && p.IsDerived() {
				logger.Warnf("derived record type do not allow derived processor: %v", p.Name())
			}
			instances = append(instances, processor.NewInstance(name, p))
		}

		// 在一条 pipeline 中如果有某个节点处理出现问题 则整条流水线构建失败
		if len(instances) != len(plc.Processors) {
			DefaultMetricMonitor.IncBuiltFailedCounter(plc.Name, plc.Type)
			logger.Errorf("build pipeline %s failed", plc.Name)
			continue
		}

		pl := NewPipeline(plc.Name, rtype, instances...)
		// 校验 pipeline 配置，precheck processor 要位于 sched processor 之前
		if !pl.Validate() {
			DefaultMetricMonitor.IncBuiltFailedCounter(plc.Name, plc.Type)
			logger.Errorf("validate pipeline %s failed", plc.Name)
			continue
		}
		DefaultMetricMonitor.IncBuiltSuccessCounter(plc.Name, plc.Type)
		logger.Infof("build pipeline %v", pl)
		pipelines[rtype] = pl
	}
	return pipelines, nil
}

// parseReportV2Configs 解析 report_v2 子配置
// report_v2 子配置结构同 subconfig 一致 但是无 service/instance 级别配置
// 使用 define.ConfigTypeReportV2 类型做区分
func parseReportV2Configs(configs []*confengine.Config) map[string][]processor.SubConfigProcessor {
	ps := make(map[string][]processor.SubConfigProcessor)
	for _, c := range configs {
		var subConf processor.SubConfig
		if err := c.Unpack(&subConf); err != nil {
			logger.Errorf("failed to unpack report_v2 config, err: %v", err)
			continue
		}
		if subConf.Type != define.ConfigTypeReportV2 {
			continue
		}
		if subConf.Token == "" {
			logger.Warnf("ignore empty token in report_v2 config: %+v", subConf)
			continue
		}

		for _, p := range subConf.Default.Processor {
			ps[p.Name] = append(ps[p.Name], processor.SubConfigProcessor{
				Token:  subConf.Token,
				Type:   define.SubConfigFieldDefault,
				Config: p,
			})
		}
	}

	return ps
}

func parseReportV1Configs(configs []*confengine.Config) map[string][]processor.SubConfigProcessor {
	ps := make(map[string][]processor.SubConfigProcessor)
	for _, c := range configs {
		var subConf reportV1Config
		if err := c.Unpack(&subConf); err != nil {
			logger.Errorf("failed to unpack report_v1 config, err: %v", err)
			continue
		}
		if subConf.Type != define.ConfigTypeReportV1 {
			continue
		}

		v2, err := convertReportV1ToV2(subConf)
		if err != nil {
			logger.Errorf("failed to convert v1/config to v2/config, err: %v", err)
			continue
		}
		for k, items := range parseReportV2Configs(v2) {
			ps[k] = append(ps[k], items...)
		}
	}

	return ps
}

// parseProcessorSubConfigs 解析 processor 子配置
func parseProcessorSubConfigs(configs []*confengine.Config) map[string][]processor.SubConfigProcessor {
	ps := make(map[string][]processor.SubConfigProcessor)
	for _, c := range configs {
		var subConf processor.SubConfig
		if err := c.Unpack(&subConf); err != nil {
			logger.Errorf("failed to unpack subconfig, err: %v", err)
			continue
		}
		if subConf.Type != define.ConfigTypeSubConfig {
			continue
		}
		if subConf.Token == "" {
			logger.Warnf("ignore empty token in subconfig: %+v", subConf)
			continue
		}

		for _, p := range subConf.Default.Processor {
			ps[p.Name] = append(ps[p.Name], processor.SubConfigProcessor{
				Token:  subConf.Token,
				Type:   define.SubConfigFieldDefault,
				Config: p,
			})
		}
		for _, srv := range subConf.Service {
			for _, s := range srv.Processor {
				ps[s.Name] = append(ps[s.Name], processor.SubConfigProcessor{
					Token:  subConf.Token,
					ID:     srv.ID,
					Type:   define.SubConfigFieldService,
					Config: s,
				})
			}
		}
		for _, inst := range subConf.Instance {
			for _, i := range inst.Processor {
				ps[i.Name] = append(ps[i.Name], processor.SubConfigProcessor{
					Token:  subConf.Token,
					ID:     inst.ID,
					Type:   define.SubConfigFieldInstance,
					Config: i,
				})
			}
		}
	}

	return ps
}

// mergeSubConfigs 合并 subconfigs 配置
func mergeSubConfigs(items ...map[string][]processor.SubConfigProcessor) map[string][]processor.SubConfigProcessor {
	dst := make(map[string][]processor.SubConfigProcessor)
	for _, item := range items {
		for k, ps := range item {
			dst[k] = append(dst[k], ps...)
		}
	}

	return dst
}

// mergeProcessors 合并处理器配置
func mergeProcessors(main, sub map[string]processor.Instance) map[string]processor.Instance {
	for k, v := range sub {
		if inst, ok := main[k]; ok {
			logger.Infof("merge platform processor: %s", k)
			inst.Clean() // 清理有状态 processors
		}
		main[k] = v
	}
	return main
}

// mergePipelines 合并流水线配置
func mergePipelines(main, sub map[define.RecordType]Pipeline) map[define.RecordType]Pipeline {
	merged := make(map[define.RecordType]Pipeline)
	for rtype, pl := range main {
		merged[rtype] = pl
	}

	for rtype, pl := range sub {
		merged[rtype] = pl
	}

	return merged
}

// Manager 负责管理 Pipelines 的解析和存储
// 无并发读写情况 不必加锁
type Manager struct {
	processors map[string]processor.Instance  // key: 处理器实例名称; value: 处理器实例（函数指针）
	pipelines  map[define.RecordType]Pipeline // key: 记录类型; value: 流水线
}

// Getter processor/pipeline 获取接口
type Getter interface {
	// GetProcessor 根据 name 获取 processor 实例
	GetProcessor(name string) processor.Instance

	// GetPipeline 根据 rtype 获取 pipeline 实例
	GetPipeline(rtype define.RecordType) Pipeline
}

var defaultGetter Getter

func GetDefaultGetter() Getter { return defaultGetter }

const (
	mainType       = "main"
	PlatformType   = "platform"
	PrivilegedType = "privileged"
)

func parseManagerConfig(conf *confengine.Config) (*Manager, error) {
	var apmConf define.ApmConfig
	var err error

	if err = conf.UnpackChild(define.ConfigFieldApmConfig, &apmConf); err != nil {
		return nil, err
	}
	logger.Infof("apmconf: %+v", apmConf)

	// 加载所有子配置
	patterns := stealConfigs(apmConf.Patterns)
	subConfigs := confengine.LoadConfigPatterns(patterns)

	// 加载字段映射
	if conf.Has(define.ConfigFieldAlias) {
		err := processor.LoadAlias(conf)
		if err != nil {
			return nil, err
		}
	}

	// 解析合并：配置 = 主配置+子配置
	processorSubConfigs := mergeSubConfigs(
		parseProcessorSubConfigs(subConfigs),
		parseReportV1Configs(subConfigs),
		parseReportV2Configs(subConfigs),
	)
	finalProcessors, err := parseProcessors(mainType, conf, processorSubConfigs)
	if err != nil {
		return nil, err
	}
	finalPipelines, err := parsePipelines(mainType, conf, finalProcessors)
	if err != nil {
		return nil, err
	}

	// 解析合并配置 = 主配置+子配置+平台配置（如果有的话）
	platformConfig := confengine.SelectConfigFromType(subConfigs, define.ConfigTypePlatform)
	if platformConfig != nil {

		// 使用平台配置覆盖字段映射
		if platformConfig.Has(define.ConfigFieldAlias) {
			err := processor.LoadAlias(platformConfig)
			if err != nil {
				return nil, err
			}
		}

		if platformConfig.Has(define.ConfigFieldProcessor) {
			platformProcessors, err := parseProcessors(PlatformType, platformConfig, processorSubConfigs)
			if err != nil {
				return nil, err
			}
			finalProcessors = mergeProcessors(finalProcessors, platformProcessors)
		}

		if platformConfig.Has(define.ConfigFieldPipeline) {
			platformPipelines, err := parsePipelines(PlatformType, platformConfig, finalProcessors)
			if err != nil {
				return nil, err
			}
			finalPipelines = mergePipelines(finalPipelines, platformPipelines)
		}
	}

	// 解析合并配置 = 主配置+子配置+平台配置+高优配置（如果有的话）
	privilegedConfig := confengine.SelectConfigFromType(subConfigs, define.ConfigTypePrivileged)
	if privilegedConfig != nil {
		if privilegedConfig.Has(define.ConfigFieldProcessor) {
			privilegedProcessors, err := parseProcessors(PrivilegedType, privilegedConfig, processorSubConfigs)
			if err != nil {
				return nil, err
			}
			finalProcessors = mergeProcessors(finalProcessors, privilegedProcessors)
		}
	}

	mgr := &Manager{
		processors: finalProcessors,
		pipelines:  finalPipelines,
	}
	return mgr, nil
}

func New(conf *confengine.Config) (*Manager, error) {
	mgr, err := parseManagerConfig(conf)
	if err != nil {
		return nil, err
	}
	defaultGetter = mgr
	return mgr, nil
}

func (mgr *Manager) Reload(conf *confengine.Config) error {
	newManager, err := parseManagerConfig(conf)
	if err != nil {
		return errors.Wrap(err, "pipeline Manager reload error")
	}

	// 清理 Processor
	for _, p := range newManager.processors {
		p.Clean()
	}

	// TODO(mando): 这里仅使用 newManager 的配置
	// 实际上应该从更上层仅返回配置 这样可以进一步节省初始化开销（待优化）
	for k, p := range newManager.processors {
		inst, ok := mgr.processors[k]
		if !ok {
			mgr.processors[k] = p
			continue
		}
		inst.Reload(p.MainConfig(), p.SubConfigs())
	}

	mgr.pipelines = newManager.pipelines
	return nil
}

func (mgr *Manager) GetProcessor(name string) processor.Instance {
	return mgr.processors[name]
}

func (mgr *Manager) GetPipeline(rtype define.RecordType) Pipeline {
	return mgr.pipelines[rtype]
}
