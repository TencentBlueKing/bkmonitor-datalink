// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/redis/go-redis/v9"
)

// RedisProgress 读取来源采集进度；写入由 RedisReceipts.Complete 与完成记录一起提交。
type RedisProgress struct {
	client redis.UniversalClient
	prefix string
}

// NewRedisProgress 使用共享连接与部署命名空间。
func NewRedisProgress(client redis.UniversalClient, prefix string) *RedisProgress {
	return &RedisProgress{client: client, prefix: prefix}
}

func (p *RedisProgress) key(tenant, source string) string {
	return p.prefix + ":collector:progress:" + url.QueryEscape(tenant) + ":" + url.QueryEscape(source)
}

// Load 返回持久化进度；首次执行返回 nil，存储失败不能当作首次执行。
func (p *RedisProgress) Load(ctx context.Context, tenant, source string) (map[string]any, error) {
	raw, err := p.client.Get(ctx, p.key(tenant, source)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load source progress: %w", err)
	}
	var state map[string]any
	if err = json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode source progress: %w", err)
	}
	return state, nil
}
