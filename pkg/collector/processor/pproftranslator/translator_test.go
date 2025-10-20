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
)

func TestDefaultTranslator(t *testing.T) {
	var transaltor defaultTranslator

	t.Run("invalid data type", func(t *testing.T) {
		pd := define.ProfilesRawData{
			Data: "invalid",
		}
		_, err := transaltor.Translate(pd)
		assert.Error(t, err)
	})

	t.Run("empty data", func(t *testing.T) {
		pd := define.ProfilesRawData{
			Data: define.ProfilePprofFormatOrigin{},
		}
		_, err := transaltor.Translate(pd)
		assert.Error(t, err)
	})

	t.Run("invalid profile data", func(t *testing.T) {
		pd := define.ProfilesRawData{
			Data: define.ProfilePprofFormatOrigin("any"),
		}
		profilesData, err := transaltor.Translate(pd)
		assert.Error(t, err)
		assert.Nil(t, profilesData)
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
		profilesData, err := transaltor.Translate(pd)
		assert.NoError(t, err)
		assert.Equal(t, pd.Metadata, profilesData.Metadata)
		assert.Len(t, profilesData.Profiles, 1)
		assert.Equal(t, p.Sample[0].Value, profilesData.Profiles[0].Sample[0].Value)
	})
}
