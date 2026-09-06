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

	"gorm.io/gorm"

	"linkd/internal/lifecycle/enrich"
)

var _ enrich.AlarmSourceReader = (*AlarmSourceClient)(nil)

const alarmSourceTable = "alarm_collect_alarmsource"

type alarmSourceRow struct {
	Name string `gorm:"column:name"`
}

// AlarmSourceClientConfig 注入已经选择 Kingeye schema 的 GORM 连接。
type AlarmSourceClientConfig struct {
	DB *gorm.DB
}

// AlarmSourceClient 只读查询 Kingeye 告警源定义。
type AlarmSourceClient struct {
	db *gorm.DB
}

// NewAlarmSourceClient 创建告警源 Client；数据库连接所有权保留在 Runtime。
func NewAlarmSourceClient(config AlarmSourceClientConfig) (*AlarmSourceClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create alarm source client: db must not be nil")
	}
	return &AlarmSourceClient{db: config.DB}, nil
}

// GetAlarmSourceName 按 bk_tenant_id 与主键 id 查询告警源名称。
func (c *AlarmSourceClient) GetAlarmSourceName(
	ctx context.Context,
	tenantID, sourceID string,
) (string, bool, error) {
	if ctx == nil {
		return "", false, fmt.Errorf("get alarm source name: context must not be nil")
	}
	if tenantID == "" || sourceID == "" {
		return "", false, fmt.Errorf("get alarm source name: tenant ID and source ID are required")
	}
	var row alarmSourceRow
	err := c.db.WithContext(ctx).
		Table(alarmSourceTable).
		Select("name").
		Where("bk_tenant_id = ? AND id = ?", tenantID, sourceID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query alarm source name: %w", err)
	}
	return row.Name, true, nil
}
