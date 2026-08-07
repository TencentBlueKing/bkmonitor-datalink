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
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"

	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

var RedisClient redis.UniversalClient

var rs *Instance

var errPipelineTest = errors.New("pipeline test error")

type pipelineErrorHook struct{}

func (pipelineErrorHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (pipelineErrorHook) AfterProcess(context.Context, redis.Cmder) error {
	return nil
}

func (pipelineErrorHook) BeforeProcessPipeline(
	ctx context.Context, _ []redis.Cmder,
) (context.Context, error) {
	return ctx, errPipelineTest
}

func (pipelineErrorHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

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

func newIsolatedRedis(t *testing.T) (*Instance, *miniredis.Miniredis) {
	t.Helper()

	server, err := miniredis.Run()
	assert.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
		server.Close()
	})
	return &Instance{Client: client, ctx: context.Background()}, server
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

func TestHSetManyWithCompareAndPublishWithoutPublish(t *testing.T) {
	instance, server := newIsolatedRedis(t)
	key := "result_table_detail"
	server.HSet(key, "exact", `{"a":1}`)
	server.HSet(key, "semantic", `{"a":1,"b":2}`)
	server.HSet(key, "changed", `{"a":1}`)

	before := server.CommandCount()
	changed, err := instance.HSetManyWithCompareAndPublish(key, map[string]string{
		"exact":    `{"a":1}`,
		"semantic": `{ "b": 2, "a": 1 }`,
		"changed":  `{"a":2}`,
		"missing":  `{"a":3}`,
	}, "", false)

	assert.NoError(t, err)
	assert.Equal(t, 2, changed)
	// 一次 HMGET，加上一条包含两个变化 field 的 HSET。
	assert.Equal(t, 2, server.CommandCount()-before)
	assert.Equal(t, `{"a":1}`, server.HGet(key, "exact"))
	// JSON 语义相同，不应用格式不同的新值覆盖旧值。
	assert.Equal(t, `{"a":1,"b":2}`, server.HGet(key, "semantic"))
	assert.Equal(t, `{"a":2}`, server.HGet(key, "changed"))
	assert.Equal(t, `{"a":3}`, server.HGet(key, "missing"))
}

func TestHSetManyWithCompareAndPublishPublishesChangedFields(t *testing.T) {
	instance, server := newIsolatedRedis(t)
	key := "result_table_detail"
	channelName := "result_table_detail_channel"
	server.HSet(key, "unchanged", `{"a":1}`)
	server.HSet(key, "changed", `{"a":1}`)

	pubsub := instance.Client.Subscribe(instance.ctx, channelName)
	t.Cleanup(func() { _ = pubsub.Close() })
	_, err := pubsub.Receive(instance.ctx)
	assert.NoError(t, err)

	before := server.CommandCount()
	changed, err := instance.HSetManyWithCompareAndPublish(key, map[string]string{
		"unchanged": `{"a":1}`,
		"missing":   `{"a":3}`,
		"changed":   `{"a":2}`,
	}, channelName, true)
	assert.NoError(t, err)
	assert.Equal(t, 2, changed)
	// 一次 HMGET、一条多 field HSET、两个逐 field PUBLISH。
	assert.Equal(t, 4, server.CommandCount()-before)

	messages := make([]string, 0, changed)
	for range changed {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		message, receiveErr := pubsub.ReceiveMessage(ctx)
		cancel()
		assert.NoError(t, receiveErr)
		messages = append(messages, message.Payload)
	}
	assert.Equal(t, []string{"changed", "missing"}, messages)
}

func TestHSetManyWithCompareAndPublishEmptyAndValidation(t *testing.T) {
	instance, _ := newIsolatedRedis(t)

	changed, err := instance.HSetManyWithCompareAndPublish("", nil, "", true)
	assert.NoError(t, err)
	assert.Zero(t, changed)

	_, err = instance.HSetManyWithCompareAndPublish("", map[string]string{"field": `{}`}, "", false)
	assert.Error(t, err)
	_, err = instance.HSetManyWithCompareAndPublish("key", map[string]string{"field": `{}`}, "", true)
	assert.Error(t, err)
	_, err = instance.HSetManyWithCompareAndPublish("key", map[string]string{"": `{}`}, "", false)
	assert.Error(t, err)
}

func TestHSetManyWithCompareAndPublishReturnsPipelineError(t *testing.T) {
	instance, server := newIsolatedRedis(t)
	instance.Client.AddHook(pipelineErrorHook{})

	changed, err := instance.HSetManyWithCompareAndPublish(
		"result_table_detail", map[string]string{"field": `{"a":1}`}, "", false,
	)
	assert.ErrorIs(t, err, errPipelineTest)
	assert.Zero(t, changed)
	assert.Empty(t, server.HGet("result_table_detail", "field"))
}

func TestHSetManyWithCompareAndPublishReturnsHMGetError(t *testing.T) {
	instance, server := newIsolatedRedis(t)
	err := server.Set("result_table_detail", "not-a-hash")
	assert.NoError(t, err)

	changed, err := instance.HSetManyWithCompareAndPublish(
		"result_table_detail", map[string]string{"field": `{"a":1}`}, "", false,
	)
	assert.Error(t, err)
	assert.Zero(t, changed)
}

func TestHSetManyWithCompareAndPublishHandlesLargeBatch(t *testing.T) {
	instance, server := newIsolatedRedis(t)
	values := make(map[string]string, 500)
	for i := 0; i < 500; i++ {
		values["field-"+strconv.Itoa(i)] = `{"a":1}`
	}

	before := server.CommandCount()
	changed, err := instance.HSetManyWithCompareAndPublish("result_table_detail", values, "", false)
	assert.NoError(t, err)
	assert.Equal(t, 500, changed)
	// 大批量读取和写入分别只有一条 HMGET/HSET。
	assert.Equal(t, 2, server.CommandCount()-before)
}

func TestHSetManyWithCompareAndPublishPreservesStorageSegmentOrder(t *testing.T) {
	instance, server := newIsolatedRedis(t)
	key := "result_table_detail"
	field := "2_bklog.test"
	oldValue := `{"storage_cluster_records":[{"storage_id":1},{"storage_id":2}]}`
	newValue := `{"storage_cluster_records":[{"storage_id":2},{"storage_id":1}]}`
	server.HSet(key, field, oldValue)

	changed, err := instance.HSetManyWithCompareAndPublish(
		key, map[string]string{field: newValue}, "", false,
	)

	assert.NoError(t, err)
	assert.Equal(t, 1, changed)
	assert.Equal(t, newValue, server.HGet(key, field))
}

func TestHScanFieldsReadsLargeHashWithoutReturningValues(t *testing.T) {
	instance, server := newIsolatedRedis(t)
	const key = "result_table_detail:scan"
	for index := 0; index < 501; index++ {
		server.HSet(key, "field-"+strconv.Itoa(index), `{"payload":"must-not-be-returned-as-field"}`)
	}

	seen := make(map[string]struct{}, 501)
	var cursor uint64
	for {
		fields, nextCursor, err := instance.HScanFields(key, cursor, 50)
		assert.NoError(t, err)
		for _, field := range fields {
			assert.NotContains(t, field, "payload")
			seen[field] = struct{}{}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	assert.Len(t, seen, 501)
}
