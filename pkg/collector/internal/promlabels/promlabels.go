// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promlabels

import "github.com/prometheus/prometheus/prompb"

type Labels []prompb.Label

func (ls *Labels) Get(name string) (prompb.Label, bool) {
	if ls == nil {
		return prompb.Label{}, false
	}
	for i := 0; i < len(*ls); i++ {
		if (*ls)[i].Name == name {
			return (*ls)[i], true
		}
	}
	return prompb.Label{}, false
}

func (ls *Labels) Upsert(name, value string) {
	if ls == nil {
		return
	}
	for i := 0; i < len(*ls); i++ {
		if (*ls)[i].Name == name {
			(*ls)[i].Value = value
			return
		}
	}
	*ls = append(*ls, prompb.Label{Name: name, Value: value})
}
