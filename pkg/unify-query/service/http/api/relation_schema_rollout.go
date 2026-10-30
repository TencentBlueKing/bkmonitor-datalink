// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const relationSchemaVersionHeader = "X-Bk-Relation-Schema-Version"

// Run before decoding the body so a rollout race can safely use the legacy
// handler with the original tenant, space, query list and timestamps intact.
// Explicit v1beta3 probes without a rollout header retain their existing behavior.
func fallbackRelationSchema(c *gin.Context, legacy gin.HandlerFunc) bool {
	version := c.GetHeader(relationSchemaVersionHeader)
	if version == "" {
		return false
	}
	if err := v1beta3.EnsureSchemaVersion(c.Request.Context(), version); err != nil {
		log.Warnf(c.Request.Context(), "relation schema not ready; use legacy query: %v", err)
		c.Header("X-Bk-Relation-Fallback", "schema-not-ready")
		legacy(c)
		return true
	}
	return false
}
