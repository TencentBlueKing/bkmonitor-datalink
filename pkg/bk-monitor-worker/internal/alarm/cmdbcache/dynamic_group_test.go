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
	"encoding/json"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
)

var demoDynamicGroupListStr = `
{
	"1": {"bk_inst_ids": [1, 2, 3],"bk_obj_id": "host", "id": "1", "name": "demo1"},
	"2": {"bk_inst_ids": [4, 5, 6],"bk_obj_id": "host", "id": "2", "name": "demo2"}
}
`

func TestDynamicGroup(t *testing.T) {
	patch := gomonkey.ApplyFunc(getDynamicGroupList, func(ctx context.Context, bkTenantId string, bizID int) (map[string]map[string]interface{}, error) {
		var demoDynamicGroupList map[string]map[string]interface{}
		err := json.Unmarshal([]byte(demoDynamicGroupListStr), &demoDynamicGroupList)
		if err != nil {
			t.Errorf("Unmarshal failed, err: %v", err)
		}

		return demoDynamicGroupList, nil
	})

	defer patch.Reset()

	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetClient(rOpts)
	ctx := context.Background()

	t.Run("TestDynamicGroupCacheManager", func(t *testing.T) {
		m, err := NewDynamicGroupCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 10)
		if err != nil {
			t.Errorf("NewDynamicGroupCacheManager failed, err: %v", err)
		}
		assert.Equal(t, m.Type(), "dynamic_group")

		// 刷新缓存
		err = m.RefreshByBiz(ctx, 1)
		if err != nil {
			t.Errorf("RefreshByBiz failed, err: %v", err)
		}

		// 验证缓存存在
		key := m.GetCacheKey(DynamicGroupCacheKey)
		data, err := client.HGetAll(ctx, key).Result()
		if err != nil {
			t.Errorf("HGetAll failed, err: %v", err)
		}
		assert.Equal(t, len(data), 2)
		assert.EqualValues(t, data["1"], `{"bk_inst_ids":[1,2,3],"bk_obj_id":"host","id":"1","name":"demo1"}`)
		assert.EqualValues(t, data["2"], `{"bk_inst_ids":[4,5,6],"bk_obj_id":"host","id":"2","name":"demo2"}`)

		// 设置额外的缓存
		err = client.HSet(ctx, key, "3", `{"bk_inst_ids":[7,8,9],"bk_obj_id":"host","id":"3","name":"demo3"}`).Err()
		if err != nil {
			t.Errorf("HSet failed, err: %v", err)
		}

		// 清理缓存
		err = m.CleanGlobal(ctx)
		if err != nil {
			t.Errorf("CleanGlobal failed, err: %v", err)
		}

		// 验证缓存被清理
		result := client.HExists(ctx, key, "3")
		if err := result.Err(); err != nil {
			t.Errorf("HExists failed, err: %v", err)
		}
		assert.Equal(t, result.Val(), false)
	})
}
