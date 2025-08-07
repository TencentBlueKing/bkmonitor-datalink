// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"context"
	"strings"

	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

func generateScrollID(items ...any) string {
	var strs []string
	for _, item := range items {
		if item != nil {
			strs = append(strs, cast.ToString(item))
		}
	}
	return strings.Join(strs, ":")
}

func (i *Instance) MakeSlices(ctx context.Context, session *redis.ScrollSession, connect, tableID string) ([]*redis.SliceInfo, error) {
	var slices []*redis.SliceInfo

	for idx := 0; idx < session.MaxSlice; idx++ {
		key := generateScrollID(consul.BkSqlStorageType, tableID, idx)
		session.Mu.RLock()
		val, exists := session.ScrollIDs[key]
		session.Mu.RUnlock()

		if !exists {
			session.Mu.Lock()
			session.ScrollIDs[key] = redis.SliceStatusValue{
				Status:    redis.StatusPending,
				FailedNum: 0,
				Offset:    idx * session.Limit,
				Limit:     session.Limit,
			}
			session.Mu.Unlock()
		}
		if val.FailedNum >= session.SliceMaxFailedNum || val.Status == redis.StatusCompleted {
			continue
		}

		slices = append(slices, &redis.SliceInfo{
			Connect:     "",
			TableId:     tableID,
			StorageType: consul.BkSqlStorageType,
			SliceIdx:    idx,
			SliceMax:    session.MaxSlice,
			Offset:      val.Offset,
		})
	}

	return slices, nil
}

func (i *Instance) UpdateScrollStatus(ctx context.Context, session *redis.ScrollSession, connect, tableID string, resultOption *metadata.ResultTableOption, status string) error {
	key := generateScrollID(consul.BkSqlStorageType, tableID, *resultOption.SliceIndex)

	var scrollID string
	var currentOffset int
	if val, exists := session.ScrollIDs[key]; exists {
		scrollID = val.ScrollID
		currentOffset = val.Offset
	}

	newOffset := currentOffset + session.MaxSlice*session.Limit
	return session.UpdateSliceStatus(ctx, key, status, scrollID, newOffset)
}
