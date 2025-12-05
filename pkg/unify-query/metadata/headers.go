// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import "context"

// Headers 统一注入请求 header 头信息
// 从上下文中获取用户信息，并将用户相关的标识信息注入到 HTTP 请求头中
// 参数:
//   - ctx: 上下文对象，包含用户信息
//   - headers: 现有的请求头映射，如果为 nil 则创建新的映射
//
// 返回: 注入用户信息后的请求头映射
// 注入的 header 包括:
//   - BkQuerySourceHeader: 查询来源标识（用户 Key）
//   - SpaceUIDHeader: 空间 UID
//   - TenantIDHeader: 租户 ID
func Headers(ctx context.Context, headers map[string]string) map[string]string {
	if headers == nil {
		headers = make(map[string]string)
	}

	user := GetUser(ctx)
	headers[BkQuerySourceHeader] = user.Key
	headers[SpaceUIDHeader] = user.SpaceUID
	headers[TenantIDHeader] = user.TenantID
	return headers
}
