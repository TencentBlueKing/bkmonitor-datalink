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
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// Params
type Params struct {
	SlowQueryThreshold time.Duration
}

var (
	once        sync.Once
	instancedIP string
)

// get instance ip
func getInstanceip() (string, error) {
	interfaceStatList, err := net.Interfaces()
	if err != nil {
		log.Errorf(context.TODO(), "failed to get instance ip,error:%v", err)
		return "", err
	}
	addrList := interfaceStatList[len(interfaceStatList)-1]
	if len(addrList.Addrs) > 0 {
		return addrList.Addrs[len(addrList.Addrs)-1].Addr, nil
	}
	return "", nil
}

// get instance ip single pass
func singleGetInstance() string {
	once.Do(func() {
		instancedIP, _ = getInstanceip()
	})
	return instancedIP
}

// Timer 进行请求处理时间记录
func Timer(p *Params) gin.HandlerFunc {
	return func(c *gin.Context) {
		var (
			ctx         = c.Request.Context()
			span        oleltrace.Span
			start       = time.Now()
			startMem, _ = mem.VirtualMemory()
			instanceIP  = singleGetInstance()
		)
		ctx, span = trace.IntoContext(ctx, trace.TracerName, "http-api")

		trace.InsertStringIntoSpan("http-api-url", c.Request.URL.Path, span)
		trace.InsertIntIntoSpan("start-mem-total", int(startMem.Total), span)

		if span != nil {
			defer func() {
				endMem, _ := mem.VirtualMemory()
				trace.InsertIntIntoSpan("start-mem-free", int(startMem.Free), span)
				trace.InsertIntIntoSpan("end-mem-free", int(endMem.Free), span)
				trace.InsertIntIntoSpan("mem-use", int(startMem.Free-endMem.Free), span)
				trace.InsertStringIntoSpan("instance-ip", instanceIP, span)

				sub := time.Since(start)
				metric.RequestSecond(ctx, sub, c.Request.URL.Path)
				// 记录慢查询
				if p.SlowQueryThreshold > 0 && sub.Milliseconds() > p.SlowQueryThreshold.Milliseconds() {
					log.Errorf(ctx,
						fmt.Sprintf(
							"slow query log request: %s, duration: %s",
							c.Request.URL.Path, sub.String(),
						),
					)
				}
				trace.InsertIntIntoSpan("http-api-query-cost", int(sub.Milliseconds()), span)

				status := metadata.GetStatus(ctx)
				if status != nil {
					trace.InsertStringIntoSpan("http-api-status-code", status.Code, span)
					trace.InsertStringIntoSpan("http-api-status-message", status.Message, span)
				}

				log.Debugf(context.TODO(), "request:%s handled duration:%s", c.Request.URL.Path, sub)
				span.End()
			}()
		}

		// 把用户名注入到 metadata 中
		source := c.Request.Header.Get(metadata.BkQuerySourceHeader)
		spaceUid := c.Request.Header.Get(metadata.SpaceUIDHeader)

		metadata.SetUser(ctx, source, spaceUid)

		user := metadata.GetUser(ctx)
		trace.InsertStringIntoSpan("metadata-key", user.Key, span)
		trace.InsertStringIntoSpan("metadata-source", user.Source, span)
		trace.InsertStringIntoSpan("metadata-name", user.Name, span)
		trace.InsertStringIntoSpan("metadata-role", user.Role, span)
		trace.InsertStringIntoSpan("metadata-space-uid", user.SpaceUid, span)

		c.Next()
	}
}
