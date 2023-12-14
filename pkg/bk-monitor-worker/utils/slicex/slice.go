// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package slicex

import (
	"golang.org/x/exp/constraints"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

// RemoveItem remove the item from string array
func RemoveItem(l []string, s string) []string {
	i := 0
	for _, val := range l {
		if val != s {
			l[i] = val
			i++
		}
	}
	return l[:i]
}

// RemoveDuplicate 可排序类型的去重
func RemoveDuplicate[T constraints.Ordered](source []T) []T {
	temp := make(map[T]bool)
	var target []T
	for _, s := range source {
		if exist := temp[s]; !exist {
			target = append(target, s)
			temp[s] = true
		}
	}
	return target
}

func ChunkSlice[T any](bigSlice []T, size int) [][]T {
	if size <= 0 {
		size = cfg.DefaultDBFilterSize
	}
	var chunkList [][]T
	for i := 0; i < len(bigSlice); i += size {
		end := i + size
		if end > len(bigSlice) {
			end = len(bigSlice)
		}
		chunkList = append(chunkList, bigSlice[i:end])
	}
	return chunkList
}
