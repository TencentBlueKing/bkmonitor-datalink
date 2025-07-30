// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

func TestScrollSliceRetryMechanism(t *testing.T) {
	s, _ := miniredis.Run()
	defer s.Close()

	options := &goRedis.UniversalOptions{
		Addrs: []string{s.Addr()},
		DB:    0,
	}

	ctx := context.Background()
	redis.SetInstance(ctx, "test", options)

	tests := []struct {
		name               string
		storageType        string
		connect            string
		tableID            string
		operations         []sliceOperation
		expectedSliceCount int
		expectedStatuses   map[int]string
	}{
		{
			name:               "ES 正常初始化",
			storageType:        "elasticsearch",
			connect:            "es-test:9200",
			tableID:            "test_table_1",
			operations:         []sliceOperation{},
			expectedSliceCount: 3,
			expectedStatuses: map[int]string{
				0: "pending",
				1: "pending",
				2: "pending",
			},
		},
		{
			name:        "ES 单个 slice 失败后重试",
			storageType: "elasticsearch",
			connect:     "es-test:9200",
			tableID:     "test_table_2",
			operations: []sliceOperation{
				{action: "fail", sliceIndex: 0, times: 1},
			},
			expectedSliceCount: 3,
			expectedStatuses: map[int]string{
				0: "pending", // 失败后重置为 pending
				1: "pending",
				2: "pending",
			},
		},
		{
			name:        "ES slice 达到最大失败次数后停止",
			storageType: "elasticsearch",
			connect:     "es-test:9200",
			tableID:     "test_table_3",
			operations: []sliceOperation{
				{action: "fail", sliceIndex: 0, times: 3}, // 失败 3 次（达到限制）
			},
			expectedSliceCount: 2, // slice 0 被排除
			expectedStatuses: map[int]string{
				0: "stop", // 超过失败次数限制
				1: "pending",
				2: "pending",
			},
		},
		{
			name:               "Doris 正常初始化",
			storageType:        "bk_sql",
			connect:            "",
			tableID:            "doris_table_1",
			operations:         []sliceOperation{},
			expectedSliceCount: 3,
			expectedStatuses: map[int]string{
				0: "pending",
				1: "pending",
				2: "pending",
			},
		},
		{
			name:        "Doris slice 失败重试",
			storageType: "bk_sql",
			connect:     "",
			tableID:     "doris_table_2",
			operations: []sliceOperation{
				{action: "fail", sliceIndex: 1, times: 2},
			},
			expectedSliceCount: 3,
			expectedStatuses: map[int]string{
				0: "pending",
				1: "pending", // 失败后重置为 pending
				2: "pending",
			},
		},
		{
			name:        "Doris slice 超过失败限制",
			storageType: "bk_sql",
			connect:     "",
			tableID:     "doris_table_3",
			operations: []sliceOperation{
				{action: "fail", sliceIndex: 2, times: 3},
			},
			expectedSliceCount: 2, // slice 2 被排除
			expectedStatuses: map[int]string{
				0: "pending",
				1: "pending",
				2: "stop", // 超过失败次数限制
			},
		},
		{
			name:        "多个 slice 失败情况",
			storageType: "elasticsearch",
			connect:     "es-test:9200",
			tableID:     "test_table_4",
			operations: []sliceOperation{
				{action: "fail", sliceIndex: 0, times: 3}, // slice 0 超过限制
				{action: "fail", sliceIndex: 1, times: 1}, // slice 1 失败一次
			},
			expectedSliceCount: 2, // 只有 slice 1, 2 可用
			expectedStatuses: map[int]string{
				0: "stop",    // 超过失败次数限制
				1: "pending", // 失败后重试
				2: "pending", // 正常
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := redis.NewScrollSession(fmt.Sprintf("test_%s", tt.tableID), 3, 5*time.Minute, 10)
			ctx := context.Background()

			_, err := session.MakeSlices(tt.storageType, tt.connect, tt.tableID)
			assert.NoError(t, err)

			for _, op := range tt.operations {
				for i := 0; i < op.times; i++ {
					if op.action == "fail" {
						if tt.storageType == consul.ElasticsearchStorageType {
							err := session.UpdateScrollID(ctx, tt.connect, tt.tableID, "", &op.sliceIndex, redis.StatusFailed)
							assert.NoError(t, err)
						} else if tt.storageType == consul.BkSqlStorageType {
							err := session.UpdateDoris(ctx, tt.tableID, &op.sliceIndex, redis.StatusFailed)
							assert.NoError(t, err)
						}
					}
				}
			}

			slices, err := session.MakeSlices(tt.storageType, tt.connect, tt.tableID)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedSliceCount, len(slices), "slice 数量不符合预期")

			for sliceIndex, expectedStatus := range tt.expectedStatuses {
				actualStatus := getSliceStatus(session, tt.storageType, tt.connect, tt.tableID, sliceIndex)
				assert.Equal(t, expectedStatus, actualStatus,
					"slice %d 状态不符合预期，期望 %s，实际 %s", sliceIndex, expectedStatus, actualStatus)
			}

			returnedSliceIndices := make(map[int]bool)
			for _, slice := range slices {
				returnedSliceIndices[slice.SliceIdx] = true
				status := getSliceStatus(session, tt.storageType, tt.connect, tt.tableID, slice.SliceIdx)
				assert.NotEqual(t, "stop", status, "返回的 slice %d 不应该是 stop 状态", slice.SliceIdx)
				assert.NotEqual(t, "completed", status, "返回的 slice %d 不应该是 completed 状态", slice.SliceIdx)
			}

			for sliceIndex, expectedStatus := range tt.expectedStatuses {
				if expectedStatus == "stop" || expectedStatus == "completed" {
					assert.False(t, returnedSliceIndices[sliceIndex],
						"slice %d 状态为 %s，不应该在返回结果中", sliceIndex, expectedStatus)
				}
			}
		})
	}
}

type sliceOperation struct {
	action     string // "fail", "complete" 等
	sliceIndex int
	times      int
}

func getSliceStatus(session *redis.ScrollSession, storageType, connect, tableID string, sliceIndex int) string {
	key := generateScrollSliceStatusKey(storageType, connect, tableID, sliceIndex)
	if val, exists := session.ScrollIDs[key]; exists {
		return val.Status
	}
	return ""
}

func generateScrollSliceStatusKey(storageType, connect, tableID string, sliceIdx int) string {
	return fmt.Sprintf("%s:%s:%s:%d", storageType, connect, tableID, sliceIdx)
}
