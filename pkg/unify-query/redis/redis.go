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
	"fmt"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

var (
	basePath = "bkmonitorv3:unify-query"
	dataPath = "data"
)

var globalInstance *Instance

var lock *sync.RWMutex

func init() {
	lock = new(sync.RWMutex)
}

func Wait() {
	if globalInstance != nil {
		globalInstance.Wait()
	}
}

func Close() {
	if globalInstance != nil {
		globalInstance.Close()
	}
}

func Client() goRedis.UniversalClient {
	var client goRedis.UniversalClient
	if globalInstance != nil {
		client = globalInstance.client
	}
	return client
}

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

var ServiceName = func() string {
	return globalInstance.serviceName
}

var Ping = func(ctx context.Context) (string, error) {
	log.Debugf(ctx, "[redis] ping")
	res := globalInstance.client.Ping(ctx)
	return res.Result()
}

var Keys = func(ctx context.Context, pattern string) ([]string, error) {
	log.Debugf(ctx, "[redis] keys")
	keys := globalInstance.client.Keys(ctx, pattern)
	return keys.Result()
}

var HGetAll = func(ctx context.Context, key string) (map[string]string, error) {
	log.Debugf(ctx, "[redis] hgetall %s", key)
	res := globalInstance.client.HGetAll(ctx, key)
	return res.Result()
}

var HGet = func(ctx context.Context, key string, field string) (string, error) {
	log.Debugf(ctx, "[redis] hget %s, %s", key, field)
	res := globalInstance.client.HGet(ctx, key, field)
	return res.Result()
}

var HSet = func(ctx context.Context, key, field, val string) (int64, error) {
	log.Debugf(ctx, "[redis] hset %s, %s", key, field)
	res := globalInstance.client.HSet(ctx, key, field, val)
	return res.Result()
}

var Set = func(ctx context.Context, key, val string, expiration time.Duration) (string, error) {
	if key == "" {
		key = globalInstance.serviceName
	}
	log.Debugf(ctx, "[redis] set %s", key)
	res := globalInstance.client.Set(ctx, key, val, expiration)
	return res.Result()
}

var Get = func(ctx context.Context, key string) (string, error) {
	if key == "" {
		key = globalInstance.serviceName
	}
	log.Debugf(ctx, "[redis] get %s", key)
	res := globalInstance.client.Get(ctx, key)
	return res.Result()
}

var MGet = func(ctx context.Context, key string) ([]any, error) {
	if key == "" {
		key = globalInstance.serviceName
	}
	log.Debugf(ctx, "[redis] mget %s", key)
	res := globalInstance.client.MGet(ctx, key)
	return res.Result()
}

var SMembers = func(ctx context.Context, key string) ([]string, error) {
	log.Debugf(ctx, "[redis] smembers %s", key)
	res := globalInstance.client.SMembers(ctx, key)
	return res.Result()
}

var Subscribe = func(ctx context.Context, channels ...string) <-chan *goRedis.Message {
	log.Debugf(ctx, "[redis] subscribe %s", channels)
	p := globalInstance.client.Subscribe(ctx, channels...)
	return p.Channel()
}

// WatchChange 监听指定 channel，监听触发时，channel将会传出信息（用于特性开关等场景）
var WatchChange = func(ctx context.Context, channel string) (<-chan any, error) {
	if globalInstance == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}

	msgChan := Subscribe(ctx, channel)

	// 转换为通用的 channel
	resultChan := make(chan any)
	go func() {
		defer close(resultChan)
		for {
			select {
			case <-ctx.Done():
				log.Debugf(ctx, "[redis] watch context cancelled")
				return
			case msg, ok := <-msgChan:
				if !ok {
					log.Debugf(ctx, "[redis] channel closed")
					return
				}
				// 当收到消息时，通知配置变更
				log.Debugf(ctx, "[redis] received change notification: %s", msg.Payload)
				// 使用非阻塞发送，如果接收者已停止，直接丢弃消息
				select {
				case resultChan <- msg:
				case <-ctx.Done():
					return
				default:
					// 如果 resultChan 已满或接收者已停止，记录日志但不阻塞
					log.Debugf(ctx, "[redis] result channel is full or receiver stopped, dropping message")
				}
			}
		}
	}()

	return resultChan, nil
}

// GetKVData 通过 key 路径获取 value（用于特性开关等场景）
var GetKVData = func(ctx context.Context, key string) ([]byte, error) {
	if globalInstance == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}

	data, err := globalInstance.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, goRedis.Nil) {
			// 若Key 不存在，返回空数据
			return []byte("{}"), nil
		}
		return nil, fmt.Errorf("failed to get data from redis: %w", err)
	}

	return []byte(data), nil
}
