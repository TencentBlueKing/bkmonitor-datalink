// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tenant_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestMain(m *testing.M) {
	mocker.InitTestDBConfig("../../bmw_test.yaml")
	m.Run()
}

func TestGetTenantList(t *testing.T) {
	cfg.EnableMultiTenantMode = false
	tenantList, err := tenant.GetTenantList()
	if err != nil {
		t.Errorf("TestGetTenantList failed, err: %v", err)
	}
	assert.Equal(t, 1, len(tenantList))
	assert.Equal(t, "system", tenantList[0].Id)
}

func TestBkBizIdToTenantId(t *testing.T) {
	db := mysql.GetDBSession().DB

	db.Delete(&space.Space{})

	db.Create(&space.Space{
		SpaceTypeId: "bkcc",
		SpaceId:     "101",
		SpaceName:   "test_space_1",
		BkTenantId:  "test_tenant_id_1",
	})
	db.Create(&space.Space{
		SpaceTypeId: "bkcc",
		SpaceId:     "102",
		SpaceName:   "test_space_2",
		BkTenantId:  "test_tenant_id_2",
	})
	db.Create(&space.Space{
		Id:          1003,
		SpaceTypeId: "bkci",
		SpaceId:     "aaa",
		SpaceName:   "test_space_3",
		BkTenantId:  "test_tenant_id_3",
	})

	cfg.EnableMultiTenantMode = false
	tenantId, err := tenant.GetTenantIdByBkBizId(101)
	assert.NoError(t, err)
	assert.Equal(t, tenant.DefaultTenantId, tenantId)

	cfg.EnableMultiTenantMode = true
	tenantId, err = tenant.GetTenantIdByBkBizId(101)
	assert.NoError(t, err)
	assert.Equal(t, "test_tenant_id_1", tenantId)

	tenantId, err = tenant.GetTenantIdByBkBizId(102)
	assert.NoError(t, err)
	assert.Equal(t, "test_tenant_id_2", tenantId)

	tenantId, err = tenant.GetTenantIdByBkBizId(-1003)
	assert.NoError(t, err)
	assert.Equal(t, "test_tenant_id_3", tenantId)
}
