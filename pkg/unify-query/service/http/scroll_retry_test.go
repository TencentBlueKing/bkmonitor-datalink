// // Tencent is pleased to support the open source community by making
// // 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// // Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// // Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// // You may obtain a copy of the License at http://opensource.org/licenses/MIT
// // Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// // an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// // specific language governing permissions and limitations under the License.
package http

//
//import (
//	"context"
//	"fmt"
//	"testing"
//	"time"
//
//	miniredis "github.com/alicebob/miniredis/v2"
//	goRedis "github.com/go-redis/redis/v8"
//	"github.com/stretchr/testify/assert"
//
//	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
//	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
//	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
//	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
//	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
//	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/elasticsearch"
//)
//
//func TestScrollSliceRetryMechanism(t *testing.T) {
//	s, _ := miniredis.Run()
//	defer s.Close()
//
//	options := &goRedis.UniversalOptions{
//		Addrs: []string{s.Addr()},
//		DB:    0,
//	}
//
//	ctx := context.Background()
//	redis.SetInstance(ctx, "test", options)
//
//	tests := []struct {
//		name               string
//		storageType        string
//		connect            string
//		tableID            string
//		operations         []sliceOperation
//		expectedSliceCount int
//		expectedStatuses   map[int]string
//	}{
//		{
//			name:               "ES 正常初始化",
//			storageType:        consul.ElasticsearchStorageType,
//			connect:            "es-test:9200",
//			tableID:            "test_table_1",
//			operations:         []sliceOperation{},
//			expectedSliceCount: 3,
//			expectedStatuses: map[int]string{
//				0: redis.StatusPending,
//				1: redis.StatusPending,
//				2: redis.StatusPending,
//			},
//		},
//		{
//			name:               "边界测试：MaxSlice为0",
//			storageType:        consul.ElasticsearchStorageType,
//			connect:            "es-test:9200",
//			tableID:            "test_table_boundary_0",
//			operations:         []sliceOperation{},
//			expectedSliceCount: 0,
//			expectedStatuses:   map[int]string{},
//		},
//		{
//			name:               "边界测试：MaxSlice为1",
//			storageType:        consul.ElasticsearchStorageType,
//			connect:            "es-test:9200",
//			tableID:            "test_table_boundary_1",
//			operations:         []sliceOperation{},
//			expectedSliceCount: 1,
//			expectedStatuses: map[int]string{
//				0: redis.StatusPending,
//			},
//		},
//		{
//			name:        "ES 单个 slice 失败后重试",
//			storageType: consul.ElasticsearchStorageType,
//			connect:     "es-test:9200",
//			tableID:     "test_table_2",
//			operations: []sliceOperation{
//				{action: redis.StatusFailed, sliceIndex: 0, times: 1},
//			},
//			expectedSliceCount: 3,
//			expectedStatuses: map[int]string{
//				0: redis.StatusPending, // 失败后重置为 pending
//				1: redis.StatusPending,
//				2: redis.StatusPending,
//			},
//		},
//		{
//			name:        "ES slice 达到最大失败次数后停止",
//			storageType: consul.ElasticsearchStorageType,
//			connect:     "es-test:9200",
//			tableID:     "test_table_3",
//			operations: []sliceOperation{
//				{action: redis.StatusFailed, sliceIndex: 0, times: 3}, // 失败 3 次（达到限制）
//			},
//			expectedSliceCount: 2, // slice 0 被排除
//			expectedStatuses: map[int]string{
//				1: redis.StatusPending,
//				2: redis.StatusPending,
//			},
//		},
//		{
//			name:        "多个 slice 失败情况",
//			storageType: consul.ElasticsearchStorageType,
//			connect:     "es-test:9200",
//			tableID:     "test_table_4",
//			operations: []sliceOperation{
//				{action: redis.StatusFailed, sliceIndex: 0, times: 3}, // slice 0 超过限制
//				{action: redis.StatusFailed, sliceIndex: 1, times: 1}, // slice 1 失败一次
//			},
//			expectedSliceCount: 2, // 只有 slice 1, 2 可用
//			expectedStatuses: map[int]string{
//				1: redis.StatusPending, // 失败后重试
//				2: redis.StatusPending, // 正常
//			},
//		},
//	}
//
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//			var maxSlice int = 3
//			if tt.name == "边界测试：MaxSlice为0" {
//				maxSlice = 0
//			} else if tt.name == "边界测试：MaxSlice为1" {
//				maxSlice = 1
//			}
//
//			tableUUID := fmt.Sprintf("%s|%s", tt.tableID, tt.connect)
//
//			session := redis.NewScrollSession(fmt.Sprintf("test_%s", tt.tableID), maxSlice, 5*time.Minute, 3)
//			ctx := context.Background()
//			instance, err := getInstance(ctx, tt.storageType)
//			if err != nil {
//				t.Fatalf("get instance failed: %v", err)
//			}
//
//			scrollHandler := instance.ScrollHandler()
//
//			slices, err := scrollHandler.MakeSlices(ctx, session, tableUUID)
//			assert.NoError(t, err)
//
//			for _, op := range tt.operations {
//				for i := 0; i < op.times; i++ {
//					resultOption := &metadata.ResultTableOption{
//						SliceIndex: &op.sliceIndex,
//						ScrollID:   op.scrollID,
//						SliceMax:   &maxSlice,
//					}
//					err := scrollHandler.UpdateScrollStatus(ctx, session, tableUUID, resultOption, op.action)
//					assert.NoError(t, err)
//				}
//			}
//
//			slices, err = scrollHandler.MakeSlices(ctx, session, tableUUID)
//			assert.NoError(t, err)
//			assert.Equal(t, tt.expectedSliceCount, len(slices), "slice 数量不符合预期")
//
//			for sliceIndex, expectedStatus := range tt.expectedStatuses {
//				actualStatus := getSliceStatus(session, tt.storageType, tt.connect, tt.tableID, sliceIndex)
//				assert.Equal(t, expectedStatus, actualStatus,
//					"slice %d 状态不符合预期，期望 %s，实际 %s", sliceIndex, expectedStatus, actualStatus)
//			}
//
//			returnedSliceIndices := make(map[int]bool)
//			for _, slice := range slices {
//				returnedSliceIndices[slice.SliceIdx] = true
//				status := getSliceStatus(session, tt.storageType, tt.connect, tt.tableID, slice.SliceIdx)
//				assert.NotEqual(t, "stop", status, "返回的 slice %d 不应该是 stop 状态", slice.SliceIdx)
//				assert.NotEqual(t, "completed", status, "返回的 slice %d 不应该是 completed 状态", slice.SliceIdx)
//			}
//
//			for sliceIndex, expectedStatus := range tt.expectedStatuses {
//				if expectedStatus == "stop" || expectedStatus == "completed" {
//					assert.False(t, returnedSliceIndices[sliceIndex],
//						"slice %d 状态为 %s，不应该在返回结果中", sliceIndex, expectedStatus)
//				}
//			}
//		})
//	}
//}
//
//type sliceOperation struct {
//	action     string // "fail", "complete" 等
//	sliceIndex int
//	scrollID   string
//	times      int
//}
//
//func getSliceStatus(session *redis.ScrollSession, storageType, connect, tableID string, sliceIndex int) string {
//	key := generateScrollSliceStatusKey(storageType, connect, tableID, sliceIndex)
//	if val, exists := session.ScrollIDs[key]; exists {
//		return val.Status
//	}
//	return ""
//}
//
//func getInstance(ctx context.Context, storageType string) (tsdb.Instance, error) {
//	switch storageType {
//	case consul.ElasticsearchStorageType:
//		return elasticsearch.NewInstance(ctx, &elasticsearch.InstanceOption{
//			Connect: elasticsearch.Connect{
//				Address: "es-test:9200",
//			},
//		})
//	case consul.BkSqlStorageType:
//		return bksql.NewInstance(ctx, &bksql.Options{
//			Address: "doris-test:8030",
//		})
//	default:
//		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
//	}
//}
