// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// redisForCaller is client with every call made through it named caller
// (redisfailure.Callers), so the operation and failure counts by caller say
// which job a shared client's traffic is: the strategy source, the control
// plane, the Runtime State store and the others that ride one connection
// when the deployment points them at one Redis.
//
// It is a clone of the client sharing its connection pool, with one hook
// more; a call whose context already names a caller keeps that name. The
// bundle closes the client it opened and never a clone. The client's own
// hooks are all added when it is opened, before any clone is taken, so a
// clone's hook list and the client's never grow into each other. A client
// that is not a single-node or Sentinel client is returned as it is.
func redisForCaller(client redis.UniversalClient, caller string) redis.UniversalClient {
	base, ok := client.(*redis.Client)
	if !ok || base == nil {
		return client
	}
	clone := base.WithContext(base.Context())
	clone.AddHook(redisCallerHook(caller))
	return clone
}

// redisCallerHook names the caller of every call that names none.
type redisCallerHook string

func (hook redisCallerHook) name(ctx context.Context) context.Context {
	if redisfailure.Caller(ctx) != "" {
		return ctx
	}
	return redisfailure.WithCaller(ctx, string(hook))
}

func (hook redisCallerHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return hook.name(ctx), nil
}

func (redisCallerHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (hook redisCallerHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return hook.name(ctx), nil
}

func (redisCallerHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }
