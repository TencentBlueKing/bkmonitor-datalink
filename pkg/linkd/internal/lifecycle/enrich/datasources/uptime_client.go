// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

var (
	_ enrich.UptimeReader     = (*UptimeClient)(nil)
	_ enrich.UptimeNodeReader = (*UptimeClient)(nil)
)

const (
	uptimeTaskTable = "home_application_uptimechecktask"
	uptimeNodeTable = "home_application_uptimechecknode"
)

type uptimeTaskRow struct {
	ID         int64  `gorm:"column:id"`
	TaskID     int64  `gorm:"column:task_id"`
	Name       string `gorm:"column:name"`
	Protocol   string `gorm:"column:protocol"`
	BKBizID    int64  `gorm:"column:bk_biz_id"`
	BKTenantID string `gorm:"column:bk_tenant_id"`
}

type uptimeNodeRow struct {
	ID         int64  `gorm:"column:id"`
	Name       string `gorm:"column:name"`
	PlatID     int64  `gorm:"column:plat_id"`
	IP         string `gorm:"column:ip"`
	BKTenantID string `gorm:"column:bk_tenant_id"`
}

// UptimeClientConfig 注入已选择 Kingeye schema 的只读连接。
type UptimeClientConfig struct {
	// DB 的连接池由 Runtime 管理，Client 不迁移表结构。
	DB *gorm.DB
}

// UptimeClient 按租户和蓝鲸拨测任务 ID 查询拨测任务。
type UptimeClient struct {
	db *gorm.DB
}

// NewUptimeClient 创建拨测 Client，不接管数据库连接生命周期。
func NewUptimeClient(config UptimeClientConfig) (*UptimeClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create uptime client: db must not be nil")
	}
	return &UptimeClient{db: config.DB}, nil
}

func parseUptimeNodeID(nodeID string) (int64, string, bool) {
	parts := strings.SplitN(nodeID, ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return 0, "", false
	}
	platID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || platID < 0 {
		return 0, "", false
	}
	return platID, parts[1], true
}

// GetUptimeNode 按租户和 plat_id:ip 查询未删除的拨测节点。
func (c *UptimeClient) GetUptimeNode(ctx context.Context, tenantID, nodeID string) (models.UptimeNode, bool, error) {
	if ctx == nil {
		return models.UptimeNode{}, false, fmt.Errorf("get uptime node: context must not be nil")
	}
	if strings.TrimSpace(tenantID) == "" {
		return models.UptimeNode{}, false, fmt.Errorf("get uptime node: tenant ID is required")
	}
	platID, ip, valid := parseUptimeNodeID(nodeID)
	if !valid {
		return models.UptimeNode{}, false, fmt.Errorf("get uptime node: node ID must use plat_id:ip")
	}
	if err := ctx.Err(); err != nil {
		return models.UptimeNode{}, false, fmt.Errorf("get uptime node: %w", err)
	}
	var rows []uptimeNodeRow
	err := c.db.WithContext(ctx).
		Table(uptimeNodeTable).
		Select("id", "name", "plat_id", "ip", "bk_tenant_id").
		Where("bk_tenant_id = ? AND plat_id = ? AND ip = ? AND is_deleted = ?", tenantID, platID, ip, false).
		Limit(2).Find(&rows).Error
	if err != nil {
		return models.UptimeNode{}, false, fmt.Errorf("query uptime node: %w", err)
	}
	if len(rows) == 0 {
		return models.UptimeNode{}, false, nil
	}
	if len(rows) != 1 {
		return models.UptimeNode{}, false, fmt.Errorf("%w: ambiguous uptime node identity", enrich.ErrInvalidDataSourceResponse)
	}
	row := rows[0]
	if row.ID <= 0 || row.PlatID != platID || row.IP != ip || row.BKTenantID != tenantID || row.Name == "" {
		return models.UptimeNode{}, false, fmt.Errorf("%w: uptime node identity, tenant, or name is invalid", enrich.ErrInvalidDataSourceResponse)
	}
	return models.UptimeNode{ID: row.ID, Name: row.Name, PlatID: row.PlatID, IP: row.IP, BKTenantID: row.BKTenantID}, true, nil
}

// GetUptimeTask 按租户和正整数 task_id 查询未删除任务。
// 告警维度 task_id 对应蓝鲸拨测任务 ID；返回的 ID 是 Linkd 资源实例使用的数据库主键。
// 查询只读取资源定位字段，避免加载可能含凭据的 config。
func (c *UptimeClient) GetUptimeTask(ctx context.Context, tenantID, taskID string) (models.UptimeTask, bool, error) {
	if ctx == nil {
		return models.UptimeTask{}, false, fmt.Errorf("get uptime task: context must not be nil")
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(taskID) == "" {
		return models.UptimeTask{}, false, fmt.Errorf("get uptime task: tenant ID and task ID are required")
	}
	id, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil || id <= 0 {
		return models.UptimeTask{}, false, fmt.Errorf("get uptime task: task ID must be a positive int64")
	}
	if err := ctx.Err(); err != nil {
		return models.UptimeTask{}, false, fmt.Errorf("get uptime task: %w", err)
	}
	var rows []uptimeTaskRow
	err = c.db.WithContext(ctx).
		Table(uptimeTaskTable).
		Select("id", "task_id", "name", "protocol", "bk_biz_id", "bk_tenant_id").
		Where("bk_tenant_id = ? AND task_id = ? AND is_deleted = ?", tenantID, id, false).
		Limit(2).Find(&rows).Error
	if err != nil {
		return models.UptimeTask{}, false, fmt.Errorf("query uptime task: %w", err)
	}
	if len(rows) == 0 {
		return models.UptimeTask{}, false, nil
	}
	if len(rows) != 1 {
		return models.UptimeTask{}, false, fmt.Errorf("%w: ambiguous uptime task identity", enrich.ErrInvalidDataSourceResponse)
	}
	row := rows[0]
	if row.ID <= 0 || row.TaskID != id || !models.UptimeProtocol(row.Protocol).Valid() || row.BKBizID < 0 || row.BKTenantID != tenantID {
		return models.UptimeTask{}, false, fmt.Errorf("%w: uptime task identity, tenant, or business ID is invalid", enrich.ErrInvalidDataSourceResponse)
	}
	return models.UptimeTask{
		ID: row.ID, TaskID: row.TaskID, Name: row.Name, Protocol: models.UptimeProtocol(row.Protocol),
		BKBizID: row.BKBizID, BKTenantID: row.BKTenantID,
	}, true, nil
}
