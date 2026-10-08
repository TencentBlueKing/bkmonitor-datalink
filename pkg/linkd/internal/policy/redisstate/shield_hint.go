// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
)

// ShieldHint 是可丢失的主告警终态提示；不携带快照、凭据、版本或可直接执行的解除命令。
type ShieldHint struct {
	// TenantID 与 Pub/Sub channel 的租户哈希必须相同。
	TenantID string `json:"bk_tenant_id"`
	// MainAlertID 只用于查找当前依赖子告警，执行时仍重新判断真实状态。
	MainAlertID string `json:"main_alert_id"`
}

// Validate 拒绝不明确租户和超出领域上限的主身份。
func (h ShieldHint) Validate() error {
	if domain.ValidateIdentityPart("tenant", h.TenantID, 64) != nil || h.MainAlertID == "" || len(h.MainAlertID) > domain.EntityIDMaxBytes {
		return ErrState
	}
	return nil
}

func (s *Store) shieldHintChannel(tenant string) string { return s.base(tenant) + ":shield-hint" }

// PublishShieldHint 最多等待一秒；返回订阅连接数量，不证明子告警已检查或解除。
// Pub/Sub 不保存重试队列，写入失败/没有订阅者均依赖持久化定时扫描。
func (s *Store) PublishShieldHint(ctx context.Context, tenant, main string) (int64, error) {
	hint := ShieldHint{TenantID: tenant, MainAlertID: main}
	if ctx == nil || hint.Validate() != nil {
		return 0, ErrState
	}
	raw, err := json.Marshal(hint)
	if err != nil {
		return 0, err
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.client.Publish(call, s.shieldHintChannel(tenant), raw).Result()
}

// ListenShieldHints 使用独立订阅连接，回调必须同步完成有界入队而不能执行关系查询。
// 首次握手最多三秒；取消显式关闭连接以解除阻塞读，返回后没有遗留订阅 goroutine。
// 非法载荷仅报告固定分类；连接失败由装配方退避重试，不影响定时检查。
func (s *Store) ListenShieldHints(ctx context.Context, accept func(ShieldHint), observe func(string)) (err error) {
	if ctx == nil || accept == nil {
		return ErrState
	}
	pattern := s.namespace + ":*:shield-hint"
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	sub := s.client.PSubscribe(call, pattern)
	var once sync.Once
	var closeErr error
	closeSub := func() error { once.Do(func() { closeErr = sub.Close() }); return closeErr }
	defer func() { err = errors.Join(err, closeSub()) }()
	reply, err := sub.ReceiveTimeout(call, 3*time.Second)
	cancel()
	if err != nil {
		return err
	}
	ack, ok := reply.(*redis.Subscription)
	if !ok || ack.Kind != "psubscribe" || ack.Channel != pattern {
		return ErrState
	}
	if observe != nil {
		observe("subscribed")
	}
	done, closed := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(closed)
		select {
		case <-ctx.Done():
			_ = closeSub()
		case <-done:
		}
	}()
	defer func() { close(done); <-closed }()
	for ctx.Err() == nil {
		message, readErr := sub.ReceiveMessage(ctx)
		if readErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return readErr
		}
		hint, valid := s.decodeShieldHint(message)
		if !valid {
			if observe != nil {
				observe("invalid")
			}
			continue
		}
		accept(hint)
	}
	return ctx.Err()
}

func (s *Store) decodeShieldHint(message *redis.Message) (ShieldHint, bool) {
	var hint ShieldHint
	if message == nil || len(message.Payload) > 2048 {
		return hint, false
	}
	decoder := json.NewDecoder(bytes.NewBufferString(message.Payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&hint) != nil || hint.Validate() != nil || message.Channel != s.shieldHintChannel(hint.TenantID) {
		return ShieldHint{}, false
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ShieldHint{}, false
	}
	return hint, true
}
