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
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
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

// SetNx sets key to hold string value if key does not exist.
// - returns true if the key was set.
// - returns false if the key was not set.(already exists)
var SetNX = func(ctx context.Context, key, val string, expiration time.Duration) (bool, error) {
	if key == "" {
		key = globalInstance.serviceName
	}
	log.Debugf(ctx, "[redis] setnx %s", key)
	res := globalInstance.client.SetNX(ctx, key, val, expiration)
	return res.Result()
}

var Delete = func(ctx context.Context, keys ...string) (int64, error) {
	log.Debugf(ctx, "[redis] del %s", keys)
	res := globalInstance.client.Del(ctx, keys...)
	return res.Result()
}

var TxPipeline = func(ctx context.Context) goRedis.Pipeliner {
	log.Debugf(ctx, "[redis] txpipeline")
	return globalInstance.client.TxPipeline()
}

var Get = func(ctx context.Context, key string) (string, error) {
	if key == "" {
		key = globalInstance.serviceName
	}
	log.Debugf(ctx, "[redis] get %s", key)
	res := globalInstance.client.Get(ctx, key)
	return res.Result()
}

var IsNil = func(err error) bool {
	return errors.Is(err, goRedis.Nil)
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

var Subscribe = func(ctx context.Context, channels ...string) (ch <-chan *goRedis.Message, close func() error) {
	log.Debugf(ctx, "[redis] subscribe %s", channels)
	p := globalInstance.client.Subscribe(ctx, channels...)

	close = func() error {
		return p.Close()
	}
	return p.Channel(), close
}

var ExecLua = func(ctx context.Context, script *goRedis.Script, keys []string, args ...any) (any, error) {
	log.Debugf(ctx, "[redis] exec lua %s", script)
	res := script.Run(ctx, globalInstance.client, keys, args...)
	return res.Result()
}

var Expire = func(ctx context.Context, key string, expiration time.Duration) (bool, error) {
	log.Debugf(ctx, "[redis] expire %s", key)
	res := globalInstance.client.Expire(ctx, key, expiration)
	return res.Result()
}

var Publish = func(ctx context.Context, channel string, message any) (int64, error) {
	log.Debugf(ctx, "[redis] publish %s", channel)
	res := globalInstance.client.Publish(ctx, channel, message)
	return res.Result()
}
