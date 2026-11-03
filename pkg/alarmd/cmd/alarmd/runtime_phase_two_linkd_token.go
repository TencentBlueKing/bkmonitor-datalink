// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdtoken"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// linkdTokenMode is a link read through the admin: a Console URL and no
// Basic pair, so every call carries the token the admin hands over.
func linkdTokenMode(cfg config.Config) bool {
	settings := cfg.PhaseTwo.Linkd
	return settings.ConsoleURL != "" && settings.Username == "" && settings.Password == ""
}

// openLinkdTokens is the token store a process calls the link with, nil
// outside token mode. It has a client of its own on the runtime store,
// because the startup discovery asks the link where it writes before the
// runtime client is opened: two connections at most, counted as the link's.
// The caller closes the client, which is nil when the store is.
func openLinkdTokens(cfg config.Config, hook *metric.RedisCallHook, now func() time.Time) (*linkdtoken.Store, redis.UniversalClient, error) {
	if !linkdTokenMode(cfg) {
		return nil, nil, nil
	}
	options := productionRedisOptions(cfg.RuntimeStoreRedis())
	options.PoolSize, options.MinIdleConns = 2, 0
	client := redis.NewUniversalClient(options)
	if hook != nil {
		client.AddHook(hook)
	}
	store, err := linkdtoken.New(client, cfg.Redis.StatePrefix, now)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return store, client, nil
}

// linkdTokenSource is the store as the link's client takes it: a nil store
// is no source, not a source that is nil.
func linkdTokenSource(store *linkdtoken.Store) openalerts.TokenSource {
	if store == nil {
		return nil
	}
	return store
}

// withLinkdToken makes a discovery carry the token, so the startup discovery
// and its retries call the link the way every later call does.
func withLinkdToken(discover discoverLinkdTarget, token openalerts.TokenSource) discoverLinkdTarget {
	if token == nil {
		return discover
	}
	return func(ctx context.Context, options openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		options.Token = token
		return discover(ctx, options)
	}
}
