// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
)

// probeRefusingClient answers PING and refuses every script, the way a
// server that will not run a write after TIME looks to the store. Only the
// two commands Ping uses are implemented; anything else is a nil-interface
// panic, which is what a test reaching past Ping deserves.
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

// A server that answers PING and refuses the fence is not a usable store,
// and Ping has to say so by name: readiness is the place this is caught,
// and a PING alone would pass it.
func TestPingRefusesAServerThatWillNotRunTheFence(t *testing.T) {
	refusal := errors.New("ERR Write commands not allowed after non deterministic commands")
	client := &probeRefusingClient{refusal: refusal}
	store, err := NewRedisStoreWithClient(client, "alarmd-ownership-test")
	if err != nil {
		t.Fatal(err)
	}
	err = store.Ping(context.Background())
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
