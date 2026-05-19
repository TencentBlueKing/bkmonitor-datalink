// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package core

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store"
)

type metadataTestStore struct {
	store.Store
	value []byte
}

func (s metadataTestStore) Get(_ string) (uint64, []byte, error) {
	return 0, s.value, nil
}

func TestMetadataCenterAddDataIdExclusive(t *testing.T) {
	center := &MetadataCenter{Mapping: &sync.Map{}, Consul: metadataTestStore{value: []byte(`{
		"token": "token-a",
		"bk_biz_id": 2,
		"bk_tenant_id": "tenant-a",
		"bk_biz_name": "BlueKing",
		"app_id": 10,
		"app_name": "app-a",
		"apps": [
			{"token": "token-b", "bk_biz_id": 3, "bk_tenant_id": "tenant-b", "bk_biz_name": -3, "app_id": 20, "app_name": "app-b"}
		],
		"kafka_info": {"topic": "topic-a", "host": "kafka"},
		"trace_es_info": {"index_name": "trace-a"},
		"save_es_info": {"index_name": "save-a"}
	}`)}}

	assert.NoError(t, center.AddDataId("1001"))
	appKey := AppKey{BkBizId: "2", AppName: "app-a"}
	baseInfos := center.ListBaseInfos("1001")
	assert.Len(t, baseInfos, 1)
	assert.ElementsMatch(t, []AppKey{appKey}, appKeys(baseInfos))
	assert.False(t, center.IsShared("1001"))
	assert.Equal(t, "token-a", baseInfos[0].Token)
}

func TestMetadataCenterAddDataIdShared(t *testing.T) {
	center := &MetadataCenter{Mapping: &sync.Map{}, Consul: metadataTestStore{value: []byte(`{
		"is_shared": true,
		"kafka_info": {"topic": "topic-a", "host": "kafka"},
		"trace_es_info": {"index_name": "trace-a"},
		"save_es_info": {"index_name": "save-a"},
		"apps": [
			{"token": "token-b", "bk_biz_id": 3, "bk_tenant_id": "tenant-b", "bk_biz_name": -3, "app_id": 20, "app_name": "app-b"},
			{"token": "token-a", "bk_biz_id": 2, "bk_tenant_id": "tenant-a", "bk_biz_name": "BlueKing", "app_id": 10, "app_name": "app-a"}
		]
	}`)}}

	assert.NoError(t, center.AddDataId("1001"))
	appA := AppKey{BkBizId: "2", AppName: "app-a"}
	appB := AppKey{BkBizId: "3", AppName: "app-b"}
	baseInfos := center.ListBaseInfos("1001")
	assert.Len(t, baseInfos, 2)
	assert.ElementsMatch(t, []AppKey{appA, appB}, appKeys(baseInfos))
	assert.True(t, center.IsShared("1001"))
	assert.ElementsMatch(t, []string{"token-a", "token-b"}, baseInfoTokens(baseInfos))
	assert.Equal(t, "trace-a", center.GetTraceEsConfig("1001").IndexName)
}

func TestMetadataCenterAddDataIdSharedWithoutApps(t *testing.T) {
	center := &MetadataCenter{Mapping: &sync.Map{}, Consul: metadataTestStore{value: []byte(`{
		"is_shared": true,
		"kafka_info": {"topic": "topic-a", "host": "kafka"},
		"trace_es_info": {"index_name": "trace-a"},
		"save_es_info": {"index_name": "save-a"}
	}`)}}

	assert.NoError(t, center.AddDataId("1001"))
	assert.True(t, center.IsShared("1001"))
	assert.Empty(t, center.ListBaseInfos("1001"))
	assert.Equal(t, "trace-a", center.GetTraceEsConfig("1001").IndexName)
}

func (s metadataTestStore) Put(string, string, uint64, time.Duration) error {
	return nil
}

func appKeys(baseInfos []BaseInfo) []AppKey {
	res := make([]AppKey, 0, len(baseInfos))
	for _, baseInfo := range baseInfos {
		res = append(res, baseInfo.AppKey())
	}
	return res
}

func baseInfoTokens(baseInfos []BaseInfo) []string {
	res := make([]string, 0, len(baseInfos))
	for _, baseInfo := range baseInfos {
		res = append(res, baseInfo.Token)
	}
	return res
}
