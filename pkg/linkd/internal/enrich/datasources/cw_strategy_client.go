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

// CWStrategyClient 从不可变 Strategy revision 构造策略视图，不读取 SplitRecord 当前态。
type CWStrategyClient struct{ db *gorm.DB }

// NewCWStrategyClient 不连接或迁移数据库，也不取得连接所有权。
func NewCWStrategyClient(config CWStrategyClientConfig) (*CWStrategyClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create cw strategy client: database is required")
	}
	return &CWStrategyClient{db: config.DB}, nil
}

// GetByStrategyID 按同租户、策略 ID 和事件版本读取历史快照；缺失返回 found=false。
// 策略管理业务取自唯一历史记录，不用事件业务过滤全局策略，也不回退 current/latest。
func (c *CWStrategyClient) GetByStrategyID(ctx context.Context, query models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
	if ctx == nil || !query.Valid() {
		return models.CWStrategy{}, false, fmt.Errorf("strategy read requires context, tenant, strategy ID and revision")
	}
	if err := ctx.Err(); err != nil {
		return models.CWStrategy{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, strategyReadTimeout)
	defer cancel()
	row, found, err := readStrategyRevision(ctx, c.db, query)
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
	payload, err := decodeStrategyRevision(row)
	if err != nil {
		return models.CWStrategy{}, false, fmt.Errorf("%w: %w", enrich.ErrInvalidDataSourceResponse, err)
	}
	strategy, err := cwStrategyFromRevision(row, payload)
	if err != nil {
		return models.CWStrategy{}, false, fmt.Errorf("%w: %w", enrich.ErrInvalidDataSourceResponse, err)
	}
	// 模板名称属于展示元数据；不借它反查或替换策略配置。
	var template monitorTemplateRow
	err = c.db.WithContext(ctx).Table("home_application_monitortemplate").Select("id,bk_tenant_id,name").Where("id = ? AND bk_tenant_id = ?", payload.RuntimeConfig.Metadata.Labels.TemplateID, query.TenantID).Take(&template).Error
	if err != nil {
		return models.CWStrategy{}, false, fmt.Errorf("query monitor template name: %w", err)
	}
	if template.ID != payload.RuntimeConfig.Metadata.Labels.TemplateID || template.TenantID != query.TenantID || template.Name == "" {
		return models.CWStrategy{}, false, fmt.Errorf("%w: monitor template identity mismatch", enrich.ErrInvalidDataSourceResponse)
	}
	strategy.MonitorTemplateName = template.Name
	return strategy, true, nil
}

func cwStrategyFromRevision(row strategyRevisionRow, payload strategyRevisionSnapshot) (models.CWStrategy, error) {
	entry := payload.RuntimeConfig
	labels := entry.Metadata.Labels
	var spec models.CWStrategySpec
	if json.Unmarshal(entry.Spec, &spec) != nil || spec.StrategyItem == nil || (spec.ConfigType != "data" && spec.ConfigType != "target") {
		return models.CWStrategy{}, descriptionFailure("configuration_disabled_or_invalid")
	}
	if err := bindRevisionDatasetLocations(&spec, payload.Items); err != nil {
		return models.CWStrategy{}, err
	}
	if err := bindRevisionMetricReferences(&spec, payload.Items); err != nil {
		return models.CWStrategy{}, err
	}
	if err := bindRevisionDerivedKind(&spec, payload.Items); err != nil {
		return models.CWStrategy{}, err
	}
	// 覆盖记录的实例标签允许整数或字符串，数据库列的字符串形态不再参与转换。
	var instanceID *string
	if raw := labels.ObjectInstanceID; len(raw) != 0 && string(raw) != "null" {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			var number int64
			if json.Unmarshal(raw, &number) != nil || number <= 0 {
				return models.CWStrategy{}, descriptionFailure("strategy_revision_binding_invalid")
			}
			value = strconv.FormatInt(number, 10)
		}
		if value == "" {
			return models.CWStrategy{}, descriptionFailure("strategy_revision_binding_invalid")
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
	businessID := row.BusinessID
	return models.CWStrategy{
		Active: true, Kind: kind, APIVersion: "v1alpha1", UID: entry.Metadata.UID,
		Name: entry.Metadata.Name, Namespace: entry.Metadata.Namespace, Annotations: entry.Metadata.Annotations,
		Spec: spec, Status: models.CWStrategyStatus{BKStrategyID: row.StrategyID}, FirstExecutionQuery: firstExecutionQuery(payload.Items),
		BKTenantID: &labels.TenantID, BKBizID: &businessID, SpaceUID: payload.SpaceUID, IsDefault: labels.IsDefault,
		DefaultStrategyConfigUID: labels.DefaultConfigUID, MonitorTemplateID: &payload.RuntimeConfig.Metadata.Labels.TemplateID,
		ConfigID: &labels.ConfigID, ObjectModelCode: labels.ObjectModelCode, BKObjectInstID: instanceID,
	}, nil
}
