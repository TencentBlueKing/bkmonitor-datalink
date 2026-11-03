// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redistest

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/go-redis/redis/v8"
)

// ScriptSite is one call site of a shared Lua script, for CheckScriptSite:
// Call makes the site's real call - its own keys and arguments, through its
// own store - on the client it is given, and Written reads, on a client of
// the test's own, that the call's writes are there.
type ScriptSite struct {
	// Prepare, when set, writes what the call works on, on the test's
	// client, before each call the server accepts.
	Prepare func(t *testing.T, client *redis.Client)
	Call    func(client *redis.Client) error
	Written func(t *testing.T, client *redis.Client)
	// Refused, when set, checks the error the call returned when the server
	// refused it - the caller's own wrapping of it - and may call the site
	// again on the refusing client. Without it the error is only required to
	// carry the server's refusal.
	Refused func(t *testing.T, client *redis.Client, err error)
}

// CheckScriptSite runs a script call site once on a real server, each way
// a script call meets a server: on a cold script cache, where EVALSHA is
// answered NOSCRIPT and the body follows as EVAL; on a warm one, one
// EVALSHA; after SCRIPT FLUSH, cold again; and refused, NOAUTH from a
// server that now requires a password, the refusal reaching the caller as
// an error and nothing written. Every call is one command, or the two of a
// cache miss, never a MULTI. Run it on each Redis major the deployments use
// (ServerEnv).
func CheckScriptSite(t *testing.T, site ScriptSite) {
	t.Helper()
	server := Start(t)
	if server == nil {
		return
	}
	admin := server.Client()
	ctx := context.Background()
	log := &commandNames{}
	client := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	client.AddHook(log)

	for _, step := range []struct {
		name string
		want string
	}{
		{"a cold script cache", "evalsha eval"},
		{"a warm script cache", "evalsha"},
		{"SCRIPT FLUSH", "evalsha eval"},
	} {
		if step.name == "SCRIPT FLUSH" {
			if err := admin.ScriptFlush(ctx).Err(); err != nil {
				t.Fatal(err)
			}
		}
		if err := admin.FlushDB(ctx).Err(); err != nil {
			t.Fatal(err)
		}
		if site.Prepare != nil {
			site.Prepare(t, admin)
		}
		log.reset()
		if err := site.Call(client); err != nil {
			t.Fatalf("on %s: %v", step.name, err)
		}
		if got := log.names(); got != step.want {
			t.Fatalf("on %s the call sent %q, want %q", step.name, got, step.want)
		}
		site.Written(t, admin)
	}

	if err := admin.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	const password = "not-the-callers"
	if err := admin.ConfigSet(ctx, "requirepass", password).Err(); err != nil {
		t.Fatal(err)
	}
	// Redis 5 asks every connection for the password from now on, the ones
	// already open included, so the test reads on one that gives it.
	_ = admin.Close()
	admin = redis.NewClient(&redis.Options{Addr: server.Addr, Password: password, MaxRetries: -1})
	t.Cleanup(func() { _ = admin.Close() })
	refused := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1})
	t.Cleanup(func() { _ = refused.Close() })
	// Redis 7 answers an unauthenticated call of more than ten arguments
	// with a protocol error before it would say NOAUTH, and closes the
	// connection; Redis 5 says NOAUTH. Both are the server refusing a caller
	// that has not authenticated.
	err := site.Call(refused)
	if err == nil || !(strings.Contains(err.Error(), "NOAUTH") || strings.Contains(err.Error(), "unauthenticated")) {
		t.Fatalf("on a server that requires a password the call returned %v, want its refusal", err)
	}
	if site.Refused != nil {
		site.Refused(t, refused, err)
	}
	if keys, err := admin.DBSize(ctx).Result(); err != nil || keys != 0 {
		t.Fatalf("a refused call left %d keys (%v), want none", keys, err)
	}
	if err := admin.ConfigSet(ctx, "requirepass", "").Err(); err != nil {
		t.Fatal(err)
	}
}

// commandNames keeps the names of the commands a client sends, pipelines
// included.
type commandNames struct {
	mu   sync.Mutex
	sent []string
}

func (log *commandNames) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	log.add(cmd)
	return ctx, nil
}
func (*commandNames) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (log *commandNames) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, cmd := range cmds {
		log.add(cmd)
	}
	return ctx, nil
}
func (*commandNames) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

func (log *commandNames) add(cmd redis.Cmder) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.sent = append(log.sent, cmd.Name())
}

func (log *commandNames) reset() {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.sent = nil
}

func (log *commandNames) names() string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return strings.Join(log.sent, " ")
}
