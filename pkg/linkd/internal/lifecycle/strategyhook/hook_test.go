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
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/redisclient"
	"linkd/internal/store/storetest"
)

type setClient struct {
	mu     sync.Mutex
	values map[string]map[string]bool
	err    error
	block  bool
}

func (c *setClient) command(ctx context.Context, key string, add bool, members ...any) *redis.IntCmd {
	if c.block {
		<-ctx.Done()
		return redis.NewIntResult(0, ctx.Err())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return redis.NewIntResult(0, c.err)
	}
	if c.values == nil {
		c.values = map[string]map[string]bool{}
	}
	if c.values[key] == nil {
		c.values[key] = map[string]bool{}
	}
	for _, m := range members {
		if add {
			c.values[key][m.(string)] = true
		} else {
			delete(c.values[key], m.(string))
		}
	}
	return redis.NewIntResult(1, nil)
}

func (c *setClient) SAdd(ctx context.Context, key string, members ...any) *redis.IntCmd {
	return c.command(ctx, key, true, members...)
}

func (c *setClient) SRem(ctx context.Context, key string, members ...any) *redis.IntCmd {
	return c.command(ctx, key, false, members...)
}

func hookInput() lifecycle.FinalHookInput {
	alert := storetest.Alert("tenant", "alert", "event", "fp", "warning")
	alert.Labels["strategy_id"] = domain.NewStringScalar("123")
	return lifecycle.FinalHookInput{Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event"}, Alert: alert, Outcome: lifecycle.OutcomeAlertCreated}
}

func terminal(input lifecycle.FinalHookInput, status domain.AlertStatus) lifecycle.FinalHookInput {
	input.Alert = input.Alert.Clone()
	input.Alert.Status = status
	end := input.Alert.UpdateAt
	input.Alert.EndAt = &end
	input.Alert.EndType = domain.AlertEndTypeSource
	return input
}

func TestMembershipAndIsolation(t *testing.T) {
	c := &setClient{}
	h, err := New(c, "active", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	input := hookInput()
	first, err := h.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.Execute(context.Background(), input)
	if err != nil || first.MessageID != again.MessageID {
		t.Fatalf("repeat: %v", err)
	}
	if !c.values["active:tenant:123"]["fp"] || len(c.values["active:tenant:123"]) != 1 {
		t.Fatal("wrong set member")
	}
	other := input
	other.Alert = other.Alert.Clone()
	other.Alert.BKTenantID = "another"
	if _, err := h.Execute(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.AlertStatus{domain.AlertStatusRecovered, domain.AlertStatusClosed} {
		if _, err := h.Execute(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := h.Execute(context.Background(), terminal(input, status)); err != nil {
				t.Fatal(err)
			}
		}
		if c.values["active:tenant:123"]["fp"] || !c.values["active:another:123"]["fp"] {
			t.Fatal("terminal deletion crossed tenant boundary")
		}
	}
	separate, err := New(c, "another-prefix", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := separate.Execute(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if !c.values["another-prefix:tenant:123"]["fp"] || c.values["active:tenant:123"]["fp"] {
		t.Fatal("prefix boundary")
	}
}

func TestStrategyLabelBoundaries(t *testing.T) {
	number, _ := domain.NewNumberScalar(123)
	for _, tt := range []struct {
		name       string
		labels     domain.DimensionMap
		skip, fail bool
	}{
		{"missing", domain.DimensionMap{}, true, false},
		{"no fallback", domain.DimensionMap{"bk_strategy_id": number}, true, false},
		{"empty", domain.DimensionMap{"strategy_id": domain.NewStringScalar("")}, true, false},
		{"number", domain.DimensionMap{"strategy_id": number}, false, false},
		{"boolean", domain.DimensionMap{"strategy_id": domain.NewBoolScalar(true)}, false, true},
		{"invalid", domain.DimensionMap{"strategy_id": {}}, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &setClient{}
			h, err := New(c, "active", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			input := hookInput()
			input.Alert.Labels = tt.labels
			result, err := h.Execute(context.Background(), input)
			if (err != nil) != tt.fail || result.Skipped != tt.skip {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if (tt.skip || tt.fail) && len(c.values) != 0 {
				t.Fatal("invalid/missing label wrote redis")
			}
			if tt.name == "number" && result.Destination != "active:tenant:123" {
				t.Fatalf("key=%s", result.Destination)
			}
		})
	}
}

func TestFailureTimeoutAndConcurrentCancellation(t *testing.T) {
	failing, err := New(&setClient{err: errors.New("credential-private")}, "active", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := failing.Execute(context.Background(), hookInput())
	if err == nil || strings.Contains(err.Error(), "credential-private") || result.MessageID == "" {
		t.Fatal("failure was lost or leaked")
	}
	blocking, err := New(&setClient{block: true}, "active", 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blocking.Execute(context.Background(), hookInput()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout=%v", err)
	}
	cancelHook, err := New(&setClient{block: true}, "active", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, err := cancelHook.Execute(ctx, hookInput())
			if !errors.Is(err, context.Canceled) {
				t.Errorf("cancel=%v", err)
			}
		})
	}
	cancel()
	wg.Wait()
}

func TestRedisSocketRespectsMillisecondDeadline(t *testing.T) {
	// 本测试自己创建只接收不应答的服务，验证真实客户端握手不会越过插件截止时间。
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closeListener := func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	}
	defer closeListener()
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() {
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		}()
		<-stopped
	}()
	client, err := redisclient.New(redisclient.Options{Address: listener.Addr().String(), ContextTimeoutEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	h, err := New(client, "active", 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, callErr := h.Execute(context.Background(), hookInput())
	elapsed := time.Since(started)
	close(stopped)
	closeListener()
	<-done
	if !errors.Is(callErr, context.DeadlineExceeded) || elapsed > time.Second {
		t.Fatalf("deadline not enforced: %v, %s", callErr, elapsed)
	}
}

func TestRedisIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("LINKD_TEST_REDIS_ADDRESS is not set")
	}
	client, err := redisclient.New(redisclient.Options{Address: address, ContextTimeoutEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefix := fmt.Sprintf("linkd-test:strategy:%d", time.Now().UnixNano())
	key := prefix + ":tenant:123"
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := client.Del(cleanup, key).Err(); err != nil {
			t.Error(err)
		}
	}()
	h, err := New(client, prefix, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	input := hookInput()
	for range 2 {
		if _, err := h.Execute(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	members, err := client.SMembers(ctx, key).Result()
	if err != nil || !slices.Equal(members, []string{"fp"}) {
		t.Fatalf("members=%v error=%v", members, err)
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil || ttl != -1 {
		t.Fatalf("TTL=%v error=%v", ttl, err)
	}
	for _, status := range []domain.AlertStatus{domain.AlertStatusRecovered, domain.AlertStatusClosed} {
		if _, err := h.Execute(ctx, input); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := h.Execute(ctx, terminal(input, status)); err != nil {
				t.Fatal(err)
			}
		}
		count, err := client.SCard(ctx, key).Result()
		if err != nil || count != 0 {
			t.Fatalf("terminal set=%d error=%v", count, err)
		}
	}
}
