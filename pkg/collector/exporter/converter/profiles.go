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
	"bytes"

	"github.com/elastic/beats/libbeat/common"
	"github.com/google/pprof/profile"
	"golang.org/x/exp/maps"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type profilesEvent struct {
	define.CommonEvent
}

func (e profilesEvent) RecordType() define.RecordType {
	return define.RecordProfiles
}

var ProfilesConverter EventConverter = profilesConverter{}

type profilesConverter struct{}

func (c profilesConverter) Clean() {}

func (c profilesConverter) ToDataID(record *define.Record) int32 {
	return record.Token.ProfilesDataId
}

func (c profilesConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return profilesEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c profilesConverter) Convert(record *define.Record, f define.GatherFunc) {
	dataId := c.ToDataID(record)
	token := record.Token

	profileData := record.Data.(*define.ProfilesData)
	if profileData == nil || len(profileData.Profiles) == 0 {
		logger.Errorf("skip empty profiles, token: %s, app: %s", token.Original, token.BizApp())
		return
	}

	svrName, tags := c.getSvrNameAndTags(profileData)
	needMergeTags := len(tags) > 0

	for i, p := range profileData.Profiles {
		if needMergeTags {
			c.mergeTagsToLabels(p, tags)
		}

		var protoBuf bytes.Buffer
		if err := p.WriteUncompressed(&protoBuf); err != nil {
			logger.Errorf(
				"write uncompressed profile on index(%d) failed, token: %s app: %s, err: %s", i, token.Original, token.BizApp(), err,
			)
			return
		}

		event := c.ToEvent(record.Token, dataId, common.MapStr{
			"data":         protoBuf.Bytes(),
			"type":         p.PeriodType.Type,
			"app":          record.Token.AppName,
			"biz_id":       record.Token.BizId,
			"service_name": svrName,
		})

		f(event)
	}
}

func (c profilesConverter) getSvrNameAndTags(pd *define.ProfilesData) (string, map[string][]string) {
	metadataTags := pd.Metadata.Tags
	var svrName string
	var exist bool

	svrKeys := []string{"serviceName", "SERVICE_NAME", "service_name", "service", "SERVICE"}
	for _, key := range svrKeys {
		if svrName, exist = metadataTags[key]; exist {
			break
		}
	}
	if !exist {
		svrName = pd.Metadata.AppName
	}

	if svrName == "" {
		svrName = "default"
	}

	for _, key := range svrKeys {
		delete(metadataTags, key)
	}

	// 将值转为数组，兼容 Profile.Sample.Labels 的格式
	tags := make(map[string][]string, len(metadataTags))
	for k, v := range metadataTags {
		tags[k] = []string{v}
	}

	return svrName, tags
}

// mergeTagsToLabels 将 Tags 内容合并至 Sample.Label 中
func (c profilesConverter) mergeTagsToLabels(pd *profile.Profile, tags map[string][]string) {
	for i := 0; i < len(pd.Sample); i++ {
		if pd.Sample[i].Label == nil {
			pd.Sample[i].Label = make(map[string][]string)
		}

		maps.Copy(pd.Sample[i].Label, tags)
	}
}
