// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"context"
	"strconv"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
)

var DemoServiceInstances = []*AlarmServiceInstanceInfo{
	{
		BkBizId:           2,
		ID:                1,
		ServiceInstanceId: 1,
		Name:              "service1",
		BkModuleId:        6,
		BkHostId:          3,
		ServiceTemplateId: 1,
		ProcessInstances:  []byte(`[{"bk_host_id": 3, "bk_cloud_id": 0, "bk_host_innerip": "127.0.0.1"}]`),
		IP:                "127.0.0.3",
		BkCloudId:         0,
		TopoLinks: map[string][]map[string]interface{}{
			"module|6": {
				{"bk_inst_id": 6, "bk_inst_name": "测试模块", "bk_obj_id": "module", "bk_obj_name": "模块"},
				{"bk_inst_id": 3, "bk_inst_name": "测试集群", "bk_obj_id": "set", "bk_obj_name": "集群"},
				{"bk_inst_id": 2, "bk_inst_name": "测试节点", "bk_obj_id": "test", "bk_obj_name": "测试"},
				{"bk_inst_id": 2, "bk_inst_name": "蓝鲸", "bk_obj_id": "biz", "bk_obj_name": "业务"},
			},
		},
	},
}

func TestServiceInstanceCacheManager(t *testing.T) {
	// mock cmdb api
	cmdbPatches := gomonkey.ApplyFunc(getHostAndTopoByBiz, func(ctx context.Context, bkTenantId string, bizId int) ([]*AlarmHostInfo, *cmdb.SearchBizInstTopoData, error) {
		return DemoHosts, DemoTopoTree, nil
	})
	defer cmdbPatches.Reset()

	patches := gomonkey.ApplyFunc(getServiceInstances, func(ctx context.Context, bkTenantId string, bizID int) ([]*AlarmServiceInstanceInfo, error) {
		return DemoServiceInstances, nil
	})
	defer patches.Reset()

	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetClient(rOpts)
	ctx := context.Background()

	t.Run("TestServiceInstanceCacheManager", func(t *testing.T) {
		// 先准备主机缓存数据，用于测试服务实例缓存
		hostCacheManager, err := NewHostAndTopoCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
		if err != nil {
			t.Error(err)
			return
		}
		err = hostCacheManager.RefreshByBiz(ctx, 2)
		if err != nil {
			t.Error(err)
			return
		}

		cacheManager, err := NewServiceInstanceCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
		if err != nil {
			t.Error(err)
			return
		}

		// 刷新业务缓存
		err = cacheManager.RefreshByBiz(ctx, 2)
		if err != nil {
			t.Error(err)
			return
		}

		expectedServiceInstanceKeys := make([]string, 0, len(DemoServiceInstances))
		for _, instance := range DemoServiceInstances {
			expectedServiceInstanceKeys = append(expectedServiceInstanceKeys, strconv.Itoa(instance.ID))
		}

		// 判断是否存在所有的缓存键
		actualServiceInstanceKeys, err := client.HKeys(ctx, cacheManager.GetCacheKey(serviceInstanceCacheKey)).Result()
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, expectedServiceInstanceKeys, actualServiceInstanceKeys)

		result := client.HGetAll(ctx, cacheManager.GetCacheKey(serviceInstanceCacheKey)).Val()
		t.Log(result["1"])

		assert.EqualValues(t, client.HGet(ctx, cacheManager.GetCacheKey(hostToServiceInstanceCacheKey), "3").Val(), "[1]")
	})
}
