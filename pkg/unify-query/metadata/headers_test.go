// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHeaders(t *testing.T) {
	InitMetadata()
	ctx := InitHashID(context.Background())

	t.Run("with nil headers", func(t *testing.T) {
		SetUser(ctx, &User{
			Key:      "test_key",
			SpaceUID: "test_space",
			TenantID: "test_tenant",
		})

		headers := Headers(ctx, nil)
		assert.NotNil(t, headers)
		assert.Equal(t, "test_key", headers[BkQuerySourceHeader])
		assert.Equal(t, "test_space", headers[SpaceUIDHeader])
		assert.Equal(t, "test_tenant", headers[TenantIDHeader])
	})

	t.Run("with existing headers", func(t *testing.T) {
		SetUser(ctx, &User{
			Key:      "test_key2",
			SpaceUID: "test_space2",
			TenantID: "test_tenant2",
		})

		existingHeaders := map[string]string{
			"Custom-Header": "custom_value",
		}
		headers := Headers(ctx, existingHeaders)
		assert.NotNil(t, headers)
		assert.Equal(t, "test_key2", headers[BkQuerySourceHeader])
		assert.Equal(t, "test_space2", headers[SpaceUIDHeader])
		assert.Equal(t, "test_tenant2", headers[TenantIDHeader])
		assert.Equal(t, "custom_value", headers["Custom-Header"])
	})

	t.Run("with empty user", func(t *testing.T) {
		ctx2 := InitHashID(context.Background())
		headers := Headers(ctx2, nil)
		assert.NotNil(t, headers)
		// 即使没有设置用户，也应该有这些 header 键，只是值为空
		assert.Contains(t, headers, BkQuerySourceHeader)
		assert.Contains(t, headers, SpaceUIDHeader)
		assert.Contains(t, headers, TenantIDHeader)
	})
}
