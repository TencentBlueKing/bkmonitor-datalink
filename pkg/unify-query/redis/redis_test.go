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
	"testing"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRedisConnection 测试Redis连接功能
func TestRedisConnection(t *testing.T) {
	ctx := context.Background()

	// 配置Redis连接参数
	options := &goRedis.UniversalOptions{
		Addrs:    []string{"127.0.0.1:6379"},
		Password: "", // 如果没有密码，留空
		DB:       1,  // 默认数据库
	}

	// 设置Redis实例
	err := SetInstance(ctx, "test-service", options) //todo：gzl 初始化redis实例
	require.NoError(t, err, "设置Redis实例失败")

	// 等待连接就绪
	Wait()

	// 测试Ping命令
	t.Run("Ping测试", func(t *testing.T) {
		result, err := Ping(ctx)
		assert.NoError(t, err, "Ping命令执行失败")
		assert.Equal(t, "PONG", result, "Ping响应不正确")
	})

	// 测试字符串操作
	t.Run("字符串操作测试", func(t *testing.T) {
		testKey := "test:string:key"
		testValue := "test-string-value"
		expiration := 10 * time.Second

		// 设置值
		_, err := Set(ctx, testKey, testValue, expiration)
		assert.NoError(t, err, "设置字符串值失败")

		// 获取值
		value, err := Get(ctx, testKey)
		assert.NoError(t, err, "获取字符串值失败")
		assert.Equal(t, testValue, value, "获取的字符串值不匹配")

		//// 清理测试数据
		_, err = Client().Del(ctx, testKey).Result()
		assert.NoError(t, err, "清理测试数据失败")
	})

	// 测试哈希表操作
	t.Run("哈希表操作测试", func(t *testing.T) {
		testKey := "test:hash:key"
		testField := "test-field"
		testValue := "test-hash-value"

		// 设置哈希字段
		_, err := HSet(ctx, testKey, testField, testValue)
		assert.NoError(t, err, "设置哈希字段失败")

		// 获取哈希字段
		value, err := HGet(ctx, testKey, testField)
		assert.NoError(t, err, "获取哈希字段失败")
		assert.Equal(t, testValue, value, "获取的哈希字段值不匹配")

		// 获取所有哈希字段
		allValues, err := HGetAll(ctx, testKey)
		assert.NoError(t, err, "获取所有哈希字段失败")
		assert.Equal(t, testValue, allValues[testField], "获取的所有哈希字段值不匹配")

		// 清理测试数据
		_, err = Client().Del(ctx, testKey).Result()
		assert.NoError(t, err, "清理测试数据失败")
	})

	// 测试键查找功能
	t.Run("键查找测试", func(t *testing.T) {
		testKey := "test:keys:pattern:1"
		testValue := "test-value"

		// 设置测试键
		_, err := Set(ctx, testKey, testValue, 10*time.Second)
		assert.NoError(t, err, "设置测试键失败")

		// 查找匹配的键
		keys, err := Keys(ctx, "test:keys:pattern:*")
		assert.NoError(t, err, "查找键失败")
		assert.Contains(t, keys, testKey, "查找的键不包含测试键")

		// 清理测试数据
		_, err = Client().Del(ctx, testKey).Result()
		assert.NoError(t, err, "清理测试数据失败")
	})

	// 测试批量获取
	t.Run("批量获取测试", func(t *testing.T) {
		testKey1 := "test:mget:key1"
		testKey2 := "test:mget:key2"
		testValue1 := "value1"
		testValue2 := "value2"

		// 设置多个键
		_, err := Set(ctx, testKey1, testValue1, 10*time.Second)
		assert.NoError(t, err, "设置键1失败")
		_, err = Set(ctx, testKey2, testValue2, 10*time.Second)
		assert.NoError(t, err, "设置键2失败")

		// 批量获取值（注意：MGet函数当前实现只支持单个键，这里测试其基本功能）
		values, err := MGet(ctx, testKey1)
		assert.NoError(t, err, "批量获取失败")
		assert.Len(t, values, 1, "批量获取结果数量不正确")

		// 清理测试数据
		_, err = Client().Del(ctx, testKey1, testKey2).Result()
		assert.NoError(t, err, "清理测试数据失败")
	})

	// 测试集合操作
	t.Run("集合操作测试", func(t *testing.T) {
		testKey := "test:set:key"
		testMembers := []string{"member1", "member2", "member3"}

		// 添加集合成员
		for _, member := range testMembers {
			_, err := Client().SAdd(ctx, testKey, member).Result()
			assert.NoError(t, err, "添加集合成员失败")
		}

		// 获取集合所有成员
		members, err := SMembers(ctx, testKey)
		assert.NoError(t, err, "获取集合成员失败")
		assert.ElementsMatch(t, testMembers, members, "集合成员不匹配")

		// 清理测试数据
		_, err = Client().Del(ctx, testKey).Result()
		assert.NoError(t, err, "清理测试数据失败")
	})

	// 测试服务名称获取
	t.Run("服务名称测试", func(t *testing.T) {
		serviceName := ServiceName()
		assert.Equal(t, "test-service", serviceName, "服务名称不匹配")
	})

	// 关闭连接
	Close()
}

// TestRedisConnectionError 测试Redis连接错误处理
func TestRedisConnectionError(t *testing.T) {
	ctx := context.Background()

	// 测试无效地址
	t.Run("无效地址测试", func(t *testing.T) {
		invalidOptions := &goRedis.UniversalOptions{
			Addrs:    []string{"127.0.0.1:9999"}, // 无效端口
			Password: "",
			DB:       0,
		}

		err := SetInstance(ctx, "invalid-service", invalidOptions)
		// 注意：这里可能不会立即报错，因为Redis客户端有重试机制
		if err != nil {
			t.Logf("设置无效Redis实例预期错误: %v", err)
		}
	})

	// 测试未初始化时的操作
	t.Run("未初始化测试", func(t *testing.T) {
		// 先关闭现有连接
		Close()

		// 尝试在未初始化时执行操作
		_, err := Ping(ctx)
		assert.Error(t, err, "未初始化时应返回错误")
	})
}

// TestRedisReconnection 测试Redis重连功能
func TestRedisReconnection(t *testing.T) {
	ctx := context.Background()

	// 配置Redis连接
	options := &goRedis.UniversalOptions{
		Addrs:    []string{"127.0.0.1:6379"},
		Password: "",
		DB:       0,
	}

	// 第一次设置实例
	err := SetInstance(ctx, "reconnect-service", options)
	require.NoError(t, err)

	// 测试连接
	result, err := Ping(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "PONG", result)

	// 关闭连接
	Close()

	// 重新设置实例
	err = SetInstance(ctx, "reconnect-service", options)
	require.NoError(t, err)

	// 再次测试连接
	result, err = Ping(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "PONG", result)

	// 最终清理
	Close()
}
