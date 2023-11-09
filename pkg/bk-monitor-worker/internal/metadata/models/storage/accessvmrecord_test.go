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
)

func TestAccessVMRecord_RefreshVmRouter(t *testing.T) {
	pushRecord := make(map[string]string)
	gomonkey.ApplyFunc(models.PushToRedis, func(ctx context.Context, key, field, value string, isPublish bool) {
		pushRecord[key+"-"+field] = value
	})
	vm := AccessVMRecord{
		ResultTableId:    "table_id_a.base",
		VmResultTableId:  "vm_test",
		StorageClusterID: 1,
	}
	err := vm.RefreshVmRouter(context.Background())
	assert.NoError(t, err)
	record, ok := pushRecord[models.QueryVmStorageRouterKey+"-"+vm.ResultTableId]
	assert.True(t, ok)
	assert.Equal(t, record, `{"clusterName":"","db":"table_id_a","measurement":"base","retention_policies":{"autogen":{"is_default":true,"resolution":0}},"storageID":"1","table_id":"table_id_a.base","tagsKey":[],"vm_rt":"vm_test"}`)
}
