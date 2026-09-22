// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"linkd/internal/enrich"
)

var _ enrich.ModelReader = (*ModelClient)(nil)

const objectModelTable = "object_model_v2"

type objectModelRow struct {
	ObjectModelID    int64  `gorm:"column:object_model_id"`
	BKTenantID       string `gorm:"column:bk_tenant_id"`
	ModelID          string `gorm:"column:model_id"`
	ModelName        string `gorm:"column:model_name"`
	BKCMDBObjectID   string `gorm:"column:bk_cmdb_obj_id"`
	DisplayFields    []byte `gorm:"column:display_fields"`
	InstDisplayName  string `gorm:"column:inst_display_name"`
	HostRelatedField string `gorm:"column:host_related_field"`
}

// ModelClientConfig 注入已选择 Kingeye schema 的只读连接。
type ModelClientConfig struct {
	DB *gorm.DB
}

// ModelClient 按租户和模型代码读取 object_model_v2 的最小丰富投影。
type ModelClient struct {
	db *gorm.DB
}

// NewModelClient 创建模型 Reader；不接管连接池所有权。
func NewModelClient(config ModelClientConfig) (*ModelClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create model client: db must not be nil")
	}
	return &ModelClient{db: config.DB}, nil
}

// GetModelByCode 按 bk_tenant_id + model_id 读取模型定义。
func (c *ModelClient) GetModelByCode(ctx context.Context, tenantID, modelCode string) (enrich.Model, bool, error) {
	if ctx == nil {
		return enrich.Model{}, false, fmt.Errorf("get model by code: context must not be nil")
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(modelCode) == "" {
		return enrich.Model{}, false, fmt.Errorf("get model by code: tenant ID and model code are required")
	}
	if err := ctx.Err(); err != nil {
		return enrich.Model{}, false, fmt.Errorf("get model by code: %w", err)
	}
	var row objectModelRow
	err := c.db.WithContext(ctx).Table(objectModelTable).
		Select("object_model_id, bk_tenant_id, model_id, model_name, bk_cmdb_obj_id, display_fields, inst_display_name, host_related_field").
		Where("bk_tenant_id = ? AND model_id = ?", tenantID, modelCode).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return enrich.Model{}, false, nil
	}
	if err != nil {
		return enrich.Model{}, false, fmt.Errorf("query object model: %w", err)
	}
	if row.ObjectModelID <= 0 || row.BKTenantID != tenantID || row.ModelID != modelCode || row.ModelName == "" {
		return enrich.Model{}, false, fmt.Errorf("%w: object model identity or name is invalid", enrich.ErrInvalidDataSourceResponse)
	}
	var displayFields []map[string]any
	if len(row.DisplayFields) != 0 && string(row.DisplayFields) != "null" {
		if err := json.Unmarshal(row.DisplayFields, &displayFields); err != nil {
			return enrich.Model{}, false, fmt.Errorf("%w: decode object model display fields: %w", enrich.ErrInvalidDataSourceResponse, err)
		}
	}
	fields := map[string]any{
		"object_model_id":    row.ObjectModelID,
		"object_model_code":  row.ModelID,
		"object_model_name":  row.ModelName,
		"bk_cmdb_obj_id":     row.BKCMDBObjectID,
		"display_fields":     displayFields,
		"inst_display_name":  row.InstDisplayName,
		"host_related_field": row.HostRelatedField,
	}
	return enrich.Model{
		TenantID: tenantID, ModelID: fmt.Sprint(row.ObjectModelID), ModelCode: modelCode, Fields: fields,
	}, true, nil
}
