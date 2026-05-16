// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/recordrule"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestPreFetchVMShortLinkTableIdValues(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	assert.NoError(t, db.AutoMigrate(&space.VMShortLinkRecord{}).Error)

	tableIds := []string{
		"prefetch_vm_short_link_rt",
		"prefetch_global_bkci_rt",
		"prefetch_global_all_rt",
		"prefetch_partial_config_rt",
		"prefetch_disabled_rt",
		"prefetch_deleted_rt",
	}
	assert.NoError(t, db.Delete(&space.VMShortLinkRecord{}, "table_id in (?)", tableIds).Error)

	records := []space.VMShortLinkRecord{
		{
			BkTenantId: "system",
			SpaceType:  "bkcc",
			SpaceId:    "1001",
			TableId:    "prefetch_vm_short_link_rt",
			IsEnabled:  true,
		},
		{
			BkTenantId: "system",
			SpaceType:  "bkci",
			SpaceId:    "project_a",
			TableId:    "prefetch_global_bkci_rt",
			IsGlobal:   true,
			IsEnabled:  true,
		},
		{
			BkTenantId:        "system",
			SpaceType:         "bkci",
			SpaceId:           "project_a",
			TableId:           "prefetch_global_all_rt",
			IsGlobal:          true,
			IsEnabled:         true,
			QueryRouterConfig: `{"space_type":"all","filter_key":"project_id","filter_value":"space_id"}`,
		},
		{
			BkTenantId:        "system",
			SpaceType:         "bkci",
			SpaceId:           "project_a",
			TableId:           "prefetch_partial_config_rt",
			IsGlobal:          true,
			IsEnabled:         true,
			QueryRouterConfig: `{"filter_key":"custom_biz"}`,
		},
		{
			BkTenantId: "system",
			SpaceType:  "bkci",
			SpaceId:    "project_a",
			TableId:    "prefetch_disabled_rt",
			IsGlobal:   true,
			IsEnabled:  false,
		},
		{
			BkTenantId: "system",
			SpaceType:  "bkci",
			SpaceId:    "project_a",
			TableId:    "prefetch_deleted_rt",
			IsGlobal:   true,
			IsEnabled:  true,
			IsDeleted:  true,
		},
	}
	for _, record := range records {
		assert.NoError(t, db.Create(&record).Error)
	}

	spaceList := []space.Space{
		{Id: 100, BkTenantId: "system", SpaceTypeId: "bkcc", SpaceId: "1001"},
		{Id: 101, BkTenantId: "system", SpaceTypeId: "bkci", SpaceId: "project_a"},
		{Id: 102, BkTenantId: "system", SpaceTypeId: "bkci", SpaceId: "project_b"},
		{Id: 201, BkTenantId: "system", SpaceTypeId: "bksaas", SpaceId: "app_a"},
		{Id: 103, BkTenantId: "other", SpaceTypeId: "bkci", SpaceId: "project_c"},
	}

	data, err := preFetchVMShortLinkTableIdValues(service.NewSpacePusher(), spaceList)

	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"filters": []map[string]any{}}, data[service.SpaceRouteKeyWithTenant("system", "bkcc", "1001")]["prefetch_vm_short_link_rt.__default__"])
	assert.Equal(t, map[string]any{"filters": []map[string]any{{"project_id": "1001"}}}, data[service.SpaceRouteKeyWithTenant("system", "bkcc", "1001")]["prefetch_global_all_rt.__default__"])

	assert.Equal(t, map[string]any{"filters": []map[string]any{}}, data[service.SpaceRouteKeyWithTenant("system", "bkci", "project_a")]["prefetch_global_bkci_rt.__default__"])
	assert.Equal(t, map[string]any{"filters": []map[string]any{}}, data[service.SpaceRouteKeyWithTenant("system", "bkci", "project_a")]["prefetch_global_all_rt.__default__"])
	assert.Equal(t, map[string]any{"filters": []map[string]any{}}, data[service.SpaceRouteKeyWithTenant("system", "bkci", "project_a")]["prefetch_partial_config_rt.__default__"])

	assert.Equal(t, map[string]any{"filters": []map[string]any{{"bk_biz_id": "-102"}}}, data[service.SpaceRouteKeyWithTenant("system", "bkci", "project_b")]["prefetch_global_bkci_rt.__default__"])
	assert.Equal(t, map[string]any{"filters": []map[string]any{{"project_id": "project_b"}}}, data[service.SpaceRouteKeyWithTenant("system", "bkci", "project_b")]["prefetch_global_all_rt.__default__"])
	assert.Equal(t, map[string]any{"filters": []map[string]any{{"custom_biz": "-102"}}}, data[service.SpaceRouteKeyWithTenant("system", "bkci", "project_b")]["prefetch_partial_config_rt.__default__"])
	assert.Equal(t, map[string]any{"filters": []map[string]any{{"project_id": "app_a"}}}, data[service.SpaceRouteKeyWithTenant("system", "bksaas", "app_a")]["prefetch_global_all_rt.__default__"])

	assert.NotContains(t, data[service.SpaceRouteKeyWithTenant("other", "bkci", "project_c")], "prefetch_global_bkci_rt.__default__", "short link records should be isolated by tenant")
	assert.NotContains(t, data[service.SpaceRouteKeyWithTenant("other", "bkci", "project_c")], "prefetch_global_all_rt.__default__", "short link records should be isolated by tenant")
	assert.NotContains(t, data[service.SpaceRouteKeyWithTenant("other", "bkci", "project_c")], "prefetch_partial_config_rt.__default__", "short link records should be isolated by tenant")
	for _, values := range data {
		assert.NotContains(t, values, "prefetch_disabled_rt.__default__")
		assert.NotContains(t, values, "prefetch_deleted_rt.__default__")
	}
}

func TestPreFetchRecordRuleV4TableIdValues(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	assert.NoError(t, db.AutoMigrate(&recordrule.RecordRuleV4{}).Error)

	tableIds := []string{"prefetch_record_rule_v4_rt", "prefetch_record_rule_v4_deleting_rt", "prefetch_record_rule_v4_recent_deleted_rt", "prefetch_record_rule_v4_expired_deleted_rt"}
	assert.NoError(t, db.Delete(&recordrule.RecordRuleV4{}, "table_id in (?)", tableIds).Error)

	recentDeletedAt := time.Now().AddDate(0, 0, -179)
	expiredDeletedAt := time.Now().AddDate(0, 0, -181)
	records := []recordrule.RecordRuleV4{
		{
			BkTenantId:    "system",
			SpaceType:     "bkcc",
			SpaceId:       "1001",
			Name:          "record_rule_v4",
			FlowName:      "rrv4_record_rule_v4",
			TableId:       "prefetch_record_rule_v4_rt",
			DstVmTableId:  "vm_prefetch_record_rule_v4_rt",
			DesiredStatus: "running",
			Status:        "running",
			Conditions:    "{}",
		},
		{
			BkTenantId:    "system",
			SpaceType:     "bkcc",
			SpaceId:       "1001",
			Name:          "record_rule_v4_deleting",
			FlowName:      "rrv4_record_rule_v4_deleting",
			TableId:       "prefetch_record_rule_v4_deleting_rt",
			DstVmTableId:  "vm_prefetch_record_rule_v4_deleting_rt",
			DesiredStatus: "deleted",
			Status:        "deleting",
			Conditions:    "{}",
		},
		{
			BkTenantId:    "system",
			SpaceType:     "bkcc",
			SpaceId:       "1001",
			Name:          "record_rule_v4_recent_deleted",
			FlowName:      "rrv4_record_rule_v4_recent_deleted",
			TableId:       "prefetch_record_rule_v4_recent_deleted_rt",
			DstVmTableId:  "vm_prefetch_record_rule_v4_recent_deleted_rt",
			DesiredStatus: "deleted",
			Status:        "deleted",
			Conditions:    "{}",
			DeletedAtTime: &recentDeletedAt,
		},
		{
			BkTenantId:    "system",
			SpaceType:     "bkcc",
			SpaceId:       "1001",
			Name:          "record_rule_v4_expired_deleted",
			FlowName:      "rrv4_record_rule_v4_expired_deleted",
			TableId:       "prefetch_record_rule_v4_expired_deleted_rt",
			DstVmTableId:  "vm_prefetch_record_rule_v4_expired_deleted_rt",
			DesiredStatus: "deleted",
			Status:        "deleted",
			Conditions:    "{}",
			DeletedAtTime: &expiredDeletedAt,
		},
	}
	for _, record := range records {
		assert.NoError(t, db.Create(&record).Error)
	}

	data, err := preFetchRecordRuleV4TableIdValues(service.NewSpacePusher())

	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"filters": []map[string]any{}}, data[service.SpaceRouteKeyWithTenant("system", "bkcc", "1001")]["prefetch_record_rule_v4_rt.__default__"])
	assert.Equal(t, map[string]any{"filters": []map[string]any{}}, data[service.SpaceRouteKeyWithTenant("system", "bkcc", "1001")]["prefetch_record_rule_v4_deleting_rt.__default__"])
	assert.Equal(t, map[string]any{"filters": []map[string]any{}}, data[service.SpaceRouteKeyWithTenant("system", "bkcc", "1001")]["prefetch_record_rule_v4_recent_deleted_rt.__default__"])
	assert.NotContains(t, data[service.SpaceRouteKeyWithTenant("system", "bkcc", "1001")], "prefetch_record_rule_v4_expired_deleted_rt.__default__")
}

func TestPreFetchRecordRuleV4TableIdValuesSkipWhenTableNotExists(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	tableName := recordrule.RecordRuleV4{}.TableName()
	assert.NoError(t, db.DropTableIfExists(tableName).Error)

	data, err := preFetchRecordRuleV4TableIdValues(service.NewSpacePusher())

	assert.NoError(t, err)
	assert.Empty(t, data)
}
