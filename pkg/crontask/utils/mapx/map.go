// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mapx

// IsMapKey 判断某个字符串为字典的值key
func IsMapKey(str string, dict map[string]interface{}) bool {
	if _, ok := dict[str]; ok {
		return true
	}
	return false
}

// GetMapKeys 获取字典的key
func GetMapKeys(dict map[string]interface{}) []string {
	var keys []string
	for key := range dict {
		keys = append(keys, key)
	}
	return keys
}

// GetValWithDefault get the default value, if key not found, return default value
func GetValWithDefault(m map[string]interface{}, key string, val interface{}) interface{} {
	// 如果可以查询到，则直接返回数据
	if v, ok := m[key]; ok {
		return v
	}
	return val
}
