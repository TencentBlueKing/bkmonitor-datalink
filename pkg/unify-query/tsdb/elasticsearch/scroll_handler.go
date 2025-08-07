// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

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
	session.Mu.Lock()
	defer session.Mu.Unlock()

	var slices []*redis.SliceInfo

	for idx := 0; idx < session.MaxSlice; idx++ {
		key := generateScrollID(consul.ElasticsearchStorageType, connect, tableID, idx)
		val, exists := session.ScrollIDs[key]

		if !exists {
			val = redis.SliceStatusValue{
				Status:    redis.StatusPending,
				FailedNum: 0,
			}
			session.ScrollIDs[key] = val
		}

		if val.FailedNum >= session.SliceMaxFailedNum || val.Status == redis.StatusCompleted {
			continue
		}

		slices = append(slices, &redis.SliceInfo{
			Connect:     connect,
			TableId:     tableID,
			StorageType: consul.ElasticsearchStorageType,
			SliceIdx:    idx,
			SliceMax:    session.MaxSlice,
			ScrollID:    val.ScrollID,
		})
	}

	return slices, nil
}

func (i *Instance) UpdateScrollStatus(ctx context.Context, session *redis.ScrollSession, connect, tableID string, resultOption *metadata.ResultTableOption, status string) error {
	key := generateScrollID(consul.ElasticsearchStorageType, connect, tableID, *resultOption.SliceIndex)
	return session.UpdateSliceStatus(ctx, key, status, resultOption.ScrollID, 0)
}
