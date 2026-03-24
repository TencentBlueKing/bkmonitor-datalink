// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tenant

import (
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestMain(m *testing.M) {
	mocker.InitTestDBConfig("../../bmw_test.yaml")
	m.Run()
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
	tenantId, err := GetTenantIdByBkBizId(101)
	assert.NoError(t, err)
	assert.Equal(t, DefaultTenantId, tenantId)

	cfg.EnableMultiTenantMode = true
	tenantId, err = GetTenantIdByBkBizId(101)
	assert.NoError(t, err)
	assert.Equal(t, "test_tenant_id_1", tenantId)

	tenantId, err = GetTenantIdByBkBizId(102)
	assert.NoError(t, err)
	assert.Equal(t, "test_tenant_id_2", tenantId)

	tenantId, err = GetTenantIdByBkBizId(-1003)
	assert.NoError(t, err)
	assert.Equal(t, "test_tenant_id_3", tenantId)
}

func TestGetTenantAdminUser(t *testing.T) {
	cfg.EnableMultiTenantMode = false
	adminUser, err := GetTenantAdminUser("system")
	assert.NoError(t, err)
	assert.Equal(t, "admin", adminUser)

	// mock sendRequestToUserApi
	patch := gomonkey.ApplyFunc(sendRequestToUserApi, func(tenantId string, method string, path string, urlParams map[string]string, response any) error {
		if resp, ok := response.(*BatchLookupVirtualUserResp); ok {
			*resp = BatchLookupVirtualUserResp{
				Data: []BatchLookupVirtualUserData{
					{
						BkUsername:  "admin",
						LoginName:   "admin",
						DisplayName: "admin",
					},
				},
			}
		}
		return nil
	})
	defer patch.Reset()

	cfg.EnableMultiTenantMode = true
	// 使用一个未缓存的 tenantId 来测试，避免缓存影响
	adminUser, err = GetTenantAdminUser("test_tenant")
	assert.NoError(t, err)
	assert.Equal(t, "admin", adminUser)
}

func TestGetTenantList(t *testing.T) {
	cfg.EnableMultiTenantMode = false
	tenantList, err := GetTenantList()
	assert.NoError(t, err)
	assert.Equal(t, 1, len(tenantList))
	assert.Equal(t, DefaultTenantId, tenantList[0].Id)

	// mock sendRequestToUserApi
	patch := gomonkey.ApplyFunc(sendRequestToUserApi, func(tenantId string, method string, path string, urlParams map[string]string, response any) error {
		if resp, ok := response.(*ListTenantResp); ok {
			*resp = ListTenantResp{
				Data: []ListTenantData{
					{
						Id:     "system",
						Name:   "System",
						Status: "normal",
					},
					{
						Id:     "tenant1",
						Name:   "Tenant1",
						Status: "normal",
					},
				},
			}
		}
		return nil
	})
	defer patch.Reset()

	cfg.EnableMultiTenantMode = true
	lastTenantListUpdate = time.Now().Add(-TenantListRefreshInterval * 2)
	tenantList, err = GetTenantList()
	assert.NoError(t, err)
	assert.Equal(t, 2, len(tenantList))
	assert.Equal(t, DefaultTenantId, tenantList[0].Id)
	assert.Equal(t, "tenant1", tenantList[1].Id)
}

// func TestGetTenantListRealApi(t *testing.T) {
// 	cfg.EnableMultiTenantMode = true
// 	tenantList, err := GetTenantList()
// 	fmt.Println(tenantList, err)
// 	assert.NoError(t, err)
// 	assert.Greater(t, len(tenantList), 0)
// }

// func TestGetTenantAdminUserRealApi(t *testing.T) {
// 	cfg.EnableMultiTenantMode = true
// 	adminUser, err := GetTenantAdminUser("putong")
// 	fmt.Println(adminUser, err)
// 	assert.NoError(t, err)
// 	assert.NotEqual(t, "admin", adminUser)
// }
