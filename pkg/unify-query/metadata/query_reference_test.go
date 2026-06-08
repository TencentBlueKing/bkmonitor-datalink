// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestQueryStorageUUIDKeepsSameStorageIDRollbackWindowsIndependent(t *testing.T) {
	base := Query{
		StorageType:     BkSqlStorageType,
		StorageID:       "s1",
		StorageName:     "cluster",
		MeasurementType: "doris",
	}

	firstS1Window := base
	firstS1Window.RouteStart = time.Unix(100, 0)
	firstS1Window.RouteEnd = time.Unix(200, 0)
	firstS1Window.RouteQueryStart = time.Unix(40, 0)
	firstS1Window.RouteQueryEnd = time.Unix(260, 0)

	secondS1Window := base
	secondS1Window.RouteStart = time.Unix(300, 0)
	secondS1Window.RouteEnd = time.Unix(400, 0)
	secondS1Window.RouteQueryStart = time.Unix(240, 0)
	secondS1Window.RouteQueryEnd = time.Unix(460, 0)

	// 场景：storage 路由发生 A -> B -> A 回切。
	//
	// 时间轴：
	// storage s1: [100s-------------200s)
	// storage s2:                  [200s-------------300s)
	// storage s1:                                    [300s-------------400s)
	//
	// 同一个 storage_id=s1 在两个不连续时间段内真实生效，这是合法路由状态，不能简单按 storage_id 去重。
	// StorageUUID 必须携带 RouteStart/RouteEnd/RouteQueryStart/RouteQueryEnd，
	// 否则 is_merge_db 会把两个 s1 route window 折叠成同一路查询，导致其中一个生效窗口被覆盖或丢失。
	assert.NotEqual(t, firstS1Window.StorageUUID(), secondS1Window.StorageUUID())
}

func TestQueryStorageUUIDSkipsZeroRouteRange(t *testing.T) {
	q := Query{
		StorageType:     BkSqlStorageType,
		StorageID:       "s1",
		StorageName:     "cluster",
		MeasurementType: "doris",
	}

	assert.NotContains(t, q.StorageUUID(), fmt.Sprintf("%d", time.Time{}.UnixNano()))
}
