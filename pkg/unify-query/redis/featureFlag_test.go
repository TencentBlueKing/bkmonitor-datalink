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
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/likexian/gokit/assert"
	"github.com/prashantv/gostub"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestGetFeatureFlagsPath 测试获取特性开关路径
func TestGetFeatureFlagsPath(t *testing.T) {
	log.InitTestLogger()

	// 设置基础路径
	basePath = "bkmonitorv3:unify-query"
	dataPath = "data"

	path := GetFeatureFlagsPath()
	expected := "bkmonitorv3:unify-query:data:feature_flag"
	assert.Equal(t, expected, path)
}

// TestGetFeatureFlagsChannel 测试获取特性开关 channel 路径
func TestGetFeatureFlagsChannel(t *testing.T) {
	log.InitTestLogger()

	// 设置基础路径
	basePath = "bkmonitorv3:unify-query"
	dataPath = "data"

	channel := GetFeatureFlagsChannel()
	expected := "bkmonitorv3:unify-query:data:feature_flag:feature_flag_channel"
	assert.Equal(t, expected, channel)
}

// TestGetFeatureFlags 测试从 Redis 获取特性开关配置
func TestGetFeatureFlags(t *testing.T) {
	log.InitTestLogger()
	ctx := context.Background()

	// 测试用例 1: 正常获取配置
	t.Run("正常获取配置", func(t *testing.T) {
		featureFlagConfig := `{
			"test-flag": {
				"variations": {
					"true": true,
					"false": false
				},
				"defaultRule": {
					"variation": "false"
				}
			}
		}`

		stubs := gostub.StubFunc(&GetKVData, []byte(featureFlagConfig), nil)
		defer stubs.Reset()

		data, err := GetFeatureFlags(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, data)
		assert.Equal(t, featureFlagConfig, string(data))
	})

	// 测试用例 2: 配置不存在（Redis 返回空数据）
	t.Run("配置不存在", func(t *testing.T) {
		// GetKVData 在 key 不存在时返回 []byte("{}")
		stubs := gostub.StubFunc(&GetKVData, []byte("{}"), nil)
		defer stubs.Reset()

		data, err := GetFeatureFlags(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, data)
		assert.Equal(t, "{}", string(data))
	})

	// 测试用例 3: 获取配置失败
	t.Run("获取配置失败", func(t *testing.T) {
		stubs := gostub.StubFunc(&GetKVData, nil, errors.New("redis get error"))
		defer stubs.Reset()

		data, err := GetFeatureFlags(ctx)
		assert.NotNil(t, err)
		assert.Equal(t, "redis get error", err.Error())
		assert.Equal(t, 0, len(data), "data should be nil or empty when error occurs")
	})

	// 测试用例 4: 空配置
	t.Run("空配置", func(t *testing.T) {
		stubs := gostub.StubFunc(&GetKVData, []byte("{}"), nil)
		defer stubs.Reset()

		data, err := GetFeatureFlags(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, data)
		assert.Equal(t, "{}", string(data))
	})
}

// TestWatchFeatureFlags 测试监听特性开关变更
func TestWatchFeatureFlags(t *testing.T) {
	log.InitTestLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 测试用例 1: 正常监听
	t.Run("正常监听", func(t *testing.T) {
		testChan := make(chan any, 1)
		testChan <- &goRedis.Message{Payload: "test notification"}

		stubs := gostub.StubFunc(&WatchChange, testChan, nil)
		defer stubs.Reset()

		ch, err := WatchFeatureFlags(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		select {
		case msg := <-ch:
			redisMsg, ok := msg.(*goRedis.Message)
			assert.True(t, ok)
			assert.Equal(t, "test notification", redisMsg.Payload)
		case <-time.After(1 * time.Second):
			t.Error("timeout waiting for message")
		}
	})

	// 测试用例 2: 监听失败
	t.Run("监听失败", func(t *testing.T) {
		stubs := gostub.StubFunc(&WatchChange, nil, errors.New("watch error"))
		defer stubs.Reset()

		ch, err := WatchFeatureFlags(ctx)
		assert.NotNil(t, err)
		assert.Equal(t, "watch error", err.Error())
		assert.True(t, ch == nil, "channel should be nil when error occurs")
	})

	// 测试用例 3: 上下文取消
	t.Run("上下文取消", func(t *testing.T) {
		cancelCtx, cancelFunc := context.WithCancel(context.Background())
		testChan := make(chan any)

		stubs := gostub.StubFunc(&WatchChange, testChan, nil)
		defer stubs.Reset()

		ch, err := WatchFeatureFlags(cancelCtx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		cancelFunc()
		time.Sleep(100 * time.Millisecond)
	})
}

// TestSetFeatureFlags 测试设置特性开关配置
func TestSetFeatureFlags(t *testing.T) {
	log.InitTestLogger()
	ctx := context.Background()

	// 保存原始的 globalInstance
	originalInstance := globalInstance

	// 测试用例 1: Redis client 未初始化
	t.Run("Redis client 未初始化", func(t *testing.T) {
		// 设置 globalInstance 为 nil
		globalInstance = nil
		defer func() {
			globalInstance = originalInstance
		}()

		err := SetFeatureFlags(ctx, []byte("{}"))
		assert.NotNil(t, err)
		assert.Equal(t, "redis client is not initialized", err.Error())
	})

	// 测试用例 2: 正常设置配置
	t.Run("正常设置配置", func(t *testing.T) {
		// 使用 miniredis 创建真实的 Redis 实例
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		// 创建 Redis client
		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		// 创建 Instance
		mockInstance := &Instance{
			client: client,
		}
		globalInstance = mockInstance
		defer func() {
			globalInstance = originalInstance
		}()

		configData := []byte(`{"flag-1":{"variations":{"true":true,"false":false},"defaultRule":{"variation":"false"}}}`)
		err = SetFeatureFlags(ctx, configData)
		assert.Nil(t, err)

		// 验证数据已设置到 Redis
		key := GetFeatureFlagsPath()
		value, err := client.Get(ctx, key).Result()
		assert.Nil(t, err)
		assert.Equal(t, string(configData), value)

		// 验证 channel 已发布消息（通过订阅验证）
		pubsub := client.Subscribe(ctx, GetFeatureFlagsChannel())
		defer pubsub.Close()

		// 再次发布以触发消息
		err = SetFeatureFlags(ctx, configData)
		assert.Nil(t, err)

		// 等待消息
		msg, err := pubsub.ReceiveMessage(ctx)
		if err == nil {
			assert.Equal(t, string(configData), msg.Payload)
		}
	})

	// 测试用例 3: Set 操作失败（模拟网络错误）
	t.Run("Set 操作失败", func(t *testing.T) {
		// 创建一个会失败的 client（使用无效地址）
		client := goRedis.NewClient(&goRedis.Options{
			Addr: "127.0.0.1:1", // 无效地址
		})
		defer client.Close()

		mockInstance := &Instance{
			client: client,
		}
		globalInstance = mockInstance
		defer func() {
			globalInstance = originalInstance
		}()

		err := SetFeatureFlags(ctx, []byte("{}"))
		// 应该返回错误（连接失败）
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "failed to set feature flags to redis")
	})
}

// TestFeatureFlagsIntegration 集成测试：完整的特性开关流程
func TestFeatureFlagsIntegration(t *testing.T) {
	log.InitTestLogger()

	// 设置基础路径
	basePath = "bkmonitorv3:unify-query"
	dataPath = "data"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 测试配置
	featureFlagConfig := `{
		"enable-new-feature": {
			"variations": {
				"true": true,
				"false": false
			},
			"defaultRule": {
				"variation": "false"
			},
			"rules": [
				{
					"name": "enable for user 123",
					"variation": "true",
					"query": "user_id == \"123\""
				}
			]
		},
		"feature-version": {
			"variations": {
				"A": "version-a",
				"B": "version-b"
			},
			"defaultRule": {
				"variation": "A"
			}
		}
	}`

	// Mock GetKVData 返回配置
	stubs := gostub.StubFunc(&GetKVData, []byte(featureFlagConfig), nil)
	defer stubs.Reset()

	// 测试获取配置
	data, err := GetFeatureFlags(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, featureFlagConfig, string(data))

	// 测试路径
	path := GetFeatureFlagsPath()
	assert.Equal(t, "bkmonitorv3:unify-query:data:feature_flag", path)

	// 测试 channel 路径
	channel := GetFeatureFlagsChannel()
	assert.Equal(t, "bkmonitorv3:unify-query:data:feature_flag:feature_flag_channel", channel)

	// 测试监听（使用一个简单的 channel）
	watchChan := make(chan any, 1)
	watchStubs := gostub.StubFunc(&WatchChange, watchChan, nil)
	defer watchStubs.Reset()

	ch, err := WatchFeatureFlags(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, ch)

	// 模拟配置变更通知
	go func() {
		time.Sleep(50 * time.Millisecond)
		watchChan <- &goRedis.Message{Payload: featureFlagConfig}
	}()

	// 验证能接收到变更通知
	select {
	case msg := <-ch:
		redisMsg, ok := msg.(*goRedis.Message)
		assert.True(t, ok)
		assert.Equal(t, featureFlagConfig, redisMsg.Payload)
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for watch notification")
	}
}

// TestGetFeatureFlagsWithMultipleFlags 测试多个特性开关配置
func TestGetFeatureFlagsWithMultipleFlags(t *testing.T) {
	log.InitTestLogger()
	ctx := context.Background()

	complexConfig := `{
		"flag-1": {
			"variations": {
				"true": true,
				"false": false
			},
			"defaultRule": {
				"variation": "false"
			}
		},
		"flag-2": {
			"variations": {
				"A": "value-a",
				"B": "value-b",
				"C": "value-c"
			},
			"defaultRule": {
				"variation": "A"
			},
			"rules": [
				{
					"name": "rule for space",
					"variation": "B",
					"query": "spaceUid == \"bkcc__2\""
				}
			]
		},
		"flag-3": {
			"variations": {
				"0": 0,
				"10": 10,
				"100": 100
			},
			"defaultRule": {
				"variation": "0"
			}
		}
	}`

	stubs := gostub.StubFunc(&GetKVData, []byte(complexConfig), nil)
	defer stubs.Reset()

	data, err := GetFeatureFlags(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, data)

	// 验证配置包含所有特性开关
	configStr := string(data)
	assert.Contains(t, configStr, "flag-1")
	assert.Contains(t, configStr, "flag-2")
	assert.Contains(t, configStr, "flag-3")
}

// TestGetFeatureFlagsPathWithCustomBasePath 测试自定义基础路径
func TestGetFeatureFlagsPathWithCustomBasePath(t *testing.T) {
	log.InitTestLogger()

	// 保存原始值
	originalBasePath := basePath
	originalDataPath := dataPath

	// 设置自定义路径
	basePath = "custom:base:path"
	dataPath = "custom_data"

	path := GetFeatureFlagsPath()
	expected := "custom:base:path:custom_data:feature_flag"
	assert.Equal(t, expected, path)

	// 测试 channel 路径也会相应变化
	channel := GetFeatureFlagsChannel()
	expectedChannel := "custom:base:path:custom_data:feature_flag:feature_flag_channel"
	assert.Equal(t, expectedChannel, channel)

	// 恢复原始值
	basePath = originalBasePath
	dataPath = originalDataPath
}

// TestWatchFeatureFlagsMultipleNotifications 测试多次通知
func TestWatchFeatureFlagsMultipleNotifications(t *testing.T) {
	log.InitTestLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchChan := make(chan any, 3)
	stubs := gostub.StubFunc(&WatchChange, watchChan, nil)
	defer stubs.Reset()

	ch, err := WatchFeatureFlags(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, ch)

	// 发送多个通知
	go func() {
		watchChan <- &goRedis.Message{Payload: "notification 1"}
		time.Sleep(10 * time.Millisecond)
		watchChan <- &goRedis.Message{Payload: "notification 2"}
		time.Sleep(10 * time.Millisecond)
		watchChan <- &goRedis.Message{Payload: "notification 3"}
	}()

	// 验证能接收到所有通知
	received := 0
	for i := 0; i < 3; i++ {
		select {
		case msg := <-ch:
			redisMsg, ok := msg.(*goRedis.Message)
			assert.True(t, ok)
			assert.Contains(t, redisMsg.Payload, "notification")
			received++
		case <-time.After(2 * time.Second):
			t.Errorf("timeout waiting for notification %d", i+1)
		}
	}
	assert.Equal(t, 3, received)
}

// TestGetFeatureFlagsChannelFormat 测试 channel 格式
func TestGetFeatureFlagsChannelFormat(t *testing.T) {
	log.InitTestLogger()

	basePath = "test:path"
	dataPath = "data"

	channel := GetFeatureFlagsChannel()
	// 应该包含 key 和 channel 后缀
	assert.Contains(t, channel, GetFeatureFlagsPath())
	assert.Contains(t, channel, featureFlagChannel)
	assert.Equal(t, "test:path:data:feature_flag:feature_flag_channel", channel)
}
