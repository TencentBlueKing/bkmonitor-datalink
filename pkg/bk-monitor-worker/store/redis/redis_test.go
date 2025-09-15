// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"

	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

var RedisClient redis.UniversalClient

var rs *Instance

func newRedisClient() {
	// 启动 server
	s, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	ctx := context.TODO()

	// 构建 client
	if RedisClient == nil {
		port, _ := strconv.Atoi(s.Port())
		RedisClient, err = redisUtils.NewRedisClient(
			ctx,
			&redisUtils.Option{
				Mode: redisUtils.StandAlone,
				Host: s.Host(),
				Port: port,
				Db:   0,
			},
		)
	}
	// 组装 rs
	if rs == nil {
		rs = &Instance{
			Client: RedisClient,
			ctx:    ctx,
		}
	}
}

func TestPutGetDeleteData(t *testing.T) {
	newRedisClient()
	key, val := "bkm-put-test", "test case"
	err := rs.Put(key, val, 0)
	assert.Nil(t, err)

	// 获取数据
	byteData, _ := rs.Get(key)
	assert.Equal(t, string(byteData), val)

	// 删除数据
	err = rs.Delete(key)
	assert.Nil(t, err)

	// 校验数据不存在
	byteData, err = rs.Get(key)
	assert.Equal(t, string(byteData), "")
}

func TestHsetHgetData(t *testing.T) {
	newRedisClient()
	key, field, val := "bkm-put-test", "test", "test case"
	err := rs.HSet(key, field, val)
	assert.Nil(t, err)

	outputVal := rs.HGet(key, field)
	assert.Equal(t, outputVal, val)

	outputVal = rs.HGet(key, "not_found_field")
	assert.Empty(t, outputVal)
}
