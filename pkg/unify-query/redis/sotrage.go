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

	redis "github.com/go-redis/redis/v8"
)

// NewRedisInstance : https://redis.uptrace.dev/guide/universal.html
// If the MasterName option is specified, a sentinel-backed FailoverClient is returned.
// if the number of Addrs is two or more, a ClusterClient is returned.
// Otherwise, a single-node Client is returned.
func NewRedisInstance(ctx context.Context, serviceName string, options *redis.UniversalOptions) (*Instance, error) {
	ctx, cancel := context.WithCancel(ctx)
	return &Instance{
		ctx:         ctx,
		cancel:      cancel,
		serviceName: serviceName,
		wg:          new(sync.WaitGroup),
		client:      redis.NewUniversalClient(options),
	}, nil
}
