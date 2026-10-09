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
	"strconv"

	"gorm.io/gorm"
	"linkd/internal/enrich"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
)

var _ enrich.CWStrategyReader = (*CWStrategyClient)(nil)

type monitorTemplateRow struct {
	ID       int64  `gorm:"column:id"`
	TenantID string `gorm:"column:bk_tenant_id"`
	Name     string `gorm:"column:name"`
}

// CWStrategyClientConfig 注入 Kingeye MySQL 连接，连接池由进程管理。
type CWStrategyClientConfig struct{ DB *gorm.DB }

// CWStrategyClient 只从 SplitRecord 发布材料构造策略视图，不读取旧策略配置表。
type CWStrategyClient struct{ db *gorm.DB }

// NewCWStrategyClient 不连接或迁移数据库，也不取得连接所有权。
func NewCWStrategyClient(config CWStrategyClientConfig) (*CWStrategyClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create cw strategy client: database is required")
	}
	return &CWStrategyClient{db: config.DB}, nil
}

// GetByStrategyID 读取同租户、同事件版本的当前发布态；缺失返回 found=false。
// 覆盖策略使用子记录自身的 ID。覆盖版本由父记录提供，普通丰富不承诺其历史重现；
// 精确文案入口仍单独拒绝缺少独立修订的覆盖记录。
func (c *CWStrategyClient) GetByStrategyID(ctx context.Context, query models.StrategyQuery) (models.CWStrategy, bool, error) {
	if ctx == nil || !query.Valid() {
		return models.CWStrategy{}, false, fmt.Errorf("strategy read requires context, tenant, split ID and version")
	}
	if err := ctx.Err(); err != nil {
		return models.CWStrategy{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, strategyReadTimeout)
	defer cancel()
	row, found, err := readStrategyPublication(ctx, c.db, query)
	if err != nil {
		var invalid *description.Error
		if errors.As(err, &invalid) {
			return models.CWStrategy{}, false, fmt.Errorf("%w: %w", enrich.ErrInvalidDataSourceResponse, err)
		}
		return models.CWStrategy{}, false, err
	}
	if !found {
		return models.CWStrategy{}, false, nil
	}
	payload, err := decodeStrategyPublication(row)
	if err != nil {
		return models.CWStrategy{}, false, fmt.Errorf("%w: %w", enrich.ErrInvalidDataSourceResponse, err)
	}
	strategy, err := cwStrategyFromPublication(row, payload)
	if err != nil {
		return models.CWStrategy{}, false, fmt.Errorf("%w: %w", enrich.ErrInvalidDataSourceResponse, err)
	}
	// 模板名称属于展示元数据；不借它反查或替换策略配置。
	var template monitorTemplateRow
	err = c.db.WithContext(ctx).Table("home_application_monitortemplate").Select("id,bk_tenant_id,name").Where("id = ? AND bk_tenant_id = ?", row.TemplateID, query.TenantID).Take(&template).Error
	if err != nil {
		return models.CWStrategy{}, false, fmt.Errorf("query monitor template name: %w", err)
	}
	if template.ID != row.TemplateID || template.TenantID != query.TenantID || template.Name == "" {
		return models.CWStrategy{}, false, fmt.Errorf("%w: monitor template identity mismatch", enrich.ErrInvalidDataSourceResponse)
	}
	strategy.MonitorTemplateName = template.Name
	return strategy, true, nil
}

func cwStrategyFromPublication(row strategyPublicationRow, payload strategyPublication) (models.CWStrategy, error) {
	entry := payload.Resolved[0]
	labels := entry.Metadata.Labels
	var spec models.CWStrategySpec
	if json.Unmarshal(entry.Spec, &spec) != nil {
		return models.CWStrategy{}, descriptionFailure("configuration_disabled_or_invalid")
	}
	if row.ParentID == nil && spec.ConfigType != payload.Set.ConfigType {
		return models.CWStrategy{}, descriptionFailure("publication_binding_invalid")
	}
	// 覆盖记录的实例标签允许整数或字符串，数据库列的字符串形态不再参与转换。
	var instanceID *string
	if raw := labels.ObjectInstanceID; len(raw) != 0 && string(raw) != "null" {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			var number int64
			if json.Unmarshal(raw, &number) != nil || number <= 0 {
				return models.CWStrategy{}, descriptionFailure("publication_binding_invalid")
			}
			value = strconv.FormatInt(number, 10)
		}
		if value == "" {
			return models.CWStrategy{}, descriptionFailure("publication_binding_invalid")
		}
		instanceID = &value
	}
	kind := entry.Kind
	if kind == "" {
		kind = models.CWStrategyKindStrategy
		if spec.CloudID != nil || spec.CloudType != "" || spec.CloudResourceType != "" {
			kind = models.CWStrategyKindCloud
		}
	}
	if kind != models.CWStrategyKindStrategy && kind != models.CWStrategyKindCloud {
		return models.CWStrategy{}, descriptionFailure("configuration_config_invalid")
	}
	businessID := labels.BusinessID
	// Kingeye 将多业务 DATA 等价投影折叠为模板业务下的全局策略；
	// 来源业务不能用于选择发布配置，仍由 Processor 校验全局业务关系。
	if row.ParentID == nil && payload.Set.ConfigType == "data" && len(payload.Resolved) > 1 && payload.Set.TemplateBusinessID != 0 {
		businessID = payload.Set.TemplateBusinessID
	}
	return models.CWStrategy{
		Active: true, Kind: kind, APIVersion: "v1alpha1", UID: entry.Metadata.UID,
		Name: entry.Metadata.Name, Namespace: entry.Metadata.Namespace, Annotations: entry.Metadata.Annotations,
		Spec: spec, Status: models.CWStrategyStatus{BKStrategyID: row.ID},
		BKTenantID: &labels.TenantID, BKBizID: &businessID, IsDefault: labels.IsDefault,
		DefaultStrategyConfigUID: labels.DefaultConfigUID, MonitorTemplateID: &row.TemplateID,
		ConfigID: &labels.ConfigID, ObjectModelCode: labels.ObjectModelCode, BKObjectInstID: instanceID,
	}, nil
}
