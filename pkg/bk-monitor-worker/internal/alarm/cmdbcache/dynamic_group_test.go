// MIT License

// Copyright (c) 2021~2024 腾讯蓝鲸

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cmdbcache

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
)

var demoDynamicGroupListStr = `
{
	"1": {"bk_inst_ids": [1, 2, 3],"bk_obj_id": "host", "id": "1", "name": "demo1"},
	"2": {"bk_inst_ids": [4, 5, 6],"bk_obj_id": "host", "id": "2", "name": "demo2"}
}
`

func TestDynamicGroup(t *testing.T) {
	patch := gomonkey.ApplyFunc(getDynamicGroupList, func(ctx context.Context, bizID int) (map[string]map[string]interface{}, error) {
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
		m, err := NewDynamicGroupCacheManager("test", rOpts, 10)
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
