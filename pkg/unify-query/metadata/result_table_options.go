// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

type ResultTableOptions map[string]*ResultTableOption

type ResultTableOption struct {
	Total       int64  `json:"total,omitempty"`
	From        int    `json:"from,omitempty"`
	ScrollID    string `json:"scroll_id,omitempty"`
	SearchAfter []any  `json:"search_after,omitempty"`
}

func (o ResultTableOptions) MergeOptions(options ResultTableOptions) {
	for k, v := range options {
		if s, ok := o[k]; ok {
			v.Total += s.Total
			v.From += s.From

			// ScrollID 和 SearchAfter 保持不变
			v.ScrollID = s.ScrollID
			v.SearchAfter = s.SearchAfter
		}
		o[k] = v
	}
}

func (o ResultTableOptions) SetOption(tableID, address string, option *ResultTableOption) {
	o[tableID+"|"+address] = option
}

func (o ResultTableOptions) GetOption(tableID, address string) *ResultTableOption {
	if option, ok := o[tableID+"|"+address]; ok {
		return option
	}
	return &ResultTableOption{
		From:  0,
		Total: 0,
	}
}

func (o ResultTableOptions) GetTotal() int64 {
	var total int64
	for _, v := range o {
		total += v.Total
	}
	return total
}

func (o ResultTableOptions) IsMultiFrom() bool {
	if len(o) == 0 {
		return false
	}

	for _, v := range o {
		if v.ScrollID != "" || len(v.SearchAfter) > 0 {
			return false
		}
	}
	return true
}
