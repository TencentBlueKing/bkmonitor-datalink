// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package query

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unsafe"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	utilsInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

func TestVmExpand(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	for name, c := range map[string]struct {
		queryRef metadata.QueryReference
		VmExpand *metadata.VmExpand
	}{
		"default-1": {
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								Field:       "container_cpu_usage_seconds",
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
							},
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vm_result_table_1",
								Field:       "container_cpu_usage_seconds",
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table_1"`,
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table"`,
							},
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vm_result_table_1",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
							},
						},
					},
				},
			},
			VmExpand: &metadata.VmExpand{
				MetricFilterCondition: map[string]string{
					"a": `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table" or __name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table_1"`,
					"b": `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table" or __name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
				},
				ResultTableList: []string{
					"vm_result_table",
					"vm_result_table_1",
				},
			},
		},
		"default-2": {
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"container_cpu_usage_seconds"},
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table"`,
							},
							{
								TableID:     "result_table.vm_1",
								VmRt:        "",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
							},
						},
					},
				},
			},
			VmExpand: &metadata.VmExpand{
				MetricFilterCondition: map[string]string{
					"a": `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
					"b": `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table"`,
				},
				ResultTableList: []string{
					"vm_result_table",
				},
			},
		},
		"default-3": {
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"container_cpu_usage_seconds"},
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
							},
						},
					},
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table_2",
								MetricNames: []string{"container_cpu_usage_seconds"},
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table_2"`,
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table"`,
							},
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vm_result_table_1",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
							},
						},
					},
				},
			},
			VmExpand: &metadata.VmExpand{
				MetricFilterCondition: map[string]string{
					"a": `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
					"b": `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table" or __name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
				},
				ResultTableList: []string{
					"vm_result_table",
					"vm_result_table_1",
				},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			VmExpand, err := ToVmExpand(ctx, c.queryRef)
			if err != nil {
				t.Errorf("ToVmExpand failed, error:%s", err)
				return
			}
			//
			for k, v := range VmExpand.MetricFilterCondition {
				or := " or "
				arr := strings.Split(v, or)
				sort.Strings(arr)
				VmExpand.MetricFilterCondition[k] = strings.Join(arr, or)
			}

			assert.Equal(t, c.VmExpand, VmExpand)
		})
	}
}

// black_list冲突测试
func TestConflict(t *testing.T) {
	prefix := "bkmonitorv3:influxdb"
	ctx := metadata.InitHashID(context.Background())
	mock.Init()
	influxdb.MockSpaceRouter(ctx)

	// 黑名单配置
	blackListInfo := utilsInfluxdb.BlackListInfo{
		ForbiddenVmCluster: [][]string{
			{"vm_cluster_1", "vm_cluster_2"},
			{"vm_cluster_3", "vm_cluster_4", "vm_cluster_5"},
		},
	}

	for name, c := range map[string]struct {
		queryRef metadata.QueryReference
		err      error
	}{
		//测试用例1 不匹配黑名单规则
		"default-1": { //[vmrt1,vmrt3,vmrt5]
			queryRef: metadata.QueryReference{
				"a": {
					{

						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vmrt_1",
								Field:       "container_cpu_usage_seconds",
								StorageName: "vm_cluster_1",
							},
							{
								TableID:     "result_table.vm_3",
								VmRt:        "vmrt_3",
								Field:       "container_cpu_usage_seconds",
								StorageName: "vm_cluster_3",
							},
							{
								TableID:     "result_table.vm_5",
								VmRt:        "vmrt_5",
								Field:       "container_cpu_usage_seconds",
								StorageName: "vm_cluster_5",
							},
						},
					}},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vmrt_1",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								StorageName: "vm_cluster_1",
							},
							{
								TableID:     "result_table.vm_3",
								VmRt:        "vmrt_3",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								StorageName: "vm_cluster_3",
							},
						},
					},
				},
			},
			err: nil,
		},
		//gzl：测试用例2 匹配黑名单规则
		"default-2": { //[vmrt1,vmrt3,vmrt4,vmrt5]
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vmrt_1",
								Field:       "container_cpu_usage_seconds",
								StorageName: "vm_cluster_1",
							},
							{
								TableID:     "result_table.vm_4",
								VmRt:        "vmrt_4",
								Field:       "container_cpu_usage_seconds",
								StorageName: "vm_cluster_4",
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm_3",
								VmRt:        "vmrt_3",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								StorageName: "vm_cluster_3",
							},
							{
								TableID:     "result_table.vm_4",
								VmRt:        "vmrt_4",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								StorageName: "vm_cluster_4",
							},
							{
								TableID:     "result_table.vm_5",
								VmRt:        "vmrt_5",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								StorageName: "vm_cluster_5",
							},
						},
					},
				},
			},
			err: fmt.Errorf("vm Cluster conflict"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			// 获取全局 Redis 客户端
			redisClient := redis.Client()
			if redisClient == nil {
				t.Fatalf("redis client is nil")
			}

			// 将黑名单信息序列化并写入 Redis
			blackListData, err := json.Marshal(blackListInfo)
			if err != nil {
				t.Fatalf("marshal blacklist info: %v", err)
			}
			blackListKey := fmt.Sprintf("%s:%s", prefix, utilsInfluxdb.BlackListKey)
			if _, err = redisClient.Set(ctx, blackListKey, string(blackListData), 0).Result(); err != nil {
				t.Fatalf("set blacklist to redis (key: %s): %v", blackListKey, err)
			}

			// 初始化 InfluxDB Router 并从 Redis 加载黑名单信息
			ir := influxdb.GetInfluxDBRouter()

			// 设置 router，避免 "influxdb router is none" 错误
			if err = setRouterField(ir, prefix, redisClient); err != nil {
				t.Fatalf("set router field: %v", err)
			}
			// 加载黑名单 key
			if err = ir.ReloadByKey(ctx, utilsInfluxdb.BlackListKey); err != nil {
				t.Fatalf("reload blacklist from redis: %v", err)
			}

			// 执行测试：vm cluster 黑名单规则检查
			_, err = ToVmExpand(ctx, c.queryRef)
			assert.Equal(t, c.err, err)
		})
	}
}

// setRouterField 使用反射和 unsafe 设置 Router 的私有 router 字段
// 仅用于测试代码，用于将 Redis 客户端注入到 Router 中
func setRouterField(ir *influxdb.Router, prefix string, client goRedis.UniversalClient) error {
	rv := reflect.ValueOf(ir).Elem()
	routerField := rv.FieldByName("router")
	lockField := rv.FieldByName("lock")

	if !routerField.IsValid() {
		return fmt.Errorf("router field not found in Router struct")
	}

	// 获取锁并加锁，确保线程安全
	unlock := acquireRouterLock(lockField)
	if unlock != nil {
		defer unlock()
	}

	// 创建真实的 router 实例，连接到本地 Redis
	realRouter := utilsInfluxdb.NewRouter(prefix, client)

	// 使用 unsafe 和 reflect.NewAt 设置私有接口字段 router
	// 对于私有字段，需要使用 UnsafeAddr() 获取地址，然后使用 reflect.NewAt 创建可设置的 Value
	routerFieldPtr := unsafe.Pointer(routerField.UnsafeAddr())
	// 使用 reflect.NewAt 创建一个指向该字段的 Value，这样就可以设置了
	routerFieldValue := reflect.NewAt(routerField.Type(), routerFieldPtr).Elem()
	routerFieldValue.Set(reflect.ValueOf(realRouter))

	return nil
}

// acquireRouterLock 尝试获取并锁定 router 的锁，返回解锁函数
// 如果锁不存在或无法获取，返回 nil（表示不需要解锁）
func acquireRouterLock(lockField reflect.Value) func() {
	if !lockField.IsValid() || lockField.IsNil() {
		return nil
	}

	lockValue := lockField.Elem()
	if !lockValue.IsValid() {
		return nil
	}

	lockMethod := lockValue.MethodByName("Lock")
	if !lockMethod.IsValid() {
		return nil
	}

	// 加锁
	lockMethod.Call(nil)
	// 返回解锁函数
	return func() {
		unlockMethod := lockValue.MethodByName("Unlock")
		if unlockMethod.IsValid() {
			unlockMethod.Call(nil)
		}
	}
}
