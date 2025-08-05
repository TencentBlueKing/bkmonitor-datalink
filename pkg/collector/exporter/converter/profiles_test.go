// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package converter

import (
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestConvertProfilesData(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		var events []define.Event
		TestConverter.Convert(&define.Record{
			RecordType: define.RecordProfiles,
			Data: &define.ProfilesData{Profiles: []*profile.Profile{{
				TimeNanos:     time.Now().UnixNano(),
				DurationNanos: int64(time.Second),
				SampleType:    []*profile.ValueType{{Type: "samples", Unit: "count"}},
				Sample:        []*profile.Sample{{Value: []int64{1000}, Location: make([]*profile.Location, 0)}},
				PeriodType:    &profile.ValueType{Type: "goroutine", Unit: "seconds"},
			}}},
			Token: define.Token{
				AppName: "testa",
				BizId:   1,
			},
		}, func(evts ...define.Event) {
			events = append(events, evts...)
		})

		assert.Len(t, events, 1)

		event := events[0]
		data := event.Data()
		assert.Equal(t, int32(1), data["biz_id"])
		assert.Equal(t, define.RecordProfiles, event.RecordType())
		assert.Equal(t, int32(0), event.DataId())
		assert.Equal(t, "testa", data["app"])
		assert.Equal(t, "goroutine", data["type"])
		assert.Equal(t, "default", data["service_name"])
	})

	t.Run("Empty Profiles", func(t *testing.T) {
		var hit bool
		TestConverter.Convert(&define.Record{
			RecordType: define.RecordProfiles,
			Data:       &define.ProfilesData{Profiles: nil},
			Token: define.Token{
				AppName: "testa",
				BizId:   1,
			},
		}, func(evts ...define.Event) {
			hit = true
		})

		assert.False(t, hit)
	})
}

func TestGetSvrNameAndTags(t *testing.T) {
	p := profilesConverter{}
	t.Run("Success", func(t *testing.T) {
		metadata := define.ProfileMetadata{
			StartTime: time.Now(),
			EndTime:   time.Now(),
			AppName:   "testApp",
			BkBizID:   1,
			SpyName:   "testSpy",
			Format:    "testFormat",
			Units:     "",
			Tags: map[string]string{
				"serviceName": "testService",
				"env":         "production",
				"version":     "v1.0",
			},
		}
		profilesData := define.ProfilesData{
			Metadata: metadata,
		}

		svrName, tags := p.getSvrNameAndTags(&profilesData)
		assert.Equal(t, "testService", svrName)
		assert.Len(t, tags, 2)
		assert.Equal(t, []string{"production"}, tags["env"])
		assert.Equal(t, []string{"v1.0"}, tags["version"])
	})

	t.Run("NoServiceName", func(t *testing.T) {
		metadata := define.ProfileMetadata{
			StartTime: time.Now(),
			EndTime:   time.Now(),
			AppName:   "testApp",
			BkBizID:   1,
			SpyName:   "testSpy",
			Format:    "testFormat",
			Units:     "",
			Tags: map[string]string{
				"env":     "production",
				"version": "v1.0",
			},
		}
		profilesData := define.ProfilesData{
			Metadata: metadata,
		}

		svrName, tags := p.getSvrNameAndTags(&profilesData)
		assert.Equal(t, "testApp", svrName)
		assert.Len(t, tags, 2)
		assert.Equal(t, []string{"production"}, tags["env"])
		assert.Equal(t, []string{"v1.0"}, tags["version"])
	})

	t.Run("NoAppName", func(t *testing.T) {
		metadata := define.ProfileMetadata{
			StartTime: time.Now(),
			EndTime:   time.Now(),
			AppName:   "",
			BkBizID:   1,
			SpyName:   "testSpy",
			Format:    "testFormat",
			Units:     "",
			Tags: map[string]string{
				"env":     "production",
				"version": "v1.0",
			},
		}
		profilesData := define.ProfilesData{
			Metadata: metadata,
		}

		svrName, tags := p.getSvrNameAndTags(&profilesData)
		assert.Equal(t, "default", svrName)
		assert.Len(t, tags, 2)
		assert.Equal(t, []string{"production"}, tags["env"])
		assert.Equal(t, []string{"v1.0"}, tags["version"])
	})
}

func TestProfilesConverterMergeTagsToLabels(t *testing.T) {
	converter := profilesConverter{}

	profileData := &profile.Profile{
		Sample: []*profile.Sample{
			{
				Label: map[string][]string{
					"key1": {"value1"},
				},
			},
			{
				Label: nil,
			},
		},
	}
	tags := map[string][]string{
		"key2": {"value2"},
	}

	converter.mergeTagsToLabels(profileData, tags)
	expected := []*profile.Sample{
		{
			Label: map[string][]string{
				"key1": {"value1"},
				"key2": {"value2"},
			},
		},
		{
			Label: map[string][]string{
				"key2": {"value2"},
			},
		},
	}
	assert.Equal(t, profileData.Sample, expected)
}
