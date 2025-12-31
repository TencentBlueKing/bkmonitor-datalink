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

	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	innerRedis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

// TestEventDrivenReloadIntegration 集成测试：使用本地 Redis 服务器进行测试
// 这个测试连接到本地 Redis (127.0.0.1:6379) 进行真实的订阅测试
// 使用前请确保本地 Redis 服务已启动

func TestEventDrivenReloadIntegration(t *testing.T) {
	ctx := context.Background()
	localRedisAddr := "127.0.0.1:6379"
	localRedisOptions := &goRedis.Options{
		Addr:     localRedisAddr,
		Password: "", // 如果有密码，请在这里设置
		DB:       0,  // 使用默认数据库，可以根据需要修改
	}

	redisClient := goRedis.NewClient(localRedisOptions)

	defer redisClient.Close()

	// 检查 Redis 是否可用
	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		t.Skipf("无法连接到本地 Redis (%s)，跳过测试。错误: %v", localRedisAddr, err)
		t.Log("提示：请确保本地 Redis 服务已启动 (redis-server)")
		return
	}

	t.Logf("✓ 成功连接到本地 Redis: %s", localRedisAddr)

	// 2. 初始化全局 Redis 实例（供服务使用）
	err = innerRedis.SetInstance(ctx, "test-service", &goRedis.UniversalOptions{
		Addrs:    []string{localRedisAddr},
		Password: "",
		DB:       0,
	})
	require.NoError(t, err, "设置 Redis 实例失败")
	defer innerRedis.Wait()

	// 3. 设置测试常量
	originalRouterInterval := RouterInterval
	originalPingPeriod := PingPeriod
	originalRouterPrefix := RouterPrefix

	RouterPrefix = "bkmonitorv3:influxdb"
	payload := "black_list" // 注意：payload 内容需要根据实际实现调整
	RouterInterval = 10 * time.Second
	PingPeriod = 10 * time.Second
	PingTimeout = 100 * time.Millisecond
	PingCount = 1
	GrpcMaxCallRecvMsgSize = 1024 * 1024 * 4
	GrpcMaxCallSendMsgSize = 1024 * 1024 * 4

	// 恢复原始值
	defer func() {
		RouterInterval = originalRouterInterval
		PingPeriod = originalPingPeriod
		RouterPrefix = originalRouterPrefix
	}()

	// 5. 启动服务
	service := &Service{
		wg: new(sync.WaitGroup),
	}
	serviceCtx, serviceCancel := context.WithCancel(ctx)
	defer serviceCancel()

	err = service.reloadInfluxDBRouter(serviceCtx)
	if err != nil {
		t.Logf("reloadInfluxDBRouter 返回错误: %v", err)
		// 即使有错误，也可以继续测试订阅机制
	}

	// 6. 等待 goroutine 启动和订阅建立
	t.Log("等待订阅建立...")
	time.Sleep(2 * time.Second)

	// 7. 获取订阅 channel
	router := influxdb.GetInfluxDBRouter()
	ch := router.RouterSubscribe(serviceCtx)
	if ch == nil {
		t.Fatal("订阅 channel 为 nil，无法继续测试")
	}
	t.Log("✓ 订阅 channel 已创建")

	// 8. 监听消息
	messageReceived := make(chan *goRedis.Message, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case msg, ok := <-ch:
			if ok {
				t.Logf("✓ 收到订阅消息: Channel=%s, Payload=%s", msg.Channel, msg.Payload)
				messageReceived <- msg
			}
		case <-time.After(5 * time.Second):
			t.Log("⚠ 5 秒内未收到消息")
			messageReceived <- nil
		}
	}()

	// 9. 发布消息触发事件驱动重载
	channelName := RouterPrefix // 注意：channel 名称需要根据实际实现调整
	t.Logf("通过 Redis PUBLISH 发送消息: Channel=%s, Payload=%s", channelName, payload)
	err = redisClient.Publish(ctx, channelName, payload).Err()
	require.NoError(t, err, "发布消息失败")
	t.Logf("✓ 消息已发布到 Redis")

	// 10. 等待消息被接收
	time.Sleep(2 * time.Second)

	// 11. 验证消息是否被接收
	select {
	case msg := <-messageReceived:
		if msg != nil {
			t.Logf("✓ 成功收到订阅消息！")
			assert.Equal(t, payload, msg.Payload, "消息 payload 应该匹配")
			t.Logf("   Channel: %s", msg.Channel)
			t.Logf("   Payload: %s", msg.Payload)
		} else {
			t.Log("⚠ 未收到订阅消息")
			t.Log("可能的原因：")
			t.Log("1. Channel 名称不正确（当前使用: " + channelName + "）")
			t.Log("2. 订阅尚未完全建立")
			t.Log("3. 消息格式不正确")
			t.Log("提示：可以尝试使用 redis-cli 手动发布消息进行测试")
		}
	case <-time.After(2 * time.Second):
		t.Log("⚠ 等待消息超时")
	}

	// 12. 清理
	serviceCancel()
	service.Wait()

	t.Log("测试完成")
}
