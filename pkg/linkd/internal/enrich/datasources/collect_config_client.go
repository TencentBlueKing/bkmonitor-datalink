// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

var _ enrich.CollectConfigReader = (*CollectConfigClient)(nil)

const collectConfigTable = "core_v1alpha1_collect"

// 数据库投影字段可空；先保留 NULL，再拒绝无法定位资源的记录。
type collectConfigRow struct {
	UID             string  `gorm:"column:uid"`
	BKTenantID      *string `gorm:"column:bk_tenant_id"`
	BKObjectCode    *string `gorm:"column:bk_object_code"`
	BKInstID        *int64  `gorm:"column:bk_inst_id"`
	FinalBizID      *int64  `gorm:"column:final_biz_id"`
	RemoteCollect   int64   `gorm:"column:remote_collect"`
	BKCollectTaskID string  `gorm:"column:bk_collect_task_id"`
	TaskIDType      string  `gorm:"column:task_id_type"`
}

// CollectConfigClientConfig 注入已选择 Kingeye schema 的只读连接。
type CollectConfigClientConfig struct {
	// DB 的连接池由 Runtime 管理，Client 不迁移表结构。
	DB *gorm.DB
}

// CollectConfigClient 按租户和平台采集任务 ID 查询声明式采集配置。
type CollectConfigClient struct {
	db *gorm.DB
}

// NewCollectConfigClient 创建采集配置 Reader；不接管连接池所有权。
func NewCollectConfigClient(config CollectConfigClientConfig) (*CollectConfigClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create collect config client: db must not be nil")
	}
	return &CollectConfigClient{db: config.DB}, nil
}

// GetCollectConfig 按 bk_tenant_id 和 status.bk_collect_task_id 精确查找有效配置。
// 任务 ID 是不透明字符串；JSON 数字和字符串均按文本匹配，前导零不归一化。
// 同租户多条有效配置命中时返回 ErrInvalidDataSourceResponse，避免任意选择告警对象。
func (c *CollectConfigClient) GetCollectConfig(ctx context.Context, tenantID, taskID string) (models.CollectConfig, bool, error) {
	if ctx == nil {
		return models.CollectConfig{}, false, fmt.Errorf("get collect config: context must not be nil")
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(taskID) == "" {
		return models.CollectConfig{}, false, fmt.Errorf("get collect config: tenant ID and task ID are required")
	}
	if err := ctx.Err(); err != nil {
		return models.CollectConfig{}, false, fmt.Errorf("get collect config: %w", err)
	}
	// CollectStatus 使用 "0" 表示尚未取得平台任务身份，不能据此关联告警。
	if taskID == "0" {
		return models.CollectConfig{}, false, nil
	}
	// 只取定位字段，避免加载 spec 中的采集凭据。最多两条即可判断身份是否歧义。
	// collect_enable 仅表示启停，停用后的在途告警仍需定位；active 才是软删除标记。
	var rows []collectConfigRow
	err := c.db.WithContext(ctx).Table(collectConfigTable).
		Select("uid, bk_tenant_id, bk_object_code, bk_inst_id, final_biz_id, "+
			"CASE JSON_UNQUOTE(JSON_EXTRACT(spec, '$.remote_collect_info.is_remote_collect')) WHEN 'true' THEN 1 ELSE 0 END AS remote_collect, "+
			"JSON_UNQUOTE(JSON_EXTRACT(status, '$.bk_collect_task_id')) AS bk_collect_task_id, "+
			"JSON_TYPE(JSON_EXTRACT(status, '$.bk_collect_task_id')) AS task_id_type").
		Where("bk_tenant_id = ? AND active = ?", tenantID, true).
		Where("JSON_UNQUOTE(JSON_EXTRACT(status, '$.bk_collect_task_id')) = ?", taskID).
		Where("COALESCE(JSON_UNQUOTE(JSON_EXTRACT(status, '$.delete_status')), '') NOT IN (?, ?)", "deleting", "deleted").
		Limit(2).Find(&rows).Error
	if err != nil {
		return models.CollectConfig{}, false, fmt.Errorf("query collect config: %w", err)
	}
	if len(rows) == 0 {
		return models.CollectConfig{}, false, nil
	}
	if len(rows) != 1 {
		return models.CollectConfig{}, false, fmt.Errorf("%w: ambiguous collect task identity", enrich.ErrInvalidDataSourceResponse)
	}
	row := rows[0]
	if row.UID == "" || row.BKTenantID == nil || *row.BKTenantID != tenantID ||
		row.BKCollectTaskID != taskID || (row.TaskIDType != "STRING" && row.TaskIDType != "INTEGER") ||
		row.BKObjectCode == nil || strings.TrimSpace(*row.BKObjectCode) == "" || row.BKInstID == nil || *row.BKInstID <= 0 {
		return models.CollectConfig{}, false, fmt.Errorf("%w: invalid collect task identity or resource fields", enrich.ErrInvalidDataSourceResponse)
	}
	return models.CollectConfig{
		UID: row.UID, BKTenantID: *row.BKTenantID, BKCollectTaskID: row.BKCollectTaskID,
		BKObjectCode: *row.BKObjectCode, BKInstID: *row.BKInstID,
		FinalBizID: row.FinalBizID, IsRemoteCollect: row.RemoteCollect == 1,
	}, true, nil
}
