// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mapx

import (
	"golang.org/x/exp/constraints"
)

// IsMapKey 判断某个值是否为字典的值key
func IsMapKey[T constraints.Ordered](key T, dict map[T]any) bool {
	if _, ok := dict[key]; ok {
		return true
	}
	return false
}

// GetMapKeys 获取字典的key
func GetMapKeys[T constraints.Ordered, K any](dict map[T]K) []T {
	var keys []T
	for key := range dict {
		keys = append(keys, key)
	}
	return keys
}

// AddSliceItems 向value为slice类型的map中插入元素
func AddSliceItems[T constraints.Ordered, K any](dict map[T][]K, key T, items ...K) {
	values, ok := dict[key]
	if ok {
		dict[key] = append(values, items...)
	} else {
		dict[key] = items
	}
}

// GetValWithDefault get the default value, if key not found, return default value
func GetValWithDefault[T constraints.Ordered, K any](m map[T]K, key T, val K) K {
	// 如果可以查询到，则直接返回数据
	if v, ok := m[key]; ok {
		return v
	}
	return val
}

// SetDefault set the default value, if key not found, set the default value
func SetDefault(m *map[string]any, key string, val any) {
	_, ok := (*m)[key]
	if !ok {
		(*m)[key] = val
	}
}
