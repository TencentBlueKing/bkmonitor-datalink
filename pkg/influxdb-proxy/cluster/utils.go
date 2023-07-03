// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cluster

// ConvertListToMap 将列表转换为map，所有的列表项都会被转换为key，value为true
func ConvertListToMap(list []string) map[string]bool {
	resultMap := make(map[string]bool)
	// 列表为空则直接返回
	if list == nil {
		return resultMap
	}
	for _, v := range list {
		resultMap[v] = true
	}
	return resultMap
}
