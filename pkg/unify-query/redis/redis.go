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
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
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
	log.Debugf(ctx, "[redis] set instance %s, %+v", serviceName, options)
	globalInstance, err = NewRedisInstance(ctx, serviceName, options)
	if err != nil {
		log.Errorf(ctx, "new redis instance error: %s", err)
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
