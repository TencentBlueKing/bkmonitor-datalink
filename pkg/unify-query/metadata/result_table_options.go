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
	From        *int   `json:"from,omitempty"`
	ScrollID    string `json:"scroll_id,omitempty"`
	SearchAfter []any  `json:"search_after,omitempty"`
	SliceIndex  *int   `json:"slice_index,omitempty"`
	SliceMax    *int   `json:"slice_max,omitempty"`

	FieldType map[string]string `json:"-"`

	SQL          string           `json:"sql,omitempty"`
	ResultSchema []map[string]any `json:"result_schema,omitempty"`
}

func (o ResultTableOptions) getKey(tableID, address string) string {
	key := tableID
	if address != "" {
		key = tableID + "|" + address
	}
	return key
}

func (o ResultTableOptions) SetOption(tableID, address string, option *ResultTableOption) {
	if option == nil {
		return
	}
	o[o.getKey(tableID, address)] = option
}

func (o ResultTableOptions) GetOption(tableID, address string) *ResultTableOption {
	if o == nil {
		return nil
	}

	if option, ok := o[o.getKey(tableID, address)]; ok {
		return option
	}
	return nil
}

func (o ResultTableOptions) MergeOptions(options ResultTableOptions) {
	if o == nil {
		return
	}
	for k, v := range options {
		o[k] = v
	}
}

// IsCrop 是否裁剪数据
func (o ResultTableOptions) IsCrop() bool {
	for _, v := range o {
		if v.ScrollID != "" || len(v.SearchAfter) > 0 {
			return false
		}
	}
	return true
}
