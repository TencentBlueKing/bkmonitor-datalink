// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

const (
	Auto = "auto"
)

// 可调整的promql
// 不解析过滤dataIDList
// 指标名是否要替换

// option
type Option struct {
	IsAlignInfluxdb bool // 是否开启与influxdb对齐
	IsFilterCond    bool // 是否过滤查询condition
	IsRealFieldName bool // 是否将指标名替换为真实指标名(eg:bkmonitor:db:measurement:metric)

	IsReplaceBizID bool // 是否取代query中的bizID
	ReplaceBizIds  []string

	IsOnlyParse bool // 是否仅转化

	SpaceUid string
}

// GenerateOptions 判断生成options参数
// 根据query的Start,End,MaxSourceResolution，和Conditions 判断
func GenerateOptions(
	query *CombinedQueryParams, onlyParse bool, replaceBizIDs []string, spaceUid string,
) (*Option, error) {
	options := new(Option)

	// 1. 如果仅仅转换promql
	if onlyParse {
		options.IsOnlyParse = true
		// 取消对齐
		options.IsAlignInfluxdb = false
		// 不用将条件中的bk_biz_id等过滤出真实data_id或table_id
		options.IsFilterCond = false
		// 解析queryList时，需要使用真实指标名
		options.IsRealFieldName = true
		return options, nil
	}

	if query == nil {
		return options, nil
	}

	if len(replaceBizIDs) != 0 {
		options.ReplaceBizIds = replaceBizIDs
		options.IsReplaceBizID = true
	}

	// 打开对齐
	options.IsAlignInfluxdb = true
	// 需要条件中的bk_biz_id等过滤出真实data_id或table_id
	options.IsFilterCond = true
	// 解析queryList时，不使用真实指标名
	options.IsRealFieldName = false

	// 是否使用 spaceUid 和 spaceType 查询
	if spaceUid != "" {
		options.SpaceUid = spaceUid
		options.IsFilterCond = false
	}
	return options, nil
}
