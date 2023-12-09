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

func (c profilesConverter) ToDataID(record *define.Record) int32 {
	return record.Token.ProfilesDataId
}

func (c profilesConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return profilesEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c profilesConverter) Convert(record *define.Record, f define.GatherFunc) {
	dataId := c.ToDataID(record)
	var buf bytes.Buffer
	buf.Write(record.Data.([]byte))

	pp, err := profile.Parse(&buf)
	if err != nil {
		logger.Errorf("failed to parse profile: %v", err)
		return
	}

	var protoBuf bytes.Buffer
	if err := pp.WriteUncompressed(&protoBuf); err != nil {
		logger.Errorf("failed to write uncompressed profile: %v", err)
		return
	}

	events := []define.Event{c.ToEvent(record.Token, dataId, common.MapStr{
		"data":   protoBuf.Bytes(),
		"type":   pp.PeriodType.Type,
		"app":    record.Token.AppName,
		"biz_id": record.Token.BizId,
	})}

	f(events...)
}
