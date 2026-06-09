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

func TestTsDBV2_GetStorageIDRanges(t *testing.T) {
	var (
		start      = time.UnixMilli(1757401305000) // 2025-09-09 15:01:45
		switchTime = time.Unix(1757401605, 0)      // 2025-09-09 15:06:45
		end        = time.UnixMilli(1757401905000) // 2025-09-09 15:11:45
		records    = []Record{
			{
				StorageID:  "16",
				EnableTime: switchTime.Unix(),
			},
			{
				StorageID:  "5",
				EnableTime: 1756969402,
			},
		}
	)

	testCases := map[string]struct {
		db       *TsDBV2
		start    time.Time
		end      time.Time
		expected []StorageIDRange
	}{
		"no storage cluster records keeps only storage id": {
			db: &TsDBV2{
				StorageID: "16",
			},
			start: start,
			end:   end,
			expected: []StorageIDRange{
				{
					StorageID: "16",
				},
			},
		},
		"query crosses storage switch time": {
			db: &TsDBV2{
				StorageID:             "16",
				StorageClusterRecords: records,
			},
			start: start,
			end:   end,
			expected: []StorageIDRange{
				{
					StorageID:  "16",
					Start:      switchTime,
					End:        end,
					QueryStart: switchTime,
					QueryEnd:   end.Add(StorageClusterRecordOverlap),
				},
				{
					StorageID:  "5",
					Start:      start,
					End:        switchTime,
					QueryStart: start.Add(-StorageClusterRecordOverlap),
					QueryEnd:   switchTime,
				},
			},
		},
		"query after storage switch time": {
			db: &TsDBV2{
				StorageID:             "16",
				StorageClusterRecords: records,
			},
			start: switchTime.Add(time.Minute),
			end:   switchTime.Add(5 * time.Minute),
			expected: []StorageIDRange{
				{
					StorageID:  "16",
					Start:      switchTime.Add(time.Minute),
					End:        switchTime.Add(5 * time.Minute),
					QueryStart: switchTime,
					QueryEnd:   switchTime.Add(65 * time.Minute),
				},
				{
					StorageID:  "5",
					QueryStart: switchTime.Add(-59 * time.Minute),
					QueryEnd:   switchTime,
				},
			},
		},
		"切换九十分钟后不再查询旧存储": {
			db: &TsDBV2{
				StorageID:             "16",
				StorageClusterRecords: records,
			},
			start: switchTime.Add(90 * time.Minute),
			end:   switchTime.Add(95 * time.Minute),
			expected: []StorageIDRange{
				{
					StorageID:  "16",
					Start:      switchTime.Add(90 * time.Minute),
					End:        switchTime.Add(95 * time.Minute),
					QueryStart: switchTime.Add(30 * time.Minute),
					QueryEnd:   switchTime.Add(155 * time.Minute),
				},
			},
		},
		"query before storage switch time": {
			db: &TsDBV2{
				StorageID:             "16",
				StorageClusterRecords: records,
			},
			start: switchTime.Add(-5 * time.Minute),
			end:   switchTime.Add(-time.Minute),
			expected: []StorageIDRange{
				{
					StorageID:  "16",
					QueryStart: switchTime,
					QueryEnd:   switchTime.Add(59 * time.Minute),
				},
				{
					StorageID:  "5",
					Start:      switchTime.Add(-5 * time.Minute),
					End:        switchTime.Add(-time.Minute),
					QueryStart: switchTime.Add(-65 * time.Minute),
					QueryEnd:   switchTime,
				},
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.db.GetStorageIDRanges(tc.start, tc.end))
		})
	}

	t.Run("额外回看窗口覆盖切换点时保留旧存储有效 route", func(t *testing.T) {
		start := switchTime.Add(90 * time.Minute)
		end := switchTime.Add(95 * time.Minute)
		db := &TsDBV2{
			StorageID:             "16",
			StorageClusterRecords: records,
		}

		assert.Equal(t, []StorageIDRange{
			{
				StorageID:  "16",
				Start:      switchTime,
				End:        end,
				QueryStart: switchTime,
				QueryEnd:   end.Add(StorageClusterRecordOverlap),
			},
			{
				StorageID:  "5",
				Start:      switchTime.Add(-30 * time.Minute),
				End:        switchTime,
				QueryStart: switchTime.Add(-30 * time.Minute),
				QueryEnd:   switchTime,
			},
		}, db.GetStorageIDRangesWithOverlap(start, end, 2*time.Hour))
	})

	t.Run("avg_over_time 首个点回看旧 route 时保留旧存储权重范围", func(t *testing.T) {
		start := switchTime.Add(30 * time.Minute)
		end := switchTime.Add(35 * time.Minute)
		db := &TsDBV2{
			StorageID:             "16",
			StorageClusterRecords: records,
		}

		assert.Equal(t, []StorageIDRange{
			{
				StorageID:  "16",
				Start:      switchTime,
				End:        end,
				QueryStart: switchTime,
				QueryEnd:   end.Add(StorageClusterRecordOverlap),
			},
			{
				StorageID:  "5",
				Start:      switchTime.Add(-90 * time.Minute),
				End:        switchTime,
				QueryStart: switchTime.Add(-90 * time.Minute),
				QueryEnd:   switchTime,
			},
		}, db.GetStorageIDRangesWithOverlap(start, end, 2*time.Hour))
	})

	t.Run("forward offset 扩展未来 route 查询窗口", func(t *testing.T) {
		start := switchTime.Add(-90 * time.Minute)
		end := switchTime.Add(-85 * time.Minute)
		db := &TsDBV2{
			StorageID:             "16",
			StorageClusterRecords: records,
		}

		assert.Equal(t, []StorageIDRange{
			{
				StorageID:  "16",
				Start:      switchTime,
				End:        switchTime.Add(35 * time.Minute),
				QueryStart: switchTime,
				QueryEnd:   switchTime.Add(35 * time.Minute),
			},
			{
				StorageID:  "5",
				Start:      start,
				End:        switchTime,
				QueryStart: switchTime.Add(-150 * time.Minute),
				QueryEnd:   switchTime,
			},
		}, db.GetStorageIDRangesWithDirectionalOverlap(start, end, 0, 2*time.Hour))
	})

	t.Run("纯回看窗口不扩展未来 route 查询窗口", func(t *testing.T) {
		start := switchTime.Add(-90 * time.Minute)
		end := switchTime.Add(-85 * time.Minute)
		db := &TsDBV2{
			StorageID:             "16",
			StorageClusterRecords: records,
		}

		assert.Equal(t, []StorageIDRange{
			{
				StorageID:  "5",
				Start:      start.Add(-2 * time.Hour),
				End:        end,
				QueryStart: start.Add(-2 * time.Hour),
				QueryEnd:   end.Add(StorageClusterRecordOverlap),
			},
		}, db.GetStorageIDRangesWithDirectionalOverlap(start, end, 2*time.Hour, 0))
	})
}
