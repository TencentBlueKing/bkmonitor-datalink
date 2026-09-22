// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 蓝鲸监控 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

var _ enrich.LogThemeReader = (*LogThemeClient)(nil)

const logThemeTable = "log_theme_logtheme"

type logThemeRow struct {
	TenantID string `gorm:"column:bk_tenant_id"`
	ID       int64  `gorm:"column:log_theme_id"`
	Name     string `gorm:"column:log_theme_name"`
}

// LogThemeClient 从 Kingeye 日志主题表读取日志主题名称。
type LogThemeClient struct {
	db *gorm.DB
}

// LogThemeClientConfig 注入已经选择 Kingeye schema 的 GORM 连接。
type LogThemeClientConfig struct {
	DB *gorm.DB
}

// NewLogThemeClient 创建日志主题 Reader；数据库连接所有权保留在 Runtime。
func NewLogThemeClient(config LogThemeClientConfig) (*LogThemeClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create log theme client: db must not be nil")
	}
	return &LogThemeClient{db: config.DB}, nil
}

// GetLogTheme 按调用方提供的租户和日志主题 ID读取主题 ID和名称。
func (c *LogThemeClient) GetLogTheme(ctx context.Context, tenantID string, themeID int64) (models.LogTheme, bool, error) {
	if ctx == nil {
		return models.LogTheme{}, false, fmt.Errorf("get log theme: context must not be nil")
	}
	if tenantID == "" {
		return models.LogTheme{}, false, fmt.Errorf("get log theme: tenant ID is required")
	}
	if themeID <= 0 {
		return models.LogTheme{}, false, fmt.Errorf("get log theme: positive theme ID is required")
	}
	var row logThemeRow
	err := c.db.WithContext(ctx).
		Table(logThemeTable).
		Select("bk_tenant_id", "log_theme_id", "log_theme_name").
		Where("bk_tenant_id = ? AND log_theme_id = ?", tenantID, themeID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.LogTheme{}, false, nil
	}
	if err != nil {
		return models.LogTheme{}, false, fmt.Errorf("query log theme: %w", err)
	}
	if row.TenantID != tenantID || row.ID != themeID {
		return models.LogTheme{}, false, fmt.Errorf("%w: log theme identity does not match query", enrich.ErrInvalidDataSourceResponse)
	}
	return models.LogTheme{TenantID: row.TenantID, ID: row.ID, Name: row.Name}, true, nil
}
