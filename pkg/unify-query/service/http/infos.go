// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

type InfoType string

const (
	TagKeys    InfoType = "tag_keys"
	TagValues  InfoType = "tag_values"
	FieldKeys  InfoType = "field_keys"
	Series     InfoType = "series"
	TimeSeries InfoType = "time_series"
	FieldMap   InfoType = "field_map"
)

// Params
type Params struct {
	TsDBs      structured.TsDBs   `json:"tsdbs"`
	DataSource string             `json:"data_source"`
	TableID    structured.TableID `json:"table_id"`
	Metric     string             `json:"metric_name"`
	// IsRegexp 指标是否使用正则查询
	IsRegexp bool `json:"is_regexp" example:"false"`

	Conditions structured.Conditions `json:"conditions"`
	Keys       []string              `json:"keys"`

	Limit  int `json:"limit"`
	Slimit int `json:"slimit"`

	Start string `json:"start_time"`
	End   string `json:"end_time"`

	Timezone string `json:"timezone,omitempty" example:"Asia/Shanghai"`
}

func (p *Params) StartTimeUnix() (int64, error) {
	return strconv.ParseInt(p.Start, 10, 64)
}

func (p *Params) EndTimeUnix() (int64, error) {
	return strconv.ParseInt(p.End, 10, 64)
}
