// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"sort"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestCommonProcessor(t *testing.T) {
	p := NewCommonProcessor(nil, nil)
	assert.Nil(t, p.MainConfig())
	assert.Nil(t, p.SubConfigs())
	p.Clean()
}

func TestRegisterCreateFunc(t *testing.T) {
	Register("NoopFuncForTest", func(config map[string]any, customized []SubConfigProcessor) (Processor, error) {
		return nil, nil
	})

	fn := GetProcessorCreator("NoopFuncForTest/id")
	p, err := fn(nil, nil)
	assert.Nil(t, p)
	assert.Nil(t, err)

	inst := NewInstance("id1", p)
	assert.Equal(t, "id1", inst.ID())
}

func TestNonSchedRecords(t *testing.T) {
	PublishNonSchedRecords(&define.Record{})
	select {
	case <-NonSchedRecords():
	default:
	}
}

func TestMustLoadConfig(t *testing.T) {
	t.Run("Normal", func(t *testing.T) {
		content := `
processor:
   - name: "attribute_filter/common"
     config:
       as_string:
         keys:
           - "attributes.http.host"
`
		assert.NotPanics(t, func() {
			MustLoadConfigs(content)
		})
	})

	t.Run("Panic", func(t *testing.T) {
		assert.Panics(t, func() {
			MustLoadConfigs("{-]")
		})
	})
}

func TestMustCreateFactory(t *testing.T) {
	assert.Panics(t, func() {
		content := `
processor:
   - name: "attribute_filter/common"
     config:
       as_string:
         keys:
           - "attributes.http.host"
`
		MustCreateFactory(content, func(config map[string]any, customized []SubConfigProcessor) (Processor, error) {
			return nil, errors.New("MUST ERROR")
		})
	})
}

func TestDiffMainConfig(t *testing.T) {
	t.Run("Equal", func(t *testing.T) {
		content1 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_string:
         keys:
           - "attributes.http.host"
       as_int:
         keys:
           - "attributes.http.status_code"
`
		content2 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_int:
         keys:
           - "attributes.http.status_code"
       as_string:
         keys:
           - "attributes.http.host"
`
		psc1 := MustLoadConfigs(content1)
		psc2 := MustLoadConfigs(content2)
		assert.True(t, DiffMainConfig(psc1[0].Config, psc2[0].Config))
	})

	t.Run("NotEqual1", func(t *testing.T) {
		content1 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_string:
         keys:
           - "attributes.http.host"
       as_int:
         keys:
           - "attributes.http.status_code"
`
		content2 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_int:
         keys:
           - "attributes.http.status_code"
       as_string:
         keys:
           - "attributes.http.hostx"
`
		psc1 := MustLoadConfigs(content1)
		psc2 := MustLoadConfigs(content2)
		assert.False(t, DiffMainConfig(psc1[0].Config, psc2[0].Config))
	})

	t.Run("NotEqual2", func(t *testing.T) {
		content1 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_string:
         keys:
           - "attributes.http.host"
           - "attributes.http.port"
`
		content2 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_string:
         keys:
           - "attributes.http.port"
           - "attributes.http.host"
`
		psc1 := MustLoadConfigs(content1)
		psc2 := MustLoadConfigs(content2)
		assert.False(t, DiffMainConfig(psc1[0].Config, psc2[0].Config))
	})
}

func TestCustomizedMainConfig(t *testing.T) {
	t.Run("Equal", func(t *testing.T) {
		content1 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_string:
         keys:
           - "attributes.http.host"
       as_int:
         keys:
           - "attributes.http.status_code"
`
		content2 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_int:
         keys:
           - "attributes.http.status_code"
       as_string:
         keys:
           - "attributes.http.port"
`
		content3 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_int:
         keys:
           - "attributes.http.scheme"
       as_string:
         keys:
           - "attributes.http.port"
`
		psc1 := MustLoadConfigs(content1)
		psc2 := MustLoadConfigs(content2)
		psc3 := MustLoadConfigs(content3)

		c1 := []SubConfigProcessor{
			{
				Type: "service",
				ID:   "foo",
				Config: Config{
					Name:   "attribute_filter/common",
					Config: psc1[0].Config,
				},
			},
			{
				Type: "service",
				ID:   "bar",
				Config: Config{
					Name:   "attribute_filter/common",
					Config: psc2[0].Config,
				},
			},
			{
				Type: "service",
				ID:   "baz",
				Config: Config{
					Name:   "attribute_filter/common",
					Config: psc3[0].Config,
				},
			},
		}

		content4 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_string:
         keys:
           - "attributes.http.hostip"
       as_int:
         keys:
           - "attributes.http.status_code"
`
		content5 := `
processor:
   - name: "attribute_filter/common"
     config:
       as_string:
         keys:
           - "attributes.http.tls"
       as_int:
         keys:
           - "attributes.http.status_code"
`
		psc4 := MustLoadConfigs(content4)
		psc5 := MustLoadConfigs(content5)
		c2 := []SubConfigProcessor{
			{
				Type: "service",
				ID:   "foo",
				Config: Config{
					Name:   "attribute_filter/common",
					Config: psc4[0].Config,
				},
			},
			{
				Type: "service",
				ID:   "baz",
				Config: Config{
					Name:   "attribute_filter/common",
					Config: psc3[0].Config,
				},
			},
			{
				Type: "service",
				ID:   "orz",
				Config: Config{
					Name:   "attribute_filter/common",
					Config: psc5[0].Config,
				},
			},
		}

		ret := DiffCustomizedConfig(c1, c2)
		assert.Len(t, ret.Updated, 2)
		sort.Slice(ret.Updated, func(i, j int) bool {
			return ret.Updated[i].ID < ret.Updated[j].ID
		})

		assert.Equal(t, "foo", ret.Updated[0].ID)
		assert.Equal(t, map[string]any{"keys": []any{"attributes.http.hostip"}}, ret.Updated[0].Config.Config["as_string"])
		assert.Equal(t, "orz", ret.Updated[1].ID)
		assert.Equal(t, map[string]any{"keys": []any{"attributes.http.tls"}}, ret.Updated[1].Config.Config["as_string"])

		assert.Len(t, ret.Deleted, 1)
		assert.Equal(t, "bar", ret.Deleted[0].ID)

		assert.Len(t, ret.Keep, 1)
		assert.Equal(t, "baz", ret.Keep[0].ID)
	})
}
