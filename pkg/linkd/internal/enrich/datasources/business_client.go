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
	"errors"
	"fmt"
	"strconv"

	"gorm.io/gorm"
	"linkd/internal/enrich"
)

var _ enrich.BusinessReader = (*BusinessClient)(nil)

const (
	businessTable     = "metadata_space"
	businessSpaceType = "bkcc"
)

type businessRow struct {
	BKTenantID *string `gorm:"column:bk_tenant_id"`
	SpaceID    string  `gorm:"column:space_id"`
	IsGlobal   bool    `gorm:"column:is_global"`
}

// BusinessClientConfig 注入已经选择 Kingeye schema 的 GORM 连接。
type BusinessClientConfig struct {
	DB *gorm.DB
}

// BusinessClient 只读查询 Kingeye BKCC 业务空间。
type BusinessClient struct {
	db *gorm.DB
}

// NewBusinessClient 创建业务空间 Client；数据库连接所有权保留在 Runtime。
func NewBusinessClient(config BusinessClientConfig) (*BusinessClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create business client: db must not be nil")
	}
	return &BusinessClient{db: config.DB}, nil
}

// IsGlobalBusiness 按租户和 BKCC 业务 ID 查询有效业务空间的全局属性。
func (c *BusinessClient) IsGlobalBusiness(ctx context.Context, tenantID string, bizID int64) (bool, bool, error) {
	if ctx == nil {
		return false, false, fmt.Errorf("get global business flag: context must not be nil")
	}
	if tenantID == "" || bizID <= 0 {
		return false, false, fmt.Errorf("get global business flag: tenant ID and positive business ID are required")
	}
	spaceID := strconv.FormatInt(bizID, 10)
	var row businessRow
	err := c.db.WithContext(ctx).
		Table(businessTable).
		Select("bk_tenant_id", "space_id", "is_global").
		Where("bk_tenant_id = ?", tenantID).
		Where("space_type_id = ?", businessSpaceType).
		Where("space_id = ?", spaceID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("query global business flag: %w", err)
	}
	if row.BKTenantID == nil || *row.BKTenantID != tenantID || row.SpaceID != spaceID {
		return false, false, fmt.Errorf("%w: metadata_space business identity does not match query", enrich.ErrInvalidDataSourceResponse)
	}
	return row.IsGlobal, true, nil
}
