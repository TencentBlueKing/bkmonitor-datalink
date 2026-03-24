// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pproftranslator

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/sampler/evaluator"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "pprof_translator/common"
    config:
      type: "spy"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "pprof_translator/common"
    config:
      type: "spy"
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
	factory := obj.(*pprofTranslator)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	var c evaluator.Config
	assert.NoError(t, mapstructure.Decode(mainConf, &c))

	assert.Equal(t, define.ProcessorPprofTranslator, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
	factory.Clean()
}

func TestFactoryProcess(t *testing.T) {
	content := `
processor:
  - name: "pprof_translator/common"
    config:
      type: "spy"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config
	obj, err := NewFactory(mainConf, []processor.SubConfigProcessor{
		{
			Token: "token1",
			Type:  define.SubConfigFieldDefault,
		},
	})
	factory := obj.(*pprofTranslator)
	assert.NoError(t, err)

	t.Run("invalid data", func(t *testing.T) {
		record := &define.Record{
			RequestType:   define.RequestHttp,
			RequestClient: define.RequestClient{IP: "localhost"},
			RecordType:    define.RecordProfiles,
			Data:          "invalid data",
			Token:         define.Token{Original: "token"},
		}

		r, err := factory.Process(record)
		assert.Nil(t, r)
		assert.Error(t, err)
	})

	t.Run("invalid profile data", func(t *testing.T) {
		record := &define.Record{
			RequestType:   define.RequestHttp,
			RequestClient: define.RequestClient{IP: "localhost"},
			RecordType:    define.RecordProfiles,
			Data:          define.ProfilesRawData{Data: "any"},
			Token:         define.Token{Original: "token"},
		}

		r, err := factory.Process(record)
		assert.Nil(t, r)
		assert.Error(t, err)
	})

	t.Run("valid data", func(t *testing.T) {
		profileData := &profile.Profile{
			TimeNanos:     time.Now().UnixNano(),
			DurationNanos: int64(time.Second),
			SampleType:    []*profile.ValueType{{Type: "samples", Unit: "count"}},
			Sample:        []*profile.Sample{{Value: []int64{1000}, Location: make([]*profile.Location, 0)}},
		}
		var buf bytes.Buffer
		err := profileData.Write(&buf)
		assert.NoError(t, err)

		record := &define.Record{
			RequestType:   define.RequestHttp,
			RequestClient: define.RequestClient{IP: "localhost"},
			RecordType:    define.RecordProfiles,
			Data: define.ProfilesRawData{
				Data: define.ProfilePprofFormatOrigin(buf.Bytes()),
				Metadata: define.ProfileMetadata{
					StartTime:       time.Now(),
					EndTime:         time.Now(),
					SpyName:         "testSpy",
					Format:          "testFormat",
					AggregationType: "cpu",
					Units:           "seconds",
					AppName:         "testAppName",
				},
			},
			Token: define.Token{Original: "token"},
		}

		r, err := factory.Process(record)
		assert.Nil(t, r)
		assert.NoError(t, err)
	})
}
