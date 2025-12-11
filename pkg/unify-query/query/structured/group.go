// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

// Dimensions 维度组合
type Dimensions []string

func MergeDimensions(original []string, addDimensions []string) []string {
	if len(addDimensions) == 0 {
		return original
	}

	seen := make(map[string]bool)
	var result []string

	// 先添加原有维度
	for _, dim := range original {
		if !seen[dim] {
			seen[dim] = true
			result = append(result, dim)
		}
	}

	for _, dim := range addDimensions {
		if !seen[dim] {
			seen[dim] = true
			result = append(result, dim)
		}
	}

	return result
}
