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
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

var (
	_                 enrich.CWStrategyReader = (*CWStrategyClient)(nil)
	cwStrategyColumns                         = []string{
		"created_at", "created_by", "updated_at", "updated_by", "active", "kind", "api_version", "name", "namespace",
		"uid", "annotations", "spec", "status", "bk_tenant_id", "bk_biz_id", "is_default", "default_strategy_config_uid",
		"monitor_template_id", "config_id", "object_model_code", "bk_object_inst_id",
	}
)

type cwStrategyRow struct {
	CreatedAt                time.Time      `gorm:"column:created_at"`
	CreatedBy                string         `gorm:"column:created_by"`
	UpdatedAt                time.Time      `gorm:"column:updated_at"`
	UpdatedBy                string         `gorm:"column:updated_by"`
	Active                   bool           `gorm:"column:active"`
	Kind                     string         `gorm:"column:kind"`
	APIVersion               string         `gorm:"column:api_version"`
	Name                     string         `gorm:"column:name"`
	Namespace                string         `gorm:"column:namespace"`
	UID                      string         `gorm:"column:uid"`
	Annotations              datatypes.JSON `gorm:"column:annotations"`
	Spec                     datatypes.JSON `gorm:"column:spec"`
	Status                   datatypes.JSON `gorm:"column:status"`
	BKTenantID               *string        `gorm:"column:bk_tenant_id"`
	BKBizID                  *int64         `gorm:"column:bk_biz_id"`
	IsDefault                *bool          `gorm:"column:is_default"`
	DefaultStrategyConfigUID *string        `gorm:"column:default_strategy_config_uid"`
	MonitorTemplateID        *int64         `gorm:"column:monitor_template_id"`
	ConfigID                 *string        `gorm:"column:config_id"`
	ObjectModelCode          *string        `gorm:"column:object_model_code"`
	BKObjectInstID           *string        `gorm:"column:bk_object_inst_id"`
}

// CWStrategyClientConfig 注入已经选择目标 schema 的鲸眼声明式策略数据库连接。
// 连接池的创建、Ping、容量设置和关闭仍由进程装配层负责。
type CWStrategyClientConfig struct {
	DB *gorm.DB
}

// CWStrategyClient 读取鲸眼声明式监控策略及云策略。
type CWStrategyClient struct {
	db *gorm.DB
}

// NewCWStrategyClient 创建鲸眼策略 Client；Client 不取得数据库连接所有权。
func NewCWStrategyClient(config CWStrategyClientConfig) (*CWStrategyClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create cw strategy client: db must not be nil")
	}
	return &CWStrategyClient{db: config.DB}, nil
}

// GetByBKStrategyID 按 status.bk_strategy_id 读取全租户唯一的鲸眼声明式策略。
func (c *CWStrategyClient) GetByBKStrategyID(ctx context.Context, tenantID string, bkStrategyID int64) (models.CWStrategy, bool, error) {
	if ctx == nil {
		return models.CWStrategy{}, false, fmt.Errorf("get cw strategy by bk strategy ID: context must not be nil")
	}
	if tenantID == "" || bkStrategyID <= 0 {
		return models.CWStrategy{}, false, fmt.Errorf("get cw strategy by bk strategy ID: tenant ID and positive strategy ID are required")
	}
	return c.take(
		c.db.WithContext(ctx).
			Where("bk_tenant_id = ?", tenantID).
			Where(datatypes.JSONQuery("status").Equals(bkStrategyID, "bk_strategy_id")),
		tenantID,
		bkStrategyID,
		"query cw strategy by bk strategy ID",
	)
}

func (c *CWStrategyClient) take(query *gorm.DB, tenantID string, strategyID int64, operation string) (models.CWStrategy, bool, error) {
	var row cwStrategyRow
	err := query.
		Table("core_v1alpha1_strategy").
		Select(cwStrategyColumns).
		Order("updated_at DESC").
		Order("uid").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.CWStrategy{}, false, nil
	}
	if err != nil {
		return models.CWStrategy{}, false, fmt.Errorf("%s: %w", operation, err)
	}
	if tenantID != "" && (row.BKTenantID == nil || *row.BKTenantID != tenantID) {
		return models.CWStrategy{}, false, fmt.Errorf("%w: core_v1alpha1_strategy tenant identity does not match query", enrich.ErrInvalidDataSourceResponse)
	}
	result, err := cwStrategyFromRow(row)
	if err != nil {
		return models.CWStrategy{}, false, fmt.Errorf("%w: %w", enrich.ErrInvalidDataSourceResponse, err)
	}
	if strategyID > 0 && result.Status.BKStrategyID != strategyID {
		return models.CWStrategy{}, false, fmt.Errorf("%w: core_v1alpha1_strategy strategy identity does not match query", enrich.ErrInvalidDataSourceResponse)
	}
	return result, true, nil
}

func cwStrategyFromRow(row cwStrategyRow) (models.CWStrategy, error) {
	annotations, err := decodeJSONObject("core_v1alpha1_strategy.annotations", row.Annotations)
	if err != nil {
		return models.CWStrategy{}, err
	}
	result := models.CWStrategy{
		CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt, UpdatedBy: row.UpdatedBy,
		Active: row.Active, Kind: models.CWStrategyKind(row.Kind), APIVersion: row.APIVersion, Name: row.Name, Namespace: row.Namespace, UID: row.UID,
		Annotations: annotations, BKTenantID: row.BKTenantID, BKBizID: row.BKBizID, IsDefault: row.IsDefault,
		DefaultStrategyConfigUID: row.DefaultStrategyConfigUID, MonitorTemplateID: row.MonitorTemplateID, ConfigID: row.ConfigID,
		ObjectModelCode: row.ObjectModelCode, BKObjectInstID: row.BKObjectInstID,
	}
	if err := json.Unmarshal(row.Spec, &result.Spec); err != nil {
		return models.CWStrategy{}, fmt.Errorf("decode core_v1alpha1_strategy.spec: %w", err)
	}
	if err := json.Unmarshal(row.Status, &result.Status); err != nil {
		return models.CWStrategy{}, fmt.Errorf("decode core_v1alpha1_strategy.status: %w", err)
	}
	return result, nil
}
