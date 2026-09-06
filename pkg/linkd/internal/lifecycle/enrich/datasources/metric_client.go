// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"linkd/internal/lifecycle/enrich/models"

	"gorm.io/gorm"

	"linkd/internal/lifecycle/enrich"
)

var _ enrich.MetricReader = (*MetricClient)(nil)

// MonitorMetric 表已经废弃；MetricClient 只读取 MonitorMetricLibrary。
const metricLibraryTable = "home_application_monitormetriclibrary"

type metricMetadataRow struct {
	FieldCNName   string          `gorm:"column:field_cn_name"`
	Description   string          `gorm:"column:description"`
	Unit          string          `gorm:"column:unit"`
	DimensionList json.RawMessage `gorm:"column:dimension_list"`
}

// MetricClientConfig 注入已经选择 Kingeye schema 的 GORM 连接。
type MetricClientConfig struct {
	DB *gorm.DB
}

// MetricClient 只读查询 KAC clean_item 使用的指标元数据。
type MetricClient struct {
	db *gorm.DB
}

// NewMetricClient 创建指标元数据 Client；数据库连接所有权保留在 Runtime。
func NewMetricClient(config MetricClientConfig) (*MetricClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create metric client: db must not be nil")
	}
	return &MetricClient{db: config.DB}, nil
}

// FindMetricLibrary 按租户、结果表和字段读取指标库展示名称。
// 指定模型时优先返回该模型记录，再沿用 KAC first() 语义回退同指标的第一条记录。
func (c *MetricClient) FindMetricLibrary(
	ctx context.Context,
	query models.MetricLibraryQuery,
) (models.MetricMetadata, bool, error) {
	if ctx == nil {
		return models.MetricMetadata{}, false, fmt.Errorf("find metric library: context must not be nil")
	}
	if query.TenantID == "" || query.FieldName == "" {
		return models.MetricMetadata{}, false, fmt.Errorf("find metric library: tenant ID and field name are required")
	}
	base := c.db.WithContext(ctx).
		Table(metricLibraryTable).
		Select("field_cn_name", "description", "unit", "dimension_list").
		Where("bk_tenant_id = ? AND field_name = ? AND is_deleted = ?", query.TenantID, query.FieldName, false)
	if query.TableID != "" {
		base = base.Where("table_id = ?", query.TableID)
	}
	if query.FieldTag != "" {
		base = base.Where("tag = ?", query.FieldTag)
	}
	if query.ObjectModelCode != "" {
		value, found, err := takeMetricMetadata(base.Where("object_model_code = ?", query.ObjectModelCode), "query model metric library")
		if err != nil || found {
			return value, found, err
		}
	}
	return takeMetricMetadata(base, "query metric library")
}

func takeMetricMetadata(query *gorm.DB, operation string) (models.MetricMetadata, bool, error) {
	var row metricMetadataRow
	err := query.Order("id").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.MetricMetadata{}, false, nil
	}
	if err != nil {
		return models.MetricMetadata{}, false, fmt.Errorf("%s: %w", operation, err)
	}
	dimensions := make([]models.MetricDimension, 0)
	if len(row.DimensionList) != 0 {
		if err := json.Unmarshal(row.DimensionList, &dimensions); err != nil {
			return models.MetricMetadata{}, false, fmt.Errorf("%s dimension_list: %w", operation, err)
		}
	}
	return models.MetricMetadata{
		FieldCNName: row.FieldCNName,
		Description: row.Description,
		Unit:        row.Unit,
		Dimensions:  dimensions,
	}, true, nil
}
