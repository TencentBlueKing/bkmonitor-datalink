// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import "context"

// MockBusinessClient 提供开发期固定业务空间属性。
type MockBusinessClient struct{}

// IsGlobalBusiness 返回固定样例业务的非全局属性。
func (MockBusinessClient) IsGlobalBusiness(ctx context.Context, tenantID string, bizID int64) (bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if tenantID != SampleTenantID || bizID != SampleBizID {
		return false, false, nil
	}
	return false, true, nil
}
