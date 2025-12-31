// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// gzl Package redis 提供统一查询模块的Redis客户端封装
// gzl 主要功能包括：
// gzl - 全局Redis实例管理
// gzl - 常用Redis操作封装（Get/Set/HGet/HSet等）
// gzl - 连接管理和健康检查
// gzl - 订阅发布模式支持
package redis

import (
	"context"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// gzl globalInstance 全局Redis实例，采用单例模式管理
var globalInstance *Instance

// gzl lock 读写锁，用于保证全局实例的线程安全
var lock *sync.RWMutex

// gzl init 初始化函数，创建读写锁实例
func init() {
	lock = new(sync.RWMutex)
}

// gzl Wait 等待Redis连接就绪
// gzl 如果全局实例存在，则调用实例的Wait方法等待连接建立完成
func Wait() {
	if globalInstance != nil {
		globalInstance.Wait()
	}
}

// gzl Close 关闭Redis连接
// gzl 如果全局实例存在，则调用实例的Close方法释放连接资源
func Close() {
	if globalInstance != nil {
		globalInstance.Close()
	}
}

// gzl Client 获取Redis客户端实例
// gzl 返回全局Redis客户端，如果实例不存在则返回nil
func Client() goRedis.UniversalClient {
	var client goRedis.UniversalClient
	if globalInstance != nil {
		client = globalInstance.client
	}
	return client
}

// gzl SetInstance 设置全局Redis实例
// gzl 使用读写锁保证线程安全，创建新的Redis实例并替换全局实例
// gzl 参数：
// gzl - ctx: 上下文对象
// gzl - serviceName: 服务名称，用于标识Redis实例
// gzl - options: Redis连接配置选项
// gzl 返回值：错误信息，如果创建失败则返回错误
func SetInstance(ctx context.Context, serviceName string, options *goRedis.UniversalOptions) error {
	lock.Lock()
	defer lock.Unlock()
	var err error
	globalInstance, err = NewRedisInstance(ctx, serviceName, options)
	if err != nil {
		err = metadata.NewMessage(
			metadata.MsgQueryRedis,
			"创建Redis实例失败",
		).Error(ctx, err)
	}
	return err
}

// gzl ServiceName 获取当前Redis实例的服务名称
// gzl 返回全局实例的服务名称标识
var ServiceName = func() string {
	return globalInstance.serviceName
}

// gzl Ping Redis健康检查
// gzl 发送PING命令检查Redis服务是否可用
// gzl 返回值：PONG字符串和错误信息
var Ping = func(ctx context.Context) (string, error) {
	log.Debugf(ctx, "[redis] ping")
	res := globalInstance.client.Ping(ctx)
	return res.Result()
}

// gzl Keys 根据模式匹配查找键名
// gzl 使用通配符模式查找匹配的键名列表
// gzl 参数：pattern - 匹配模式（支持*等通配符）
// gzl 返回值：匹配的键名列表和错误信息
var Keys = func(ctx context.Context, pattern string) ([]string, error) {
	log.Debugf(ctx, "[redis] keys")
	keys := globalInstance.client.Keys(ctx, pattern)
	return keys.Result()
}

// gzl HGetAll 获取哈希表所有字段和值 todo：用此函数加载redis缓存key：value（json）
// gzl 返回指定哈希键的所有字段值对
// gzl 参数：key - 哈希表键名
// gzl 返回值：字段值映射表和错误信息
var HGetAll = func(ctx context.Context, key string) (map[string]string, error) {
	log.Debugf(ctx, "[redis] hgetall %s", key)
	res := globalInstance.client.HGetAll(ctx, key)
	return res.Result()
}

// gzl HGet 获取哈希表指定字段的值
// gzl 从哈希表中获取指定字段的值
// gzl 参数：
// gzl - key: 哈希表键名
// gzl - field: 字段名
// gzl 返回值：字段值和错误信息
var HGet = func(ctx context.Context, key string, field string) (string, error) {
	log.Debugf(ctx, "[redis] hget %s, %s", key, field)
	res := globalInstance.client.HGet(ctx, key, field)
	return res.Result()
}

// gzl HSet 设置哈希表字段值
// gzl 在哈希表中设置指定字段的值
// gzl 参数：
// gzl - key: 哈希表键名
// gzl - field: 字段名
// gzl - val: 字段值
// gzl 返回值：操作结果和错误信息
var HSet = func(ctx context.Context, key, field, val string) (int64, error) {
	log.Debugf(ctx, "[redis] hset %s, %s", key, field)
	res := globalInstance.client.HSet(ctx, key, field, val)
	return res.Result()
}

// gzl Set 设置字符串键值对
// gzl 设置指定键的字符串值，支持设置过期时间
// gzl 参数：
// gzl - key: 键名（如果为空则使用服务名称作为默认键）
// gzl - val: 值
// gzl - expiration: 过期时间
// gzl 返回值：操作结果和错误信息
var Set = func(ctx context.Context, key, val string, expiration time.Duration) (string, error) {
	if key == "" {
		key = globalInstance.serviceName
	}
	log.Debugf(ctx, "[redis] set %s", key)
	res := globalInstance.client.Set(ctx, key, val, expiration)
	return res.Result()
}

// gzl Get 获取字符串值
// gzl 获取指定键的字符串值
// gzl 参数：key - 键名（如果为空则使用服务名称作为默认键）
// gzl 返回值：键值和错误信息
var Get = func(ctx context.Context, key string) (string, error) {
	if key == "" {
		key = globalInstance.serviceName
	}
	log.Debugf(ctx, "[redis] get %s", key)
	res := globalInstance.client.Get(ctx, key)
	return res.Result()
}

// gzl MGet 批量获取多个键的值
// gzl 一次性获取多个键对应的值
// gzl 参数：key - 键名（如果为空则使用服务名称作为默认键）
// gzl 返回值：值列表和错误信息
var MGet = func(ctx context.Context, key string) ([]any, error) {
	if key == "" {
		key = globalInstance.serviceName
	}
	log.Debugf(ctx, "[redis] mget %s", key)
	res := globalInstance.client.MGet(ctx, key)
	return res.Result()
}

// gzl SMembers 获取集合所有成员
// gzl 返回指定集合键的所有成员
// gzl 参数：key - 集合键名
// gzl 返回值：成员列表和错误信息
var SMembers = func(ctx context.Context, key string) ([]string, error) {
	log.Debugf(ctx, "[redis] smembers %s", key)
	res := globalInstance.client.SMembers(ctx, key)
	return res.Result()
}

// gzl Subscribe 订阅Redis频道
// gzl 订阅指定的Redis频道，返回消息接收通道
// gzl 参数：channels - 要订阅的频道列表
// gzl 返回值：消息接收通道，用于接收订阅消息
var Subscribe = func(ctx context.Context, channels ...string) <-chan *goRedis.Message {
	log.Debugf(ctx, "[redis] subscribe %s", channels)
	p := globalInstance.client.Subscribe(ctx, channels...)
	return p.Channel()
}
