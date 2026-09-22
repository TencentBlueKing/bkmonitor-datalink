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
	"fmt"

	"linkd/internal/enrich/models"
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
