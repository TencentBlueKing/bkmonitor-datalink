// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// The renewal rule executed by Redis, not restated in Go.
//
// The in-memory backend the other tests use restates this rule, which means
// those tests pass whether or not the script implements it: a script that got
// the -1 reply wrong - the key that exists with no expiry, which is every key
// written before lifetimes existed - would leave all of them green and leave
// the leak exactly where it was. This is the only test that can tell.
func TestRenewIfBelowFollowsRedisPTTLReplies(t *testing.T) {
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	backend, err := NewRedisBackend(RedisBackendOptions{
		Address: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2,
	})
	if err != nil {
		t.Fatalf("NewRedisBackend() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	waitRedisReady(t, backend)
	ctx := context.Background()

	const lifetime = 10 * time.Second
	threshold := GenerationScopedRenewalThreshold(lifetime)

	// A key that is not there is left alone. Renewing it would create a key
	// with no value, which every reader classifies as corrupt state.
	renewed, err := backend.RenewIfBelow(ctx, "absent", lifetime, threshold)
	if err != nil {
		t.Fatal(err)
	}
	if renewed {
		t.Fatal("a key that does not exist was renewed into existence")
	}
	values, err := backend.MGet(ctx, []string{"absent"})
	if err != nil {
		t.Fatal(err)
	}
	if values[0] != nil {
		t.Fatalf("renewing an absent key created %q", values[0])
	}

	// A key with no expiry at all: PTTL answers -1, and it must be renewed.
	// This is the state every key written before lifetimes existed is in.
	// Written with a bare SET, because that is the state being tested and the
	// store's own writer refuses to produce it any more: a key with no expiry
	// can now only come from a build that predates lifetimes.
	if _, err := backend.client.Eval(ctx, `return redis.call('SET', KEYS[1], ARGV[1])`,
		[]string{"immortal"}, "v").Result(); err != nil {
		t.Fatal(err)
	}
	renewed, err = backend.RenewIfBelow(ctx, "immortal", lifetime, threshold)
	if err != nil {
		t.Fatal(err)
	}
	if !renewed {
		t.Fatal("a key with no expiry was not given one; keys written before this existed would stay immortal")
	}
	remaining := remainingLife(t, backend, "immortal")
	if remaining <= 0 || remaining > lifetime {
		t.Fatalf("remaining life = %s, want it inside the lifetime %s", remaining, lifetime)
	}

	// Plenty of life left: no renewal, and the remaining time must not jump.
	before := remainingLife(t, backend, "immortal")
	renewed, err = backend.RenewIfBelow(ctx, "immortal", lifetime, threshold)
	if err != nil {
		t.Fatal(err)
	}
	if renewed {
		t.Fatal("a key with most of its life left was renewed anyway")
	}
	after := remainingLife(t, backend, "immortal")
	if after > before {
		t.Fatalf("remaining life went from %s to %s without a renewal", before, after)
	}

	// Under the threshold: renewed back to the full lifetime.
	if err := backend.SetMany(ctx, []BackendWrite{{Key: "expiring", Value: []byte("v"), TTL: time.Second}}); err != nil {
		t.Fatal(err)
	}
	renewed, err = backend.RenewIfBelow(ctx, "expiring", lifetime, threshold)
	if err != nil {
		t.Fatal(err)
	}
	if !renewed {
		t.Fatal("a key about to expire was not renewed")
	}
	remaining = remainingLife(t, backend, "expiring")
	if remaining <= threshold {
		t.Fatalf("remaining life after renewal = %s, want more than the threshold %s", remaining, threshold)
	}
}

// remainingLife asks Redis what PTTL answers, so the assertions read the
// server's own view rather than a model of it.
//
// Through Eval rather than a PTTL method, because the client interface is
// deliberately the few commands this package needs and a test is not a reason
// to widen it.
func remainingLife(t *testing.T, backend *RedisBackend, key string) time.Duration {
	t.Helper()
	left, err := backend.client.Eval(context.Background(),
		`return redis.call('PTTL', KEYS[1])`, []string{key}).Int64()
	if err != nil {
		t.Fatal(err)
	}
	return time.Duration(left) * time.Millisecond
}
