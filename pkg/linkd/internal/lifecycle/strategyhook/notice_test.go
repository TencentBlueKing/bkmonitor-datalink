// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategyhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/redisclient"
)

func TestChangeNoticeOnlyForChangedMembership(t *testing.T) {
	c := &setClient{}
	h, err := New(c, Config{KeyPrefix: "active", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	input := hookInput()
	for _, value := range []lifecycle.FinalHookInput{input, input, terminal(input, domain.AlertStatusRecovered), terminal(input, domain.AlertStatusClosed)} {
		if _, err := h.Execute(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.notices) != 2 {
		t.Fatalf("notices=%d", len(c.notices))
	}
	for _, n := range c.notices {
		payload := decodeNotice(t, n.payload)
		if n.channel != "active:changes" || payload != (changeNotice{BKTenantID: "tenant", StrategyID: "123"}) {
			t.Fatalf("notice=%+v %s", payload, n.channel)
		}
	}
	other := input
	other.Alert = other.Alert.Clone()
	other.Alert.BKTenantID = "other"
	other.Alert.Labels["strategy_id"], _ = domain.NewNumberScalar(456)
	if _, err := h.Execute(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	payload := decodeNotice(t, c.notices[2].payload)
	if payload.BKTenantID != "other" || payload.StrategyID != "456" {
		t.Fatalf("notice=%+v", payload)
	}
}

func TestConcurrentChangesNotifyOnce(t *testing.T) {
	c := &setClient{}
	h, err := New(c, Config{KeyPrefix: "active", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []lifecycle.FinalHookInput{hookInput(), terminal(hookInput(), domain.AlertStatusClosed)} {
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				if _, err := h.Execute(t.Context(), input); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
	}
	if len(c.notices) != 2 {
		t.Fatalf("concurrent notices=%d", len(c.notices))
	}
}

func TestNoticeDefaultAndFailureBoundaries(t *testing.T) {
	c := &setClient{}
	h, err := New(c, Config{KeyPrefix: "active", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Execute(t.Context(), hookInput()); err != nil {
		t.Fatal(err)
	}
	if c.evals != 1 || len(c.notices) != 1 || c.notices[0].channel != "active:changes" {
		t.Fatal("default notification did not use the shared prefix")
	}
	for _, pubFailure := range []bool{false, true} {
		client := &setClient{}
		if pubFailure {
			client.pubErr = errors.New("private-server-detail")
		} else {
			client.err = errors.New("private-server-detail")
		}
		hook, err := New(client, Config{KeyPrefix: "active", Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_, err = hook.Execute(t.Context(), hookInput())
		if err == nil || strings.Contains(err.Error(), "private-server-detail") || len(client.notices) != 0 {
			t.Fatalf("error=%v notices=%v", err, client.notices)
		}
		if pubFailure {
			if !client.values["active:tenant:123"]["fp"] {
				t.Fatal("expected possible partial effect")
			}
			client.pubErr = nil
			if _, err := hook.Execute(t.Context(), hookInput()); err != nil {
				t.Fatal(err)
			}
			if len(client.notices) != 0 {
				t.Fatal("unchanged retry must not manufacture change")
			}
		}
	}
	blocking, err := New(&setClient{block: true}, Config{KeyPrefix: "active", Timeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocking.Execute(t.Context(), hookInput()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := blocking.Execute(ctx, hookInput()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestNoticeUsesPrefixWithoutExtraConfiguration(t *testing.T) {
	for _, prefix := range []string{"alarmd:open_alerts", "another-prefix", strings.Repeat("x", 256)} {
		c := &setClient{}
		h, err := New(c, Config{KeyPrefix: prefix, Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.Execute(t.Context(), hookInput()); err != nil {
			t.Fatal(err)
		}
		if len(c.notices) != 1 || c.notices[0].channel != prefix+":changes" {
			t.Fatalf("wrong default channel for %q", prefix)
		}
	}
}

func TestNotificationRedisIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("LINKD_TEST_REDIS_ADDRESS is not set")
	}
	client, err := redisclient.New(redisclient.Options{Address: address, Username: os.Getenv("LINKD_TEST_REDIS_USERNAME"), Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), Database: 8, ContextTimeoutEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	prefix := fmt.Sprintf("linkd-test:strategy-notice:%d", time.Now().UnixNano())
	key, channel := prefix+":tenant:123", prefix+":changes"
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := client.Del(cleanup, key).Err(); err != nil {
			t.Error(err)
		}
	}()
	h, err := New(client, Config{KeyPrefix: prefix, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	// 没有订阅者时 PUBLISH 返回 0，仍是一次成功的集合修改。
	if _, err := h.Execute(ctx, hookInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Execute(ctx, terminal(hookInput(), domain.AlertStatusClosed)); err != nil {
		t.Fatal(err)
	}
	pubsub := client.Subscribe(ctx, channel)
	defer func() {
		if err := pubsub.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := pubsub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	for _, input := range []lifecycle.FinalHookInput{hookInput(), terminal(hookInput(), domain.AlertStatusClosed)} {
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				if _, err := h.Execute(ctx, input); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
	}
	// 同一 channel 的屏障消息用于排空此前所有通知，不依赖 sleep 猜测“没有重复消息”。
	if err := client.Publish(ctx, channel, "done").Err(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for {
		message, err := pubsub.ReceiveMessage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if message.Payload == "done" {
			break
		}
		count++
		notice := decodeNotice(t, message.Payload)
		if notice != (changeNotice{BKTenantID: "tenant", StrategyID: "123"}) {
			t.Fatalf("notice=%+v", notice)
		}
	}
	if count != 2 {
		t.Fatalf("notifications=%d", count)
	}
	if n, err := client.Exists(ctx, key).Result(); err != nil || n != 0 {
		t.Fatalf("terminal key exists=%d err=%v", n, err)
	}
	if err := client.Set(ctx, key, "wrong-type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Execute(ctx, hookInput()); err == nil {
		t.Fatal("wrong type accepted")
	}
	if err := client.Publish(ctx, channel, "after-error").Err(); err != nil {
		t.Fatal(err)
	}
	if message, err := pubsub.ReceiveMessage(ctx); err != nil || message.Payload != "after-error" {
		t.Fatalf("failed update published: %v %v", message, err)
	}
}

func decodeNotice(t *testing.T, payload string) changeNotice {
	t.Helper()
	var fields map[string]string
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["bk_tenant_id"] == "" || fields["strategy_id"] == "" {
		t.Fatalf("notice must contain only tenant and strategy: %s", payload)
	}
	return changeNotice{BKTenantID: fields["bk_tenant_id"], StrategyID: fields["strategy_id"]}
}
