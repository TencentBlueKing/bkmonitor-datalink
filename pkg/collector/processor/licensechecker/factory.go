// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package licensechecker

import (
	"time"

	"github.com/pkg/errors"
	"go.opentelemetry.io/collector/pdata/ptrace"
	semconv "go.opentelemetry.io/collector/semconv/v1.8.0"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/licensecache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Status int

const (
	statusUnspecified Status = iota
	statusLicenseAccess
	statusLicenseTolerable
	statusLicenseExpire
	statusNodeAccess
	statusNodeExcess
	statusAgentNew
	statusAgentOld
)

var (
	errLicenseExpired             = errors.New("license expired, reject all agents")
	errLicenseTolerable           = errors.New("license tolerable, reject new agents")
	errLicenseTolerableNodeExcess = errors.New("license tolerable and node excess, reject new agents")
	errNodeExcess                 = errors.New("license agents excess, reject new agents")
)

func init() {
	processor.Register(define.ProcessorLicenseChecker, NewFactory)
}

func NewFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (*licenseChecker, error) {
	configs := confengine.NewTierConfig()

	var c Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	configs.SetGlobal(c)

	for _, custom := range customized {
		var cfg Config
		if err := mapstructure.Decode(custom.Config.Config, &cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		configs.Set(custom.Token, custom.Type, custom.ID, cfg)
	}

	return &licenseChecker{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		config:          configs,
	}, nil
}

type licenseChecker struct {
	processor.CommonProcessor
	config *confengine.TierConfig
}

func (p licenseChecker) Name() string {
	return define.ProcessorLicenseChecker
}

func (p licenseChecker) IsDerived() bool {
	return false
}

func (p licenseChecker) IsPreCheck() bool {
	return true
}

func (p licenseChecker) Process(record *define.Record) (*define.Record, error) {
	switch record.RecordType {
	case define.RecordTraces:
		return p.processTraces(record)
	}
	return nil, nil
}

func (p licenseChecker) processTraces(record *define.Record) (*define.Record, error) {
	pdTraces := record.Data.(ptrace.Traces)
	if pdTraces.ResourceSpans().Len() == 0 {
		return nil, define.ErrSkipEmptyRecord
	}

	token := record.Token.Original
	conf := p.config.GetByToken(token).(Config)

	// 如果是关闭 license 校验的情况下 直接放行
	if !conf.Enabled {
		return nil, nil
	}

	// 单次 traces 数据都是同一个 AttributeServiceInstanceID
	attrs := pdTraces.ResourceSpans().At(0).Resource().Attributes()
	val, ok := attrs.Get(semconv.AttributeServiceInstanceID)
	if !ok {
		return nil, errors.New("service.instance.id attribute not found")
	}

	instance := val.AsString()
	pass, err := processLicenseStatus(checkStatus(conf, token, instance))
	if !pass {
		return nil, err
	}

	logger.Debugf("get or create new cacher, token=%v", token)
	cacher := licensecache.GetOrCreateCacher(token)
	cacher.Set(instance)
	return nil, nil
}

type statusInfo struct {
	agent   Status
	node    Status
	license Status
}

func checkStatus(conf Config, token, instance string) statusInfo {
	agentStatus, nodeStatus := checkAgentNodeStatus(conf, token, instance)
	licenseStatus := checkLicenseStatus(conf)
	return statusInfo{
		agent:   agentStatus,
		node:    nodeStatus,
		license: licenseStatus,
	}
}

func checkLicenseStatus(conf Config) Status {
	expTime := time.Unix(conf.ExpireTime, 0)
	toleTime := expTime.Add(conf.TolerableExpire)

	now := time.Now()
	if now.Before(expTime) {
		return statusLicenseAccess
	} else if now.After(expTime) && now.Before(toleTime) {
		return statusLicenseTolerable
	}
	return statusLicenseExpire
}

func checkAgentNodeStatus(conf Config, token, instance string) (Status, Status) {
	agentStatus := statusAgentOld
	agentNodeNum := 0

	cacher := licensecache.GetCacher(token)
	if cacher != nil {
		agentNodeNum = cacher.Count()
		if !cacher.Exist(instance) {
			agentStatus = statusAgentNew
		}
	} else {
		agentStatus = statusAgentNew
	}

	// 所允许的最大数量
	nodeNumAllow := int(float64(conf.NumNodes) * conf.TolerableNumRatio)
	if agentStatus == statusAgentNew {
		agentNodeNum += 1
	}

	if agentNodeNum <= nodeNumAllow {
		return agentStatus, statusNodeAccess
	}
	return agentStatus, statusNodeExcess
}

// processLicenseStatus 根据已接入探针数量以及 license 状态等进行判断是否接受数据
func processLicenseStatus(status statusInfo) (bool, error) {
	// license 过期不允许探针接入
	if status.license == statusLicenseExpire {
		return false, errLicenseExpired
	}

	// 探针已经存在的场景下
	if status.agent == statusAgentOld {
		switch status.license {
		// 如果 license 未过期 直接放行
		case statusLicenseAccess:
			return true, nil

			// license 在容忍度范围内的时候
		case statusLicenseTolerable:
			// 已接入探针数量未超限的情况下 接收数据 并且给出提示信息
			if status.node == statusNodeAccess {
				return true, errLicenseTolerable
			}
			// 超限探针（因为是原先已经接入的探针）依旧接受数据
			return true, errLicenseTolerableNodeExcess
		}
	}

	// 接入新探针场景下
	if status.agent == statusAgentNew {
		switch status.license {
		// license 未过期情况下
		case statusLicenseAccess:
			// 新探针未超限，放行数据
			if status.node == statusNodeAccess {
				return true, nil
			}
			return false, errNodeExcess

			// license 处于容忍期限内
		case statusLicenseTolerable:
			// 新探针未超限也不接收（因证书已经过期）
			if status.node == statusNodeAccess {
				return false, errLicenseTolerable
			}
			return false, errLicenseTolerableNodeExcess
		}
	}
	return false, define.ErrUnknownRecordType
}
