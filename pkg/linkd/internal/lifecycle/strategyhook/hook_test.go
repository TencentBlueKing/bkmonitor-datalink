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
	"net"
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
	evals  int
}

func (c *setClient) Eval(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	if c.block {
		<-ctx.Done()
		return redis.NewCmdResult(nil, ctx.Err())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evals++
	if c.err != nil {
		return redis.NewCmdResult(nil, c.err)
	}
	if c.values == nil {
		c.values = map[string]map[string]bool{}
	}
	if c.values[keys[0]] == nil {
		c.values[keys[0]] = map[string]bool{}
	}
	c.values[keys[0]][args[0].(string)] = true
	return redis.NewCmdResult(int64(1), nil)
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
			h, err := New(c, Config{KeyPrefix: "active", Timeout: time.Second})
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
	failing, err := New(&setClient{err: errors.New("credential-private")}, Config{KeyPrefix: "active", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := failing.Execute(context.Background(), hookInput())
	if err == nil || strings.Contains(err.Error(), "credential-private") || result.MessageID == "" {
		t.Fatal("failure was lost or leaked")
	}
	blocking, err := New(&setClient{block: true}, Config{KeyPrefix: "active", Timeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blocking.Execute(context.Background(), hookInput()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout=%v", err)
	}
	cancelHook, err := New(&setClient{block: true}, Config{KeyPrefix: "active", Timeout: time.Minute})
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
	h, err := New(client, Config{KeyPrefix: "active", Timeout: 50 * time.Millisecond})
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
