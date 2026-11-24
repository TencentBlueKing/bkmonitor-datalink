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

var _ enrich.CloudResourceReader = (*CloudResourceClient)(nil)

const cloudResourceTable = "cloud_application_cloudresource"

type cloudResourceRow struct {
	TenantID        string `gorm:"column:bk_tenant_id"`
	CloudID         string `gorm:"column:cloud_id"`
	InstanceID      string `gorm:"column:instanceid"`
	ResourceType    string `gorm:"column:type"`
	Name            string `gorm:"column:name"`
	CloudName       string `gorm:"column:cloud_name"`
	CloudTenantID   string `gorm:"column:cloud_tenant_id"`
	ObjectModelCode string `gorm:"column:object_model_code"`
	ModelInstanceID string `gorm:"column:model_instance_id"`
}

// CloudResourceClient 从 Kingeye CloudResource 表读取云资源及其所属云平台名称。
type CloudResourceClient struct {
	db *gorm.DB
}

// CloudResourceClientConfig 注入已经选择 Kingeye schema 的 GORM 连接。
type CloudResourceClientConfig struct {
	DB *gorm.DB
}

// NewCloudResourceClient 创建云资源 Reader；数据库连接所有权保留在 Runtime。
func NewCloudResourceClient(config CloudResourceClientConfig) (*CloudResourceClient, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("create cloud resource client: db must not be nil")
	}
	return &CloudResourceClient{db: config.DB}, nil
}

// GetCloudResource 按调用方租户、云平台、资源类型和实例身份读取云资源。
func (c *CloudResourceClient) GetCloudResource(
	ctx context.Context,
	tenantID, cloudID, resourceType, instanceID string,
) (models.CloudResource, bool, error) {
	if ctx == nil {
		return models.CloudResource{}, false, fmt.Errorf("get cloud resource: context must not be nil")
	}
	if tenantID == "" || cloudID == "" || resourceType == "" || instanceID == "" {
		return models.CloudResource{}, false, fmt.Errorf("get cloud resource: tenant, cloud ID, resource type, and instance ID are required")
	}
	var row cloudResourceRow
	err := c.db.WithContext(ctx).
		Table(cloudResourceTable+" AS resource").
		Select("resource.bk_tenant_id, resource.cloud_id, resource.instanceid, resource.type, resource.name, resource.object_model_code, resource.id AS model_instance_id, cloud.name AS cloud_name, cloud.bk_tenant_id AS cloud_tenant_id").
		Joins("JOIN cloud_application_cloud AS cloud ON cloud.id = resource.cloud_id").
		Where("resource.bk_tenant_id = ? AND resource.cloud_id = ? AND resource.type = ? AND resource.instanceid = ?", tenantID, cloudID, resourceType, instanceID).
		Where("cloud.bk_tenant_id = resource.bk_tenant_id").
		Limit(1).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.CloudResource{}, false, nil
	}
	if err != nil {
		return models.CloudResource{}, false, fmt.Errorf("query cloud resource: %w", err)
	}
	if row.TenantID != tenantID || row.CloudTenantID != tenantID || row.CloudID != cloudID || row.ResourceType != resourceType || row.InstanceID != instanceID {
		return models.CloudResource{}, false, fmt.Errorf("%w: cloud resource identity does not match query", enrich.ErrInvalidDataSourceResponse)
	}
	return models.CloudResource{
		TenantID: row.TenantID, CloudID: row.CloudID, InstanceID: row.InstanceID,
		ResourceType: row.ResourceType, Name: row.Name, CloudName: row.CloudName,
		ObjectModelCode: row.ObjectModelCode, ModelInstanceID: row.ModelInstanceID,
	}, true, nil
}
