// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 蓝鲸监控 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

var _ enrich.APMApplicationReader = (*APMApplicationClient)(nil)

const apmApplicationTable = "kapm_namespace"

type apmApplicationRow struct {
	TenantID       string `gorm:"column:bk_tenant_id"`
	ID             int64  `gorm:"column:namespace_id"`
	Name           string `gorm:"column:namespace"`
	NormalizedName string `gorm:"column:normalized_name"`
	Alias          string `gorm:"column:namespace_alias"`
	BKBizID        int64  `gorm:"column:bk_biz_id"`
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

// FindAPMApplications 按租户、业务精确匹配未删除应用的 namespace 或 normalized_name。
// 返回的 Name 保留请求拼写，供下游校验告警中的应用名称；不自行推导规范化规则。
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
		Select("bk_tenant_id", "namespace_id", "namespace", "normalized_name", "namespace_alias", "bk_biz_id").
		Where("bk_tenant_id = ? AND bk_biz_id = ? AND (namespace = ? OR normalized_name = ?) AND is_deleted = ?", tenantID, bizID, name, name, false).
		Order("namespace_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("query APM applications: %w", err)
	}
	applications := make([]models.APMApplication, 0, len(rows))
	for _, row := range rows {
		if row.TenantID != tenantID || row.BKBizID != bizID || (row.Name != name && row.NormalizedName != name) || row.ID <= 0 {
			return nil, fmt.Errorf("%w: APM application identity does not match query", enrich.ErrInvalidDataSourceResponse)
		}
		applications = append(applications, models.APMApplication{
			TenantID: row.TenantID,
			ID:       row.ID,
			Name:     name,
			Alias:    row.Alias,
			BKBizID:  row.BKBizID,
		})
	}
	return applications, nil
}
