// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"slices"
	"strings"

	"github.com/jinzhu/gorm"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
)

// APM application ownership is derived on every refresh, never persisted in ResultTable.labels.
// Consumers that do not understand this optional label continue using the existing route.
const apmApplicationNameLabel = "apm_application_name"

type apmMetricApplicationRow struct {
	BkDataID                 uint   `gorm:"column:bk_data_id"`
	BkBizID                  int    `gorm:"column:bk_biz_id"`
	AppName                  string `gorm:"column:app_name"`
	ResultTableID            string `gorm:"column:result_table_id"`
	SharedDataSourceID       *int64 `gorm:"column:shared_datasource_id"`
	ApplicationMatchesTenant bool   `gorm:"column:application_matches_tenant"`
	DataSourceMatchesTenant  bool   `gorm:"column:datasource_matches_tenant"`
}

// loadAPMApplicationNames follows DataSourceResultTable so separate metric tables sharing
// the application's DataID receive the same ownership. APM MetricDataSource has no tenant
// column: keep every candidate and validate its application and metadata DataSource tenant,
// rather than filtering invalid candidates away and accepting the remaining one.
func loadAPMApplicationNames(
	db *gorm.DB, bkTenantID string, resultTables map[string]resulttable.ResultTable,
	relations []resulttable.DataSourceResultTable,
) (map[string]string, error) {
	dataIDSet := make(map[uint]struct{}, len(relations))
	for _, relation := range relations {
		if relation.BkDataId > 0 {
			dataIDSet[relation.BkDataId] = struct{}{}
		}
	}
	dataIDs := make([]uint, 0, len(dataIDSet))
	for dataID := range dataIDSet {
		dataIDs = append(dataIDs, dataID)
	}
	slices.Sort(dataIDs)

	var candidates []apmMetricApplicationRow
	for _, batch := range slicex.ChunkSlice(dataIDs, resultTableDetailBatchSize) {
		var rows []apmMetricApplicationRow
		err := db.Table("apm_metricdatasource AS metric").Select(`
			metric.bk_data_id, metric.bk_biz_id, metric.app_name,
			metric.result_table_id, metric.shared_datasource_id,
			EXISTS (SELECT 1 FROM apm_apmapplication AS application
				WHERE application.bk_biz_id = metric.bk_biz_id
				AND application.app_name = metric.app_name
				AND application.bk_tenant_id = ?) AS application_matches_tenant,
			EXISTS (SELECT 1 FROM metadata_datasource AS datasource
				WHERE datasource.bk_data_id = metric.bk_data_id
				AND datasource.bk_tenant_id = ?) AS datasource_matches_tenant`, bkTenantID, bkTenantID).
			Where("metric.bk_data_id IN (?)", batch).Find(&rows).Error
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, rows...)
	}
	return resolveAPMApplicationNames(resultTables, relations, candidates), nil
}

func resolveAPMApplicationNames(
	resultTables map[string]resulttable.ResultTable,
	relations []resulttable.DataSourceResultTable,
	candidates []apmMetricApplicationRow,
) map[string]string {
	type owner struct {
		appName string
		valid   bool
	}
	dataOwners := make(map[uint]owner)
	for _, candidate := range candidates {
		valid := candidate.ApplicationMatchesTenant && candidate.DataSourceMatchesTenant &&
			strings.TrimSpace(candidate.AppName) != "" && strings.TrimSpace(candidate.ResultTableID) != "" &&
			candidate.SharedDataSourceID == nil
		previous, exists := dataOwners[candidate.BkDataID]
		if exists {
			valid = valid && previous.valid && previous.appName == candidate.AppName
		}
		dataOwners[candidate.BkDataID] = owner{appName: candidate.AppName, valid: valid}
	}

	tableOwners := make(map[string]owner)
	for _, relation := range relations {
		_, exists := resultTables[relation.TableId]
		if !exists {
			continue
		}
		current := dataOwners[relation.BkDataId]
		if previous, exists := tableOwners[relation.TableId]; exists {
			current.valid = current.valid && previous.valid && previous.appName == current.appName
		}
		tableOwners[relation.TableId] = current
	}

	names := make(map[string]string)
	for tableID, owner := range tableOwners {
		if owner.valid {
			names[tableID] = owner.appName
		}
	}
	return names
}

func metricRouteLabels(tableID string, rt resulttable.ResultTable, applicationName string) map[string]any {
	labels := normalizeResultTableLabels(tableID, rt.Labels)
	delete(labels, apmApplicationNameLabel)
	if applicationName != "" {
		labels[apmApplicationNameLabel] = applicationName
	}
	return labels
}
