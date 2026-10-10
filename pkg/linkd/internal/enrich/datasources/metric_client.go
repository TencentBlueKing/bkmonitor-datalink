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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

var _ enrich.MetricReader = (*MetricClient)(nil)

const maxMetricJSONBytes = 1 << 20

type metricMetadataRow struct {
	ID              int64
	TenantID        string `gorm:"column:bk_tenant_id"`
	SpaceUID        string
	ObjectModelCode string `gorm:"column:model_id"`
	FieldName       string `gorm:"column:metric_name"`
	FieldCNName     string `gorm:"column:display_name"`
	Description     string
	Unit            string
	Kind            string
	TableID         string `gorm:"column:result_table_id"`
	DataLabel       string
	PhysicalField   string
	ValueMapping    json.RawMessage
	Dimensions      json.RawMessage
}

// MetricClientConfig 注入已经选择 Kingeye schema 的 GORM 连接。
type MetricClientConfig struct{ DB *gorm.DB }

// MetricClient 只读查询 metricset 指标目录的 metric 元数据行。
// metricset 仅登记来源，展示元数据与主引用由 metric 行持有。
type MetricClient struct{ db *gorm.DB }

// NewMetricClient 创建指标元数据 Client；数据库连接所有权保留在 Runtime。
func NewMetricClient(config MetricClientConfig) (*MetricClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create metric client: db must not be nil")
	}
	return &MetricClient{db: config.DB}, nil
}

// FindMetric 优先按租户与 metric_id 点查；有 ID 时不回退其他指标。
// 无 ID 的发布材料按原生主引用反查，衍生指标按 derived 定义名反查。
// 指定 SpaceUID 时同时可见平台层 *，未指定时在租户内要求唯一。多义结果失败。
// enabled/ref_status 不参与过滤：这里只丰富已触发告警，不判断当前是否允许取数。
func (c *MetricClient) FindMetric(ctx context.Context, query models.MetricQuery) (models.MetricMetadata, bool, error) {
	if ctx == nil {
		return models.MetricMetadata{}, false, fmt.Errorf("find metric: context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return models.MetricMetadata{}, false, err
	}
	if query.TenantID == "" || query.MetricID < 0 || (query.MetricID == 0 && (query.FieldName == "" || (query.TableID == "" && query.FieldTag != models.CWStrategyFieldTagDerivedMetric))) {
		return models.MetricMetadata{}, false, fmt.Errorf("find metric: tenant and metric ID or complete locator are required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// JSON 在 SQL 端限长，避免异常目录行使单次丰富分配无界内存。
	base := c.db.WithContext(ctx).Table("metric").Select("id,bk_tenant_id,space_uid,model_id,metric_name,display_name,description,unit,kind,result_table_id,data_label,physical_field,CASE WHEN OCTET_LENGTH(value_mapping) <= ? THEN value_mapping ELSE NULL END AS value_mapping,CASE WHEN OCTET_LENGTH(dimensions) <= ? THEN dimensions ELSE NULL END AS dimensions", maxMetricJSONBytes, maxMetricJSONBytes).Where("bk_tenant_id = ?", query.TenantID)
	if query.MetricID > 0 {
		base = base.Where("id = ?", query.MetricID)
	} else {
		if query.SpaceUID != "" {
			base = base.Where("space_uid IN ?", []string{query.SpaceUID, "*"})
		}
		if query.FieldTag == models.CWStrategyFieldTagDerivedMetric {
			base = base.Where("kind = ? AND metric_name = ?", "derived", query.FieldName)
		} else {
			base = base.Where("kind = ? AND physical_field = ? AND (result_table_id = ? OR data_label = ?)", "native", query.FieldName, query.TableID, query.TableID)
		}
		// 明确指定对象时必须精确命中；不继承旧指标库跨模型 first() 回退。
		if query.ObjectModelCode != "" {
			base = base.Where("model_id = ?", query.ObjectModelCode)
		}
	}
	var rows []metricMetadataRow
	if err := base.Order("id").Limit(2).Find(&rows).Error; err != nil {
		return models.MetricMetadata{}, false, fmt.Errorf("query metric catalog: %w", err)
	}
	if len(rows) == 0 {
		return models.MetricMetadata{}, false, nil
	}
	if len(rows) != 1 {
		return models.MetricMetadata{}, false, fmt.Errorf("%w: metric locator is ambiguous", enrich.ErrInvalidDataSourceResponse)
	}
	row := rows[0]
	// 身份精确复核不依赖数据库排序规则；ID 点查不得串租户或被物理位置改写。
	if row.TenantID != query.TenantID || (query.MetricID > 0 && row.ID != query.MetricID) {
		return models.MetricMetadata{}, false, fmt.Errorf("%w: metric identity mismatch", enrich.ErrInvalidDataSourceResponse)
	}
	if query.MetricID == 0 {
		valid := query.SpaceUID == "" || row.SpaceUID == query.SpaceUID || row.SpaceUID == "*"
		valid = valid && (query.ObjectModelCode == "" || row.ObjectModelCode == query.ObjectModelCode)
		if query.FieldTag == models.CWStrategyFieldTagDerivedMetric {
			valid = valid && row.Kind == "derived" && row.FieldName == query.FieldName
		} else {
			valid = valid && row.Kind == "native" && row.PhysicalField == query.FieldName && (row.TableID == query.TableID || row.DataLabel == query.TableID)
		}
		if !valid {
			return models.MetricMetadata{}, false, fmt.Errorf("%w: metric locator mismatch", enrich.ErrInvalidDataSourceResponse)
		}
	}
	if row.FieldName == "" {
		return models.MetricMetadata{}, false, fmt.Errorf("%w: metric_name is empty", enrich.ErrInvalidDataSourceResponse)
	}
	var mapping []models.MetricValueMapping
	var dimensions []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	// NULL 表示 schema/超限异常；新目录这两列的合法空值都是 []。
	if len(row.ValueMapping) == 0 || len(row.Dimensions) == 0 || bytes.Equal(bytes.TrimSpace(row.ValueMapping), []byte("null")) || bytes.Equal(bytes.TrimSpace(row.Dimensions), []byte("null")) {
		return models.MetricMetadata{}, false, fmt.Errorf("%w: metric JSON is null or oversized", enrich.ErrInvalidDataSourceResponse)
	}
	if err := json.Unmarshal(row.ValueMapping, &mapping); err != nil {
		return models.MetricMetadata{}, false, fmt.Errorf("%w: value_mapping: %w", enrich.ErrInvalidDataSourceResponse, err)
	}
	if err := json.Unmarshal(row.Dimensions, &dimensions); err != nil {
		return models.MetricMetadata{}, false, fmt.Errorf("%w: dimensions: %w", enrich.ErrInvalidDataSourceResponse, err)
	}
	outputDimensions := make([]models.MetricDimension, 0, len(dimensions))
	for _, dimension := range dimensions {
		if dimension.ID == "" {
			return models.MetricMetadata{}, false, fmt.Errorf("%w: dimension id is empty", enrich.ErrInvalidDataSourceResponse)
		}
		outputDimensions = append(outputDimensions, models.MetricDimension{Key: dimension.ID, Name: dimension.Name})
	}
	return models.MetricMetadata{ObjectModelCode: row.ObjectModelCode, FieldName: row.FieldName, FieldCNName: row.FieldCNName, Description: row.Description, Unit: row.Unit, ValueMapping: mapping, Dimensions: outputDimensions}, true, nil
}
