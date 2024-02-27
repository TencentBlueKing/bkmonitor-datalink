// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pprofconverter

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/pprofconverter/jfr"
)

// isEqual 因为 sdk 里面解析原因，原始数组中空数组会被解析为 nil，所以测试时需要认为他们是一致的
func isEqual(a, b interface{}) bool {
	v1 := reflect.ValueOf(a)
	v2 := reflect.ValueOf(b)

	if v1.Kind() != v2.Kind() {
		return false
	}

	for i := 0; i < v1.NumField(); i++ {
		f1 := v1.Field(i)
		f2 := v2.Field(i)

		if f1.Kind() == reflect.Slice {
			if (f1.IsNil() && f2.Len() == 0) || (f2.IsNil() && f1.Len() == 0) {
				continue
			}
		}

	}

	return true
}

func TestDefaultConverter(t *testing.T) {
	d := &DefaultPprofable{}

	t.Run("invalid data type", func(t *testing.T) {
		pd := define.ProfilesRawData{
			Data: "invalid",
		}
		_, err := d.Parse(pd)
		assert.Error(t, err)
	})

	t.Run("empty data", func(t *testing.T) {
		pd := define.ProfilesRawData{
			Data: define.ProfilePprofFormatOrigin{},
		}
		_, err := d.Parse(pd)
		assert.Error(t, err)
	})

	t.Run("valid data", func(t *testing.T) {
		p := &profile.Profile{
			TimeNanos:     time.Now().UnixNano(),
			DurationNanos: int64(time.Second),
			SampleType:    []*profile.ValueType{{Type: "samples", Unit: "count"}},
			Sample:        []*profile.Sample{{Value: []int64{1000}, Location: make([]*profile.Location, 0)}},
		}
		var buf bytes.Buffer
		err := p.Write(&buf)
		assert.NoError(t, err)

		pd := define.ProfilesRawData{
			Data: define.ProfilePprofFormatOrigin(buf.Bytes()),
		}
		profilesData, err := d.Parse(pd)
		assert.NoError(t, err)
		assert.Equal(t, pd.Metadata, profilesData.Metadata)
		assert.Equal(t, 1, len(profilesData.Profiles))
		assert.True(t, isEqual(*p.Sample[0], *profilesData.Profiles[0].Sample[0]))
	})
}

func TestJfrConverter(t *testing.T) {
	c := &jfr.Converter{}

	t.Run("invalid data type", func(t *testing.T) {
		pd := define.ProfilesRawData{
			Data: "invalid",
		}
		_, err := c.Parse(pd)
		assert.Error(t, err)
	})

	t.Run("valid data", func(t *testing.T) {
		data, err := jfr.ReadGzipFile("testdata/jfr_cortex-dev-01__kafka-0__cpu_lock_alloc__0.jfr.gz")
		assert.NoError(t, err)

		jfrData := define.ProfilesRawData{Data: define.ProfileJfrFormatOrigin{Jfr: data}}
		_, err = c.Parse(jfrData)
		assert.NoError(t, err)
	})
}

func TestSwitchConverter(t *testing.T) {
	c := Config{Type: "spy_converter"}
	entry := NewPprofConverter(c)
	assert.IsType(t, entry, &spyNameJudgeConverter{})

	c = Config{Type: "default"}
	entry = NewPprofConverter(c)
	assert.IsType(t, entry, &spyNameJudgeConverter{})
}
