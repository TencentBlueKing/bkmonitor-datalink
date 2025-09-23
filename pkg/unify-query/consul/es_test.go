// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul_test

import (
	"context"
	"testing"

	"github.com/hashicorp/consul/api"
	"github.com/likexian/gokit/assert"
	"github.com/prashantv/gostub"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestGetESData
func TestGetESData(t *testing.T) {
	log.InitTestLogger()
	_ = consul.SetInstance(
		context.Background(), "", "test-unify", "http://127.0.0.1:8500", []string{}, "127.0.0.1", 10205, "30s", nil,
	)
	kv1 := api.KVPairs{
		{
			Key:   "bkmonitorv3/unify-query/data/es/info/testbb.ttt",
			Value: []byte(`{"storage_id":2,"alias_format":"{index}_{time}_read","date_format":"20060102","date_step":2}`),
		},
	}
	stubs := gostub.StubFunc(&consul.GetDataWithPrefix, kv1, nil)
	defer stubs.Reset()
	tableInfos, err := consul.GetESTableInfo()
	assert.Nil(t, err)
	assert.Equal(t, 2, tableInfos["testbb.ttt"].StorageID)

	kv2 := api.KVPairs{
		{
			Key:   "bkmonitorv3/unify-query/data/storage/2",
			Value: []byte(`{"address":"http://127.0.0.1:9200","username":"","password":"","type":"elasticsearch"}`),
		},
	}
	stubs.Reset()
	stubs.StubFunc(&consul.GetDataWithPrefix, kv2, nil)
	storageInfos, err := consul.GetESStorageInfo()
	assert.Nil(t, err)
	assert.Equal(t, "http://127.0.0.1:9200", storageInfos["2"].Address)
}
