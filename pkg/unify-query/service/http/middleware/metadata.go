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
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// Params
type Params struct {
	SlowQueryThreshold time.Duration
}

// MetaData 初始化所有原数据
func MetaData(p *Params) gin.HandlerFunc {
	return func(c *gin.Context) {
		var (
			start     = time.Now()
			source    = c.Request.Header.Get(metadata.BkQuerySourceHeader)
			tenantID  = c.Request.Header.Get(metadata.TenantIDHeader)
			spaceUID  = c.Request.Header.Get(metadata.SpaceUIDHeader)
			skipSpace = c.Request.Header.Get(metadata.SkipSpaceHeader)

			hostName, ip = metadata.GetLocalHost()

			ctx = c.Request.Context()
			err error
		)

		ctx = metadata.InitHashID(ctx)
		c.Request = c.Request.WithContext(ctx)

		ctx, span := trace.NewSpan(ctx, "http-api-metadata")

		// 把用户名注入到 metadata 中
		metadata.SetUser(ctx, &metadata.User{
			Key:       source,
			TenantID:  tenantID,
			SpaceUID:  spaceUID,
			SkipSpace: skipSpace,
		})
		user := metadata.GetUser(ctx)
		metric.APIRequestInc(ctx, c.Request.URL.Path, metric.StatusReceived, spaceUID, user.Source)

		if span != nil {
			defer func() {
				span.Set("local-ip", ip)
				span.Set("local-host-name", hostName)

				sub := time.Since(start)
				metric.APIRequestSecond(ctx, sub, c.Request.URL.Path, spaceUID)

				// 记录慢查询
				if p != nil {
					if p.SlowQueryThreshold > 0 && sub.Milliseconds() > p.SlowQueryThreshold.Milliseconds() {
						codedErr := errno.ErrDataProcessFailed().
							WithComponent("HTTP中间件").
							WithOperation("慢查询监控").
							WithContext("请求路径", c.Request.URL.Path).
							WithContext("持续时间", sub.String()).
							WithSolution("检查查询性能和索引优化")
						log.WarnWithCodef(ctx, codedErr)
					}
				}

				span.Set("http-api-query-cost", int(sub.Milliseconds()))

				status := metadata.GetStatus(ctx)
				if status != nil {
					span.Set("http-api-status-code", status.Code)
					span.Set("http-api-status-message", status.Message)
				}

				span.End(&err)
			}()
		}

		c.Next()
	}
}
