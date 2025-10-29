// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package query

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTsDBV2_GetStorageIDs(t *testing.T) {
	db := &TsDBV2{
		StorageID: "16",
		StorageClusterRecords: []Record{
			{
				StorageID:  "16",
				EnableTime: 1757401605, // 2025-09-09 15:06:45
			},
			{
				StorageID:  "5",
				EnableTime: 1756969402, // 2025-09-04 15:03:22
			},
			{
				StorageID:  "27",
				EnableTime: 1756957849, // 2025-09-04 11:50:49
			},
			{
				StorageID:  "26",
				EnableTime: 1756894884, // 2025-09-03 18:21:24
			},
			{
				StorageID:  "16",
				EnableTime: 1753789890, // 2025-07-29 19:51:30
			},
		},
	}

	start := time.UnixMilli(1757399805337) // 2025-09-09 14:36:45
	end := time.UnixMilli(1757401605337)   // 2025-09-09 15:06:45

	ids := db.GetStorageIDs(start, end)
	// 由于set返回顺序不确定，需要排序后比较
	sort.Strings(ids)
	expected := []string{"16", "5"}
	sort.Strings(expected)
	assert.Equal(t, expected, ids)
}
