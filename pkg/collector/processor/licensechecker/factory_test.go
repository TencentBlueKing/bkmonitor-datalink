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
	"testing"

	"github.com/mitchellh/mapstructure"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/licensecache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
)

func TestFactory(t *testing.T) {
	content := `
processor:
    - name: "license_checker/common"
      config:
        enabled: true
        expire_time: 4084531651
        tolerable_expire: 24h
        number_nodes: 200
        tolerable_num_ratio: 1.5
`

	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*licenseChecker)
	assert.NoError(t, err)
	assert.Equal(t, psc[0].Config, factory.MainConfig())

	var c Config
	decoder, _ := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:     &c,
		DecodeHook: mapstructure.StringToTimeDurationHookFunc(),
	})
	err = decoder.Decode(psc[0].Config)
	assert.NoError(t, err)
	assert.Equal(t, c, factory.config.Get("", "", "").(Config))

	assert.Equal(t, define.ProcessorLicenseChecker, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.True(t, factory.IsPreCheck())
}

func makeLicenseChecker(content string) *licenseChecker {
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	if err != nil {
		panic(err)
	}
	return obj.(*licenseChecker)
}

func TestLicenseCheckerProcess(t *testing.T) {
	content := `
processor:
    - name: "license_checker/common"
      config:
        enabled: true
        expire_time: 4084531651
        tolerable_expire: 24h
        number_nodes: 200
        tolerable_num_ratio: 1.5
`
	checker := makeLicenseChecker(content)
	t.Run("Success", func(t *testing.T) {
		g := generator.NewTracesGenerator(define.TracesOptions{
			GeneratorOptions: define.GeneratorOptions{
				Resources: map[string]string{
					"service.instance.id": "my-instance-1",
				},
			},
			SpanCount: 1,
		})
		for i := 0; i < 10; i++ {
			r := &define.Record{
				Token:       define.Token{Original: "token1"},
				RequestType: define.RequestGrpc,
				RecordType:  define.RecordTraces,
				Data:        g.Generate(),
			}
			_, err := checker.Process(r)
			assert.NoError(t, err)
		}
	})

	t.Run("NoInstance", func(t *testing.T) {
		g := generator.NewTracesGenerator(define.TracesOptions{
			SpanCount: 1,
		})
		r := &define.Record{
			RequestType: define.RequestGrpc,
			RecordType:  define.RecordTraces,
			Data:        g.Generate(),
		}
		_, err := checker.Process(r)
		assert.Equal(t, "service.instance.id attribute not found", err.Error())
	})

	t.Run("NoSpans", func(t *testing.T) {
		r := &define.Record{
			RequestType: define.RequestGrpc,
			RecordType:  define.RecordTraces,
			Data:        ptrace.NewTraces(),
		}
		_, err := checker.Process(r)
		assert.Equal(t, define.ErrSkipEmptyRecord, err)
	})
}

func TestAgentNodeStatusProcess(t *testing.T) {
	checker := &licenseChecker{}
	conf := Config{NumNodes: 1, TolerableNumRatio: 1.0}
	agentStatus, nodeStatus := checker.checkAgentNodeStatus(conf, "token_x1", "instance1")
	assert.Equal(t, statusAgentNew, agentStatus)
	assert.Equal(t, statusNodeAccess, nodeStatus)

	cacher := licensecache.GetOrCreateCacher("token_x1")
	cacher.Set("instance1")

	agentStatus, nodeStatus = checker.checkAgentNodeStatus(conf, "token_x1", "instance2")
	assert.Equal(t, statusAgentNew, agentStatus)
	assert.Equal(t, statusNodeExcess, nodeStatus)
}

func TestCheckLicenseStatus(t *testing.T) {
	t.Run("statusLicenseAccess", func(t *testing.T) {
		content := `
processor:
    - name: "license_checker/common"
      config:
        enabled: true
        expire_time: 4084531651
        tolerable_expire: 24h
        number_nodes: 200
        tolerable_num_ratio: 1.5
`
		checker := makeLicenseChecker(content)
		conf := checker.config.GetByToken("").(Config)
		status := checker.checkLicenseStatus(conf)
		assert.Equal(t, statusLicenseAccess, status)
	})

	t.Run("statusLicenseTolerable", func(t *testing.T) {
		content := `
processor:
   - name: "license_checker/common"
     config:
       enabled: true
       expire_time: 1686134440
       tolerable_expire: 2400000h
       number_nodes: 200
       tolerable_num_ratio: 1.5
`
		checker := makeLicenseChecker(content)
		conf := checker.config.GetByToken("").(Config)
		status := checker.checkLicenseStatus(conf)
		assert.Equal(t, statusLicenseTolerable, status)
	})

	t.Run("statusLicenseExpire", func(t *testing.T) {
		content := `
processor:
   - name: "license_checker/common"
     config:
       enabled: true
       expire_time: 1683542440
       tolerable_expire: 24h
       number_nodes: 200
       tolerable_num_ratio: 1.5
`
		checker := makeLicenseChecker(content)
		conf := checker.config.GetByToken("").(Config)
		status := checker.checkLicenseStatus(conf)
		assert.Equal(t, statusLicenseExpire, status)
	})
}

func TestJudgeByStatus(t *testing.T) {
	type Case struct {
		agentStatus   Status
		licenseStatus Status
		nodeStatus    Status
		exceptedRes   bool
		exceptedErr   error
	}

	cases := []Case{
		{
			agentStatus:   statusAgentOld,
			licenseStatus: statusLicenseAccess,
			nodeStatus:    statusNodeAccess,
			exceptedRes:   true,
			exceptedErr:   nil,
		},
		{
			agentStatus:   statusAgentOld,
			licenseStatus: statusLicenseTolerable,
			nodeStatus:    statusNodeAccess,
			exceptedRes:   true,
			exceptedErr:   errLicenseTolerable,
		},
		{
			agentStatus:   statusAgentOld,
			licenseStatus: statusLicenseTolerable,
			nodeStatus:    statusNodeExcess,
			exceptedRes:   true,
			exceptedErr:   errLicenseTolerableNodeExcess,
		},
		{
			agentStatus:   statusAgentOld,
			licenseStatus: statusLicenseExpire,
			nodeStatus:    statusNodeAccess,
			exceptedRes:   false,
			exceptedErr:   errLicenseExpired,
		},
		{
			agentStatus:   statusAgentOld,
			licenseStatus: statusLicenseExpire,
			nodeStatus:    statusNodeExcess,
			exceptedRes:   false,
			exceptedErr:   errLicenseExpired,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseAccess,
			nodeStatus:    statusNodeAccess,
			exceptedRes:   true,
			exceptedErr:   nil,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseAccess,
			nodeStatus:    statusNodeExcess,
			exceptedRes:   false,
			exceptedErr:   errNodeExcess,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseTolerable,
			nodeStatus:    statusNodeAccess,
			exceptedRes:   false,
			exceptedErr:   errLicenseTolerable,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseTolerable,
			nodeStatus:    statusNodeExcess,
			exceptedRes:   false,
			exceptedErr:   errLicenseTolerableNodeExcess,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseExpire,
			nodeStatus:    statusNodeAccess,
			exceptedRes:   false,
			exceptedErr:   errLicenseExpired,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseExpire,
			nodeStatus:    statusNodeExcess,
			exceptedRes:   false,
			exceptedErr:   errLicenseExpired,
		},
	}

	checker := &licenseChecker{}
	for _, v := range cases {
		res, err := checker.judgeByStatus(v.agentStatus, v.nodeStatus, v.licenseStatus)
		assert.Equal(t, v.exceptedErr, err)
		assert.Equal(t, v.exceptedRes, res)
	}

	res, err := checker.judgeByStatus(statusUnspecified, statusUnspecified, statusUnspecified)
	assert.Equal(t, false, res)
	assert.Equal(t, define.ErrUnknownRecordType, err)
}
