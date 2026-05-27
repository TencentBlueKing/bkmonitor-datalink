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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTsDBV2_GetStorageRoutes(t *testing.T) {
	start := time.Unix(1500, 0)
	end := time.Unix(2500, 0)

	t.Run("storage type fallback to tsdb", func(t *testing.T) {
		db := &TsDBV2{
			StorageID:   "1",
			StorageType: "elasticsearch",
		}

		assert.Equal(t, []Record{
			{
				StorageID:   "1",
				StorageType: "elasticsearch",
			},
		}, db.GetStorageRoutes(start, end))
	})

	t.Run("record storage type overrides tsdb storage type", func(t *testing.T) {
		db := &TsDBV2{
			StorageID:   "1",
			StorageType: "elasticsearch",
			StorageClusterRecords: []Record{
				{
					StorageID:   "3",
					StorageType: "bk_sql",
					EnableTime:  2000,
				},
				{
					StorageID:  "2",
					EnableTime: 1000,
				},
			},
		}

		assert.Equal(t, []Record{
			{
				StorageID:   "3",
				StorageType: "bk_sql",
				EnableTime:  2000,
			},
			{
				StorageID:   "2",
				StorageType: "elasticsearch",
				EnableTime:  1000,
			},
		}, db.GetStorageRoutes(start, end))
	})

	t.Run("record route metadata overrides tsdb metadata with fallback", func(t *testing.T) {
		db := &TsDBV2{
			StorageID:   "1",
			StorageType: "elasticsearch",
			StorageName: "es_default",
			ClusterName: "es_default",
			DB:          "es_index",
			Measurement: "__default__",
			StorageClusterRecords: []Record{
				{
					StorageID:   "3",
					StorageType: "bk_sql",
					StorageName: "doris_default",
					ClusterName: "doris_default",
					DB:          "bkbase_table",
					Measurement: "doris",
					EnableTime:  2000,
				},
				{
					StorageID:  "2",
					EnableTime: 1000,
				},
			},
		}

		assert.Equal(t, []Record{
			{
				StorageID:   "3",
				StorageType: "bk_sql",
				StorageName: "doris_default",
				ClusterName: "doris_default",
				DB:          "bkbase_table",
				Measurement: "doris",
				EnableTime:  2000,
			},
			{
				StorageID:   "2",
				StorageType: "elasticsearch",
				StorageName: "es_default",
				ClusterName: "es_default",
				DB:          "es_index",
				Measurement: "__default__",
				EnableTime:  1000,
			},
		}, db.GetStorageRoutes(start, end))
	})
}
