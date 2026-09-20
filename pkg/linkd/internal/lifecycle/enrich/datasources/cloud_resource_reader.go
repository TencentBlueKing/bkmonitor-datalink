package datasources

import (
	"context"
	"fmt"

	"linkd/internal/lifecycle/enrich/models"
)

// StaticCloudResourceReader 为尚未接入真实 CloudResource 存储的测试/过渡实现提供窄接口。
type StaticCloudResourceReader struct {
	Resources map[string]models.CloudResource
}

// GetCloudResource 按租户、云平台、类型和实例身份查找资源。
func (r StaticCloudResourceReader) GetCloudResource(_ context.Context, tenantID, cloudID, resourceType, instanceID string) (models.CloudResource, bool, error) {
	key := fmt.Sprintf("%s/%s/%s/%s", tenantID, cloudID, resourceType, instanceID)
	resource, found := r.Resources[key]
	return resource, found, nil
}
