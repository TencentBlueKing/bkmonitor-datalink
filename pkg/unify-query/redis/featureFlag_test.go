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
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/likexian/gokit/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestMain(m *testing.M) {
	// 订阅退出时仍可能写日志，全包只初始化一次，避免各测试重建全局日志器。
	log.InitTestLogger()
	os.Exit(m.Run())
}

func TestInitializeFeatureFlagsNotifiesAndPreservesExistingSnapshot(t *testing.T) {
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flags := NewFeatureFlagClient(client, "test")
	watch, err := flags.WatchFeatureFlags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		for range watch {
		}
	}()
	created, err := flags.InitializeFeatureFlags(ctx, []byte("{}"))
	if err != nil || !created {
		t.Fatalf("initialize snapshot: created %v, error: %v", created, err)
	}
	select {
	case <-watch:
	case <-time.After(time.Second):
		t.Fatal("expected initialization to notify readers")
	}
	created, err = flags.InitializeFeatureFlags(ctx, []byte(`{"another-flag":{}}`))
	if err != nil || created {
		t.Fatalf("initialization must preserve the existing snapshot: created %v, error: %v", created, err)
	}
	persisted, err := mr.Get(flags.GetFeatureFlagsPath())
	if err != nil || persisted != "{}" || mr.TTL(flags.GetFeatureFlagsPath()) != 0 {
		t.Fatalf("unexpected persisted snapshot %q, error: %v", persisted, err)
	}
}

func TestResetFeatureFlags(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := NewFeatureFlagClient(nil, "test").ResetFeatureFlags(ctx); err == nil {
		t.Fatal("expected an uninitialized client to fail")
	}
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	defer client.Close()
	flags := NewFeatureFlagClient(client, "test")
	key := flags.GetFeatureFlagsPath()
	if err := mr.Set(key, "{}"); err != nil {
		t.Fatal(err)
	}
	otherKey := key + ":other"
	if err := mr.Set(otherKey, "preserved"); err != nil {
		t.Fatal(err)
	}
	watch, err := flags.WatchFeatureFlags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		for range watch {
		}
	}()
	for i := 0; i < 2; i++ {
		if err := flags.ResetFeatureFlags(ctx); err != nil {
			t.Fatalf("reset attempt %d: %v", i, err)
		}
		if mr.Exists(key) {
			t.Fatal("feature flag snapshot was not deleted")
		}
		if value, err := mr.Get(otherKey); err != nil || value != "preserved" {
			t.Fatalf("reset changed another key: value %q, error %v", value, err)
		}
		select {
		case message := <-watch:
			if notification, ok := message.(*goRedis.Message); !ok || notification.Payload != "" {
				t.Fatalf("expected an empty reset notification, got %#v", message)
			}
		case <-time.After(time.Second):
			t.Fatal("expected reset to notify online readers")
		}
	}
	if err := mr.Set(key, "{}"); err != nil {
		t.Fatal(err)
	}
	publishAttempted := make(chan struct{}, 1)
	mr.Server().SetPreHook(func(peer *server.Peer, command string, _ ...string) bool {
		if command == "DEL" {
			peer.WriteError("ERR delete failed")
			return true
		}
		if command == "PUBLISH" {
			publishAttempted <- struct{}{}
		}
		return false
	})
	if err := flags.ResetFeatureFlags(ctx); err == nil || !mr.Exists(key) {
		t.Fatalf("failed deletion must preserve the snapshot, error %v", err)
	}
	select {
	case <-publishAttempted:
		t.Fatal("failed deletion must not publish a reset notification")
	default:
	}
}

// TestGetFeatureFlagsPath 测试获取特性开关路径
func TestGetFeatureFlagsPath(t *testing.T) {
	// 使用 miniredis 创建 mock client
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := goRedis.NewClient(&goRedis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")
	path := ffClient.GetFeatureFlagsPath()
	expected := "bkmonitorv3:unify-query:data:feature_flag"
	assert.Equal(t, expected, path)
}

// TestGetFeatureFlagsChannel 测试获取特性开关 channel 路径
func TestGetFeatureFlagsChannel(t *testing.T) {
	// 使用 miniredis 创建 mock client
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := goRedis.NewClient(&goRedis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")
	channel := ffClient.GetFeatureFlagsChannel()
	expected := "bkmonitorv3:unify-query:data:feature_flag:feature_flag_channel"
	assert.Equal(t, expected, channel)
}

// TestGetFeatureFlags 测试从 Redis 获取特性开关配置
func TestGetFeatureFlags(t *testing.T) {
	ctx := context.Background()

	// 测试用例 1: 正常获取配置
	t.Run("正常获取配置", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

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

		ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")
		key := ffClient.GetFeatureFlagsPath()
		err = client.Set(ctx, key, featureFlagConfig, 0).Err()
		assert.Nil(t, err)

		data, err := ffClient.GetFeatureFlags(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, data)
		assert.Equal(t, featureFlagConfig, string(data))
	})

	// 测试用例 2: 配置不存在（交由上层回退到 Consul）
	t.Run("配置不存在", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")
		data, err := ffClient.GetFeatureFlags(ctx)
		assert.Nil(t, err)
		if data != nil {
			t.Fatalf("expected missing Redis key to return nil, got %q", data)
		}
	})

	// 测试用例 3: Redis client 未初始化
	t.Run("Redis client 未初始化", func(t *testing.T) {
		ffClient := NewFeatureFlagClient(nil, "bkmonitorv3:unify-query")
		data, err := ffClient.GetFeatureFlags(ctx)
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "redis client is not initialized")
		assert.Equal(t, 0, len(data))
	})

	// 测试用例 4: 空配置
	t.Run("空配置", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")
		err = client.Set(ctx, ffClient.GetFeatureFlagsPath(), "{}", 0).Err()
		assert.Nil(t, err)
		data, err := ffClient.GetFeatureFlags(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, data)
		assert.Equal(t, "{}", string(data))
	})
}

// TestWatchFeatureFlags 测试监听特性开关变更
func TestWatchFeatureFlags(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 测试用例 1: 正常监听
	t.Run("正常监听", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")
		ch, err := ffClient.WatchFeatureFlags(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		// 发布消息
		channel := ffClient.GetFeatureFlagsChannel()
		err = client.Publish(ctx, channel, "test notification").Err()
		assert.Nil(t, err)

		// 等待消息
		select {
		case msg := <-ch:
			redisMsg, ok := msg.(*goRedis.Message)
			assert.True(t, ok)
			assert.Equal(t, "test notification", redisMsg.Payload)
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for message")
		}
	})

	// 测试用例 2: Redis client 未初始化
	t.Run("Redis client 未初始化", func(t *testing.T) {
		ffClient := NewFeatureFlagClient(nil, "bkmonitorv3:unify-query")
		ch, err := ffClient.WatchFeatureFlags(ctx)
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "redis client is not initialized")
		assert.True(t, ch == nil, "channel should be nil when error occurs")
	})

	// 测试用例 3: 上下文取消
	t.Run("上下文取消", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		cancelCtx, cancelFunc := context.WithCancel(context.Background())
		ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")
		ch, err := ffClient.WatchFeatureFlags(cancelCtx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		cancelFunc()
		time.Sleep(100 * time.Millisecond)
	})
}

func TestWatchFeatureFlagsUnresponsiveSubscription(t *testing.T) {
	for _, cancelHandshake := range []bool{false, true} {
		name := "timeout"
		if cancelHandshake {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			connection, server := net.Pipe()
			defer server.Close()
			subscribed := make(chan struct{})
			go func() {
				buffer := make([]byte, 4096)
				if _, err := server.Read(buffer); err == nil {
					close(subscribed)
				}
				// 接收订阅命令但始终不返回应答，模拟 Redis 连接存活却不响应。
				_, _ = io.Copy(io.Discard, server)
			}()
			client := goRedis.NewClient(&goRedis.Options{
				ReadTimeout: 10 * time.Millisecond,
				Dialer: func(context.Context, string, string) (net.Conn, error) {
					return connection, nil
				},
			})
			defer client.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type watchResult struct {
				watch <-chan any
				err   error
			}
			done := make(chan watchResult, 1)
			go func() {
				watch, err := NewFeatureFlagClient(client, "test").WatchFeatureFlags(ctx)
				done <- watchResult{watch: watch, err: err}
			}()
			select {
			case <-subscribed:
			case <-time.After(time.Second):
				t.Fatal("subscription command was not received")
			}
			wait := featureFlagSubscribeTimeout + time.Second
			if cancelHandshake {
				cancel()
				wait = time.Second
			}
			select {
			case result := <-done:
				if result.watch != nil || result.err == nil {
					t.Fatalf("expected subscription handshake to fail, got channel %v, error %v", result.watch, result.err)
				}
				if !cancelHandshake {
					var networkError net.Error
					if !errors.As(result.err, &networkError) || !networkError.Timeout() {
						t.Fatalf("expected subscription timeout, got %v", result.err)
					}
				}
			case <-time.After(wait):
				t.Fatal("subscription handshake did not exit")
			}
		})
	}
}

// TestSetFeatureFlags 测试设置特性开关配置
func TestSetFeatureFlags(t *testing.T) {
	ctx := context.Background()

	// 测试用例 1: Redis client 未初始化
	t.Run("Redis client 未初始化", func(t *testing.T) {
		ffClient := NewFeatureFlagClient(nil, "bkmonitorv3:unify-query")
		err := ffClient.SetFeatureFlags(ctx, []byte("{}"))
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "redis client is not initialized")
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

		ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")
		configData := []byte(`{"flag-1":{"variations":{"true":true,"false":false},"defaultRule":{"variation":"false"}}}`)
		err = ffClient.SetFeatureFlags(ctx, configData)
		assert.Nil(t, err)

		// 验证数据已设置到 Redis
		key := ffClient.GetFeatureFlagsPath()
		value, err := client.Get(ctx, key).Result()
		assert.Nil(t, err)
		assert.Equal(t, string(configData), value)

		// 验证 channel 已发布消息（通过订阅验证）
		channel := ffClient.GetFeatureFlagsChannel()
		pubsub := client.Subscribe(ctx, channel)
		defer pubsub.Close()

		// 再次发布以触发消息
		err = ffClient.SetFeatureFlags(ctx, configData)
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

		ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")
		err := ffClient.SetFeatureFlags(ctx, []byte("{}"))
		// 应该返回错误（连接失败）
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "failed to set feature flags to redis")
	})
}

// TestFeatureFlagsIntegration 集成测试：完整的特性开关流程
func TestFeatureFlagsIntegration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 使用 miniredis 创建真实的 Redis 实例
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := goRedis.NewClient(&goRedis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")

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

	// 设置配置
	err = ffClient.SetFeatureFlags(ctx, []byte(featureFlagConfig))
	assert.Nil(t, err)

	// 测试获取配置
	data, err := ffClient.GetFeatureFlags(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, featureFlagConfig, string(data))

	// 测试路径
	path := ffClient.GetFeatureFlagsPath()
	assert.Equal(t, "bkmonitorv3:unify-query:data:feature_flag", path)

	// 测试 channel 路径
	channel := ffClient.GetFeatureFlagsChannel()
	assert.Equal(t, "bkmonitorv3:unify-query:data:feature_flag:feature_flag_channel", channel)

	// 测试监听
	ch, err := ffClient.WatchFeatureFlags(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, ch)

	// 模拟配置变更通知
	go func() {
		time.Sleep(50 * time.Millisecond)
		err := client.Publish(ctx, channel, featureFlagConfig).Err()
		assert.Nil(t, err)
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
	ctx := context.Background()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := goRedis.NewClient(&goRedis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")

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

	// 设置配置
	err = ffClient.SetFeatureFlags(ctx, []byte(complexConfig))
	assert.Nil(t, err)

	data, err := ffClient.GetFeatureFlags(ctx)
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
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := goRedis.NewClient(&goRedis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	// 测试自定义 basePath
	ffClient := NewFeatureFlagClient(client, "custom:base:path")
	path := ffClient.GetFeatureFlagsPath()
	expected := "custom:base:path:data:feature_flag"
	assert.Equal(t, expected, path)

	// 测试 channel 路径也会相应变化
	channel := ffClient.GetFeatureFlagsChannel()
	expectedChannel := "custom:base:path:data:feature_flag:feature_flag_channel"
	assert.Equal(t, expectedChannel, channel)
}

// TestWatchFeatureFlagsMultipleNotifications 测试多次通知
func TestWatchFeatureFlagsMultipleNotifications(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := goRedis.NewClient(&goRedis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ffClient := NewFeatureFlagClient(client, "bkmonitorv3:unify-query")
	ch, err := ffClient.WatchFeatureFlags(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, ch)

	channel := ffClient.GetFeatureFlagsChannel()

	published := make(chan error, 3)
	// 连续发送多个通知；监听方允许将尚未消费的通知合并为一个全量刷新信号。
	go func() {
		defer close(published)
		published <- client.Publish(ctx, channel, "notification 1").Err()
		published <- client.Publish(ctx, channel, "notification 2").Err()
		published <- client.Publish(ctx, channel, "notification 3").Err()
	}()

	select {
	case msg := <-ch:
		redisMsg, ok := msg.(*goRedis.Message)
		assert.True(t, ok)
		assert.Contains(t, redisMsg.Payload, "notification")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for coalesced notification")
	}
	for publishErr := range published {
		assert.Nil(t, publishErr)
	}
}

// TestGetFeatureFlagsChannelFormat 测试 channel 格式
func TestGetFeatureFlagsChannelFormat(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := goRedis.NewClient(&goRedis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ffClient := NewFeatureFlagClient(client, "test:path")
	channel := ffClient.GetFeatureFlagsChannel()
	// 应该包含 key 和 channel 后缀
	assert.Contains(t, channel, ffClient.GetFeatureFlagsPath())
	assert.Contains(t, channel, featureFlagChannel)
	assert.Equal(t, "test:path:data:feature_flag:feature_flag_channel", channel)
}
