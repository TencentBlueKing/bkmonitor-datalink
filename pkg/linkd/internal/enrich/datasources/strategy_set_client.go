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
	"fmt"
	"strings"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

const maxStrategySetSpecBytes = 1 << 20
const maxStrategySetConfigs = 256

// StrategySetClient 只读当前声明式 StrategySet；不读取历史或配置修订表。
type StrategySetClient struct{ db *gorm.DB }

var _ enrich.StrategySetReader = (*StrategySetClient)(nil)

// NewStrategySetClient 注入已选定 Kingeye schema 的连接；连接由装配层管理。
func NewStrategySetClient(db *gorm.DB) (*StrategySetClient, error) {
	if db == nil {
		return nil, fmt.Errorf("strategy set client requires database")
	}
	return &StrategySetClient{db: db}, nil
}

type strategySetRow struct {
	UID               string         `gorm:"column:uid"`
	BKTenantID        string         `gorm:"column:bk_tenant_id"`
	MonitorTemplateID int64          `gorm:"column:monitor_template_id"`
	Kind              string         `gorm:"column:kind"`
	APIVersion        string         `gorm:"column:api_version"`
	Spec              datatypes.JSON `gorm:"column:spec"`
}

// GetStrategySet 按租户和模板读取最多两条 active 记录，重复身份明确报错。
// 数据库先限制 JSON 大小，避免将超限配置传回进程；缺失不得按其他租户或模板回退。
func (c *StrategySetClient) GetStrategySet(ctx context.Context, tenantID string, monitorTemplateID int64) (models.StrategySet, bool, error) {
	if ctx == nil || tenantID == "" || strings.TrimSpace(tenantID) != tenantID || monitorTemplateID <= 0 {
		return models.StrategySet{}, false, fmt.Errorf("strategy set read requires context, tenant and template ID")
	}
	if err := ctx.Err(); err != nil {
		return models.StrategySet{}, false, err
	}
	var rows []strategySetRow
	err := c.db.WithContext(ctx).Table("core_v1alpha1_strategyset").
		Select("uid, bk_tenant_id, monitor_template_id, kind, api_version, CASE WHEN OCTET_LENGTH(spec) <= ? THEN spec ELSE NULL END AS spec", maxStrategySetSpecBytes).
		Where("bk_tenant_id = ? AND monitor_template_id = ? AND active = ?", tenantID, monitorTemplateID, true).
		Limit(2).Find(&rows).Error
	if err != nil {
		return models.StrategySet{}, false, fmt.Errorf("read strategy set: %w", err)
	}
	if len(rows) == 0 {
		return models.StrategySet{}, false, nil
	}
	if len(rows) != 1 {
		return models.StrategySet{}, false, invalidStrategySet("duplicate template identity")
	}
	row := rows[0]
	if row.Kind != "StrategySet" || row.APIVersion != "v1alpha1" || row.UID == "" {
		return models.StrategySet{}, false, invalidStrategySet("kind, schema or UID invalid")
	}
	if len(row.Spec) == 0 || len(row.Spec) > maxStrategySetSpecBytes {
		return models.StrategySet{}, false, invalidStrategySet("spec size invalid")
	}
	value := models.StrategySet{UID: row.UID, BKTenantID: row.BKTenantID, MonitorTemplateID: row.MonitorTemplateID}
	if err := json.Unmarshal(row.Spec, &value.Spec); err != nil {
		return models.StrategySet{}, false, invalidStrategySet("spec JSON invalid")
	}
	if !value.ValidIdentity(tenantID, monitorTemplateID) || len(value.Spec.StrategyConfigs) > maxStrategySetConfigs {
		return models.StrategySet{}, false, invalidStrategySet("identity or config count invalid")
	}
	return value, true, nil
}

func invalidStrategySet(reason string) error {
	return fmt.Errorf("%w: strategy set %s", enrich.ErrInvalidDataSourceResponse, reason)
}
