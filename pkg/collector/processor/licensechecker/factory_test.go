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

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/licensechecker/licensecache"
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
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
    - name: "license_checker/common"
      config:
        enabled: true
        expire_time: 110001110
        tolerable_expire: 24h
        number_nodes: 200
        tolerable_num_ratio: 1.5
`
	customConf := processor.MustLoadConfigs(customContent)[0].Config

	obj, err := NewFactory(mainConf, []processor.SubConfigProcessor{
		{
			Token: "token1",
			Type:  define.SubConfigFieldDefault,
			Config: processor.Config{
				Config: customConf,
			},
		},
	})
	factory := obj.(*licenseChecker)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	var c1 Config
	assert.NoError(t, mapstructure.Decode(mainConf, &c1))
	assert.Equal(t, c1, factory.configs.GetGlobal().(Config))

	var c2 Config
	assert.NoError(t, mapstructure.Decode(customConf, &c2))
	assert.Equal(t, c2, factory.configs.GetByToken("token1").(Config))

	assert.Equal(t, define.ProcessorLicenseChecker, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.True(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
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

	factory := processor.MustCreateFactory(content, NewFactory)

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
			record := define.Record{
				Token:       define.Token{Original: "token1"},
				RequestType: define.RequestGrpc,
				RecordType:  define.RecordTraces,
				Data:        g.Generate(),
			}
			testkits.MustProcess(t, factory, record)
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
		_, err := factory.Process(r)
		assert.Equal(t, "service.instance.id attribute not found", err.Error())
	})

	t.Run("NoSpans", func(t *testing.T) {
		r := &define.Record{
			RequestType: define.RequestGrpc,
			RecordType:  define.RecordTraces,
			Data:        ptrace.NewTraces(),
		}
		_, err := factory.Process(r)
		assert.Equal(t, define.ErrSkipEmptyRecord, err)
	})
}

func TestAgentNodeStatusProcess(t *testing.T) {
	config := Config{
		NumNodes:          1,
		TolerableNumRatio: 1.0,
	}

	cacheMgr := licensecache.NewManager()

	agentStatus, nodeStatus := checkAgentNodeStatus(config, "token_x1", "instance1", cacheMgr)
	assert.Equal(t, statusAgentNew, agentStatus)
	assert.Equal(t, statusNodeAccess, nodeStatus)

	cache := cacheMgr.GetOrCreate("token_x1")
	cache.Set("instance1")

	agentStatus, nodeStatus = checkAgentNodeStatus(config, "token_x1", "instance2", cacheMgr)
	assert.Equal(t, statusAgentNew, agentStatus)
	assert.Equal(t, statusNodeExcess, nodeStatus)
}

func TestCheckLicenseStatus(t *testing.T) {
	t.Run("Access", func(t *testing.T) {
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
		factory := processor.MustCreateFactory(content, NewFactory)
		config := factory.(*licenseChecker).configs.Get("", "", "").(Config)
		assert.Equal(t, statusLicenseAccess, checkLicenseStatus(config))
	})

	t.Run("Tolerable", func(t *testing.T) {
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
		factory := processor.MustCreateFactory(content, NewFactory)
		config := factory.(*licenseChecker).configs.Get("", "", "").(Config)
		assert.Equal(t, statusLicenseTolerable, checkLicenseStatus(config))
	})

	t.Run("Expire", func(t *testing.T) {
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
		factory := processor.MustCreateFactory(content, NewFactory)
		config := factory.(*licenseChecker).configs.Get("", "", "").(Config)
		assert.Equal(t, statusLicenseExpire, checkLicenseStatus(config))
	})
}

func TestProcessLicenseStatus(t *testing.T) {
	tests := []struct {
		agentStatus   Status
		licenseStatus Status
		nodeStatus    Status
		pass          bool
		err           error
	}{
		{
			agentStatus:   statusAgentOld,
			licenseStatus: statusLicenseAccess,
			nodeStatus:    statusNodeAccess,
			pass:          true,
			err:           nil,
		},
		{
			agentStatus:   statusAgentOld,
			licenseStatus: statusLicenseTolerable,
			nodeStatus:    statusNodeAccess,
			pass:          true,
			err:           errLicenseTolerable,
		},
		{
			agentStatus:   statusAgentOld,
			licenseStatus: statusLicenseTolerable,
			nodeStatus:    statusNodeExcess,
			pass:          true,
			err:           errLicenseTolerableNodeExcess,
		},
		{
			agentStatus:   statusAgentOld,
			licenseStatus: statusLicenseExpire,
			nodeStatus:    statusNodeAccess,
			pass:          false,
			err:           errLicenseExpired,
		},
		{
			agentStatus:   statusAgentOld,
			licenseStatus: statusLicenseExpire,
			nodeStatus:    statusNodeExcess,
			pass:          false,
			err:           errLicenseExpired,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseAccess,
			nodeStatus:    statusNodeAccess,
			pass:          true,
			err:           nil,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseAccess,
			nodeStatus:    statusNodeExcess,
			pass:          false,
			err:           errNodeExcess,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseTolerable,
			nodeStatus:    statusNodeAccess,
			pass:          false,
			err:           errLicenseTolerable,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseTolerable,
			nodeStatus:    statusNodeExcess,
			pass:          false,
			err:           errLicenseTolerableNodeExcess,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseExpire,
			nodeStatus:    statusNodeAccess,
			pass:          false,
			err:           errLicenseExpired,
		},
		{
			agentStatus:   statusAgentNew,
			licenseStatus: statusLicenseExpire,
			nodeStatus:    statusNodeExcess,
			pass:          false,
			err:           errLicenseExpired,
		},
	}

	for _, tt := range tests {
		pass, err := processLicenseStatus(statusInfo{
			agent:   tt.agentStatus,
			node:    tt.nodeStatus,
			license: tt.licenseStatus,
		})
		assert.Equal(t, tt.err, err)
		assert.Equal(t, tt.pass, pass)
	}

	pass, err := processLicenseStatus(statusInfo{
		agent:   statusUnspecified,
		node:    statusUnspecified,
		license: statusUnspecified,
	})
	assert.False(t, pass)
	assert.Equal(t, define.ErrUnknownRecordType, err)
}
