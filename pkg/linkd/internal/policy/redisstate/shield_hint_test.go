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
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
)

func TestShieldHintValidatesChannelTenantAndPayload(t *testing.T) {
	s := &Store{namespace: "owned"}
	channel := s.shieldHintChannel("tenant")
	for _, m := range []*redis.Message{nil, {Channel: channel, Payload: `{}`}, {Channel: channel, Payload: `{"bk_tenant_id":"other","main_alert_id":"main"}`}, {Channel: channel, Payload: `{"bk_tenant_id":"tenant","main_alert_id":"main","force":true}`}, {Channel: channel, Payload: strings.Repeat("x", 2049)}, {Channel: channel, Payload: `{"bk_tenant_id":"tenant","main_alert_id":"main"}{}`}} {
		if _, ok := s.decodeShieldHint(m); ok {
			t.Fatal("invalid hint accepted")
		}
	}
	raw, _ := json.Marshal(ShieldHint{TenantID: "tenant", MainAlertID: "main"})
	hint, ok := s.decodeShieldHint(&redis.Message{Channel: channel, Payload: string(raw)})
	if !ok || hint.TenantID != "tenant" || hint.MainAlertID != "main" {
		t.Fatal(hint, ok)
	}
	if _, err := s.PublishShieldHint(t.Context(), "", "main"); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
}

func TestRedisShieldHintSubscriptionIsolationCancellationAndLoss(t *testing.T) {
	s := redisClipStore(t)
	if count, err := s.PublishShieldHint(t.Context(), "tenant", "no-listener"); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready := make(chan struct{}, 1)
	invalid := make(chan struct{}, 1)
	received := make(chan ShieldHint, 4)
	done := make(chan error, 1)
	go func() {
		done <- s.ListenShieldHints(ctx, func(h ShieldHint) { received <- h }, func(outcome string) {
			if outcome == "subscribed" {
				ready <- struct{}{}
			}
			if outcome == "invalid" {
				invalid <- struct{}{}
			}
		})
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("subscription not ready")
	}
	if n, err := s.PublishShieldHint(t.Context(), "tenant", "main"); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	select {
	case h := <-received:
		if h != (ShieldHint{TenantID: "tenant", MainAlertID: "main"}) {
			t.Fatal(h)
		}
	case <-time.After(time.Second):
		t.Fatal("hint missing")
	}
	// 同一个部署可以订阅不同租户，但载荷必须与每条租户 channel 完全相符。
	if err := s.client.Publish(t.Context(), s.shieldHintChannel("tenant"), `{"bk_tenant_id":"other","main_alert_id":"main"}`).Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-invalid:
	case <-time.After(time.Second):
		t.Fatal("foreign tenant not rejected")
	}
	other := &Store{client: s.client, namespace: s.namespace + "-other"}
	if n, err := other.PublishShieldHint(t.Context(), "tenant", "main"); err != nil || n != 0 {
		t.Fatal("deployment isolation", n, err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked read leaked on shutdown")
	}
	// 本地监听已经退出；另一连接的 PUBLISH 可能先于 Redis 处理 TCP 关闭。
	// 有界等待服务端清理订阅，持续残留仍失败，不能把一次计数当作泄漏证据。
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		n, err := s.PublishShieldHint(t.Context(), "tenant", "after-stop")
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("subscription remains", n)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if len(received) != 0 {
		t.Fatal("delivered forged or foreign hint")
	}
}
