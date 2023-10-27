// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/elasticsearch"
)

func TestClusterInfo_GetESClient(t *testing.T) {
	cluster := ClusterInfo{
		ClusterType: models.StorageTypeInfluxdb,
		Version:     "7.10.1",
		Schema:      "https",
		DomainName:  "example.com",
		Port:        9200,
		Username:    "elastic",
		Password:    "123456",
	}

	// 测试错误后端类型
	client, err := cluster.GetESClient(context.TODO())
	assert.EqualError(t, err, "record type error")
	assert.Nil(t, client)
	// 测试连接超时
	cluster.ClusterType = models.StorageTypeES
	client, err = cluster.GetESClient(context.TODO())
	assert.Error(t, err, context.Canceled)
	assert.Nil(t, client)
	// 测试获取客户端
	patchESPing := gomonkey.ApplyFuncReturn(elasticsearch.Elasticsearch.Ping, nil, nil)
	defer patchESPing.Reset()
	client, err = cluster.GetESClient(context.TODO())
	assert.Nil(t, err)
	assert.Equal(t, client.Version, "7")
}
