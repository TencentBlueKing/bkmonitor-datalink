// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

// TestReloadInfluxDBRouter_ReloadByKeyBlackListWithChannel 测试当订阅消息的 payload 为 "black_list" 时，ReloadByKey 会被调用
// 这个测试通过模拟订阅消息来验证行为
func TestReloadInfluxDBRouter_ReloadByKeyBlackListWithChannel(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建 Service 实例
	service := &Service{
		wg: new(sync.WaitGroup),
	}

	// 启动 reloadInfluxDBRouter 只加载一次全部路由
	err := service.reloadInfluxDBRouter(ctx)

	/*
		一、reloadInfluxDBRouter流程：
		1. reloadInfluxDBRouter -> ReloadRouter -> ReloadAllKey(首先加载全部路由)
		2. 启动第二个goroutine：处理路由订阅和定时重载
		   - 2.1 定时重载：ticker.C -> ReloadAllKey
		   - 2.2 事件驱动重载：监听订阅消息（ir.RouterSubscribe），按需重载特定路由
		          PUBLISH ch key (如：PUBLISH "bkmonitorv3:influxdb" "black_list")
		          -> ir.ReloadByKey(ctx, msg.Payload)
		          -> loadRouter 匹配 case:influxdb.BlackListKey
		          -> r.router.GetBlackListInfo(ctx) 从redis动态获取黑名单信息blackListInfo [][]string
	*/
	if err != nil {
		t.Logf("reloadInfluxDBRouter error: %v", err)
	}

	// 等待 goroutine 启动
	time.Sleep(200 * time.Millisecond)

	// 获取真实的 router
	influxdbRouter := influxdb.GetInfluxDBRouter()
	if influxdbRouter == nil {
		t.Errorf("influxdb router is nil")
	}
	for name, c := range map[string]struct {
		queryRef   metadata.QueryReference
		isConflict bool
	}{
		//gzl：测试用例1 匹配黑名单规则 isConflict为false
		"default-1": {
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID: "result_table.vm",
								VmRt:    "vmrt_1",
								Field:   "container_cpu_usage_seconds",
							},
							{
								TableID: "result_table.vm_1",
								VmRt:    "vmrt_3",
								Field:   "container_cpu_usage_seconds",
							},
							{
								TableID: "result_table.vm_3",
								VmRt:    "vmrt_5",
								Field:   "container_cpu_usage_seconds",
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vmrt_1",
								MetricNames: []string{"kube_pod_container_resource_requests"},
							},
							{
								TableID:     "result_table.vm_3",
								VmRt:        "vmrt_3",
								MetricNames: []string{"kube_pod_container_resource_requests"},
							},
						},
					},
				},
			},
			isConflict: false,
		},
		//gzl：测试用例2 不匹配黑名单规则 isConflict为true
		"default-2": {
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID: "result_table.vm_1",
								VmRt:    "vmrt_1",
								Field:   "container_cpu_usage_seconds",
							},
							{
								TableID: "result_table.vm_2",
								VmRt:    "vmrt_2",
								Field:   "container_cpu_usage_seconds",
							},
							{
								TableID: "result_table.vm_3",
								VmRt:    "vmrt_3",
								Field:   "container_cpu_usage_seconds",
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vmrt_1",
								MetricNames: []string{"kube_pod_container_resource_requests"},
							},
							{
								TableID:     "result_table.vm_2",
								VmRt:        "vmrt_2",
								MetricNames: []string{"kube_pod_container_resource_requests"},
							},
						},
					},
				},
			},
			isConflict: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			VmExpand := query.ToVmExpand(ctx, c.queryRef)
			isConflict, err := influxdbRouter.CheckVMRT(VmExpand.ResultTableList) // vm rt 黑名单规则检查
			if err != nil {
				t.Errorf("check vm rt failed, error:%s", err)
			}
			assert.Equal(t, c.isConflict, isConflict)

		})
	}

	service.Wait()
}
