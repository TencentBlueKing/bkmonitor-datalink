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
	"linkd/internal/lifecycle/enrich/models"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"linkd/internal/lifecycle/enrich"
)

var (
	_                 enrich.BKStrategyReader = (*BKStrategyClient)(nil)
	bkStrategyColumns                         = []string{
		"id", "name", "bk_biz_id", "source", "scenario", "type", "is_enabled", "is_invalid", "invalid_type",
		"create_user", "create_time", "update_user", "update_time", "app", "path", "hash", "snippet", "priority",
		"priority_group_key",
	}
)

type bkStrategyHistoryRow struct {
	ID         int64          `gorm:"column:id"`
	StrategyID int64          `gorm:"column:strategy_id"`
	Content    datatypes.JSON `gorm:"column:content"`
}

// BKStrategyClientConfig 注入已经选择目标 schema 的平台策略数据库连接。
// 连接池的创建、Ping、容量设置和关闭仍由进程装配层负责。
type BKStrategyClientConfig struct {
	DB *gorm.DB
}

// BKStrategyClient 读取监控平台当前策略及历史快照。
type BKStrategyClient struct {
	db *gorm.DB
}

// NewBKStrategyClient 创建平台策略 Client；Client 不取得数据库连接所有权。
func NewBKStrategyClient(config BKStrategyClientConfig) (*BKStrategyClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create bk strategy client: db must not be nil")
	}
	return &BKStrategyClient{db: config.DB}, nil
}

// GetStrategyHistory 按全租户唯一的平台策略 ID 与历史 ID 联合读取完整策略快照。
func (c *BKStrategyClient) GetStrategyHistory(ctx context.Context, strategyID, historyID int64) (models.BkStrategyHistory, bool, error) {
	if ctx == nil {
		return models.BkStrategyHistory{}, false, fmt.Errorf("get platform strategy history: context must not be nil")
	}
	if strategyID <= 0 || historyID <= 0 {
		return models.BkStrategyHistory{}, false, fmt.Errorf("get platform strategy history: strategy and history IDs must be positive")
	}
	var row bkStrategyHistoryRow
	err := c.db.WithContext(ctx).
		Table("alarm_strategy_history").
		Select("id", "strategy_id", "content").
		Where("strategy_id = ?", strategyID).
		Where("id = ?", historyID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.BkStrategyHistory{}, false, nil
	}
	if err != nil {
		return models.BkStrategyHistory{}, false, fmt.Errorf("query platform strategy history: %w", err)
	}
	content, err := decodeJSONObject("alarm_strategy_history.content", row.Content)
	if err != nil {
		return models.BkStrategyHistory{}, false, err
	}
	return models.BkStrategyHistory{ID: row.ID, StrategyID: row.StrategyID, Content: content}, true, nil
}

// GetStrategy 按全租户唯一的平台策略 ID 读取当前策略记录。
func (c *BKStrategyClient) GetStrategy(ctx context.Context, strategyID int64) (models.BkStrategy, bool, error) {
	if ctx == nil {
		return models.BkStrategy{}, false, fmt.Errorf("get platform strategy: context must not be nil")
	}
	if strategyID <= 0 {
		return models.BkStrategy{}, false, fmt.Errorf("get platform strategy: strategy ID must be positive")
	}
	var result models.BkStrategy
	err := c.db.WithContext(ctx).
		Table("alarm_strategy_v2").
		Select(bkStrategyColumns).
		Where("id = ?", strategyID).
		Take(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.BkStrategy{}, false, nil
	}
	if err != nil {
		return models.BkStrategy{}, false, fmt.Errorf("query platform strategy: %w", err)
	}
	return result, true, nil
}
