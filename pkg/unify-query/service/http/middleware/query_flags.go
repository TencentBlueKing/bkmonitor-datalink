// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// QueryFlags 对所有公共查询及代理入口生效，不修改各接口的请求体。
func QueryFlags() gin.HandlerFunc {
	return func(c *gin.Context) {
		flags, err := metadata.ParseQueryFlags(c.GetHeader(metadata.QueryFlagsHeader))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error(), "trace_id": trace.TraceIDFromContext(c.Request.Context())})
			return
		}
		c.Request = c.Request.WithContext(metadata.WithQueryFlags(c.Request.Context(), flags))
		c.Next()
	}
}
