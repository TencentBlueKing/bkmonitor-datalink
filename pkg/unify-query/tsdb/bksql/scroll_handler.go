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
	needUpdate := false
	var slices []*redis.SliceInfo

	for i := 0; i < session.MaxSlice; i++ {
		key := generateScrollID(consul.BkSqlStorageType, tableID, i)
		val, exists := session.ScrollIDs[key]

		if !exists {
			val = redis.SliceStatusValue{
				Status:    redis.StatusPending,
				FailedNum: 0,
				Offset:    i * 10,
				Limit:     10,
			}
			session.ScrollIDs[key] = val
			needUpdate = true
		}

		if val.Status == redis.StatusFailed {
			if val.FailedNum < session.SliceMaxFailedNum {
				val.Status = redis.StatusPending
				session.ScrollIDs[key] = val
				needUpdate = true
			} else {
				val.Status = redis.StatusStop
				session.ScrollIDs[key] = val
				needUpdate = true
				continue
			}
		}

		// 如果状态是运行中，自动推进offset
		if val.Status == redis.StatusRunning {
			val.Offset += session.MaxSlice * val.Limit
			session.ScrollIDs[key] = val
			needUpdate = true
		}

		if val.Status == redis.StatusStop || val.Status == redis.StatusCompleted {
			continue
		}

		slices = append(slices, &redis.SliceInfo{
			Connect:     "",
			TableId:     tableID,
			StorageType: consul.BkSqlStorageType,
			SliceIdx:    i,
			SliceMax:    session.MaxSlice,
			Offset:      val.Offset,
		})
	}

	if needUpdate {
		err := redis.Client().Set(ctx, session.SessionKey, session, session.ScrollTimeout).Err()
		if err != nil {
			return nil, err
		}
	}

	return slices, nil
}

func (i *Instance) UpdateScrollStatus(ctx context.Context, session *redis.ScrollSession, connect, tableID string, resultOption *metadata.ResultTableOption, status string) error {
	key := generateScrollID(consul.BkSqlStorageType, tableID, resultOption.SliceIndex)
	sliceStatusValue, ok := session.ScrollIDs[key]
	if !ok {
		return redis.ErrorOfScrollSliceStatusNotFound
	}

	sliceStatusValue.Status = status
	if status == redis.StatusFailed {
		sliceStatusValue.FailedNum++
	}

	return session.UpdateScrollSliceStatusValue(ctx, key, sliceStatusValue)
}
