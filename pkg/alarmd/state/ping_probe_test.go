// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
)

// probeRefusingClient answers PING and refuses every script: a server that
// will not run a write after TIME, as the backend sees it. Only what Ping
// uses is implemented.
type probeRefusingClient struct {
	redis.UniversalClient
	refusal error
	evals   int
}

func (client *probeRefusingClient) Ping(ctx context.Context) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	cmd.SetVal("PONG")
	return cmd
}

func (client *probeRefusingClient) Eval(ctx context.Context, _ string, _ []string, _ ...interface{}) *redis.Cmd {
	client.evals++
	cmd := redis.NewCmd(ctx)
	cmd.SetErr(client.refusal)
	return cmd
}

// The batched State write runs the owner fence inside Redis; a server that
// answers PING but refuses that script cannot take a fenced write, and the
// backend's readiness has to say so by name rather than pass on the PING.
func TestRedisBackendPingRefusesAServerThatWillNotRunTheFence(t *testing.T) {
	refusal := errors.New("ERR Write commands not allowed after non deterministic commands")
	client := &probeRefusingClient{refusal: refusal}
	backend := &RedisBackend{address: "fake", client: client}
	err := backend.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping() = nil on a server that refuses the fence script, want the refusal")
	}
	if !errors.Is(err, refusal) {
		t.Fatalf("Ping() = %v, want it to wrap the server's refusal", err)
	}
	if !strings.Contains(err.Error(), "the owner fence cannot run on this Redis") {
		t.Fatalf("Ping() = %v, want the fence named as what cannot run", err)
	}
	if client.evals != 1 {
		t.Fatalf("Ping() ran the probe %d times, want once", client.evals)
	}
}
