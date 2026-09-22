// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 蓝鲸监控 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

var _ enrich.APMApplicationReader = (*APMApplicationClient)(nil)

const apmApplicationTable = "kapm_namespace"

type apmApplicationRow struct {
	TenantID string `gorm:"column:bk_tenant_id"`
	ID       int64  `gorm:"column:namespace_id"`
	Name     string `gorm:"column:namespace"`
	Alias    string `gorm:"column:namespace_alias"`
	BKBizID  int64  `gorm:"column:bk_biz_id"`
}

// APMApplicationClient 从 Kingeye APM 应用表读取租户内的应用身份。
type APMApplicationClient struct {
	db *gorm.DB
}

// APMApplicationClientConfig 注入已经选择 Kingeye schema 的 GORM 连接。
type APMApplicationClientConfig struct {
	DB *gorm.DB
}

// NewAPMApplicationClient 创建 APM 应用 Reader；数据库连接所有权保留在 Runtime。
func NewAPMApplicationClient(config APMApplicationClientConfig) (*APMApplicationClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create APM application client: db must not be nil")
	}
	return &APMApplicationClient{db: config.DB}, nil
}

// FindAPMApplications 按租户、业务和应用名称精确查询未删除的应用。
func (c *APMApplicationClient) FindAPMApplications(ctx context.Context, tenantID string, bizID int64, name string) ([]models.APMApplication, error) {
	if ctx == nil {
		return nil, fmt.Errorf("find APM applications: context must not be nil")
	}
	if tenantID == "" {
		return nil, fmt.Errorf("find APM applications: tenant ID is required")
	}
	if bizID <= 0 {
		return nil, fmt.Errorf("find APM applications: positive business ID is required")
	}
	if name == "" {
		return nil, fmt.Errorf("find APM applications: name is required")
	}
	var rows []apmApplicationRow
	err := c.db.WithContext(ctx).
		Table(apmApplicationTable).
		Select("bk_tenant_id", "namespace_id", "namespace", "namespace_alias", "bk_biz_id").
		Where("bk_tenant_id = ? AND bk_biz_id = ? AND namespace = ? AND is_deleted = ?", tenantID, bizID, name, false).
		Order("namespace_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("query APM applications: %w", err)
	}
	applications := make([]models.APMApplication, 0, len(rows))
	for _, row := range rows {
		if row.TenantID != tenantID || row.BKBizID != bizID || row.Name != name || row.ID <= 0 {
			return nil, fmt.Errorf("%w: APM application identity does not match query", enrich.ErrInvalidDataSourceResponse)
		}
		applications = append(applications, models.APMApplication{
			TenantID: row.TenantID,
			ID:       row.ID,
			Name:     row.Name,
			Alias:    row.Alias,
			BKBizID:  row.BKBizID,
		})
	}
	return applications, nil
}
