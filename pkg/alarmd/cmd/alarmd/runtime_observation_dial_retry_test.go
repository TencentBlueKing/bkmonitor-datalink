// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// lookupTimeout is the error a dial returns when the name of the address did
// not resolve in time.
func lookupTimeout() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "i/o timeout", Name: "redis.invalid", IsTimeout: true}}
}

// poolAboveFailures keeps a test's pool larger than the dials it fails:
// go-redis starts dialing in the background once a pool has failed as many
// dials in a row as it holds connections, and those dials would be counted
// with the call's.
const poolAboveFailures = 10

// scriptedDial fails its first failures dials with a lookup timeout and then
// dials for real, counting every dial.
type scriptedDial struct {
	mu       sync.Mutex
	failures int
	dials    int
}

func (s *scriptedDial) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials
}

func (s *scriptedDial) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	s.mu.Lock()
	s.dials++
	fail := s.dials <= s.failures
	s.mu.Unlock()
	if fail {
		return nil, lookupTimeout()
	}
	return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, addr)
}

type retriesHeard struct {
	mu      sync.Mutex
	reasons []string
}

func (r *retriesHeard) heard() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reasons...)
}

func (r *retriesHeard) hear(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reasons = append(r.reasons, reason)
}

// A lookup that timed out once is dialed again, and the call succeeds; the
// retry is heard once, as a timeout.
func TestADialThatFailsOnceIsDialedAgainAndTheCallSucceeds(t *testing.T) {
	address, _ := startPhaseTwoRedis(t)
	dial := &scriptedDial{failures: 1}
	heard := &retriesHeard{}
	options := &redis.UniversalOptions{Addrs: []string{address}, MaxRetries: -1, PoolSize: poolAboveFailures}
	withDialRetry(options, dial.dial, heard.hear)
	client := redis.NewUniversalClient(options)
	defer client.Close()
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("Ping() = %v after a second dial", err)
	}
	if dials, reasons := dial.count(), heard.heard(); dials != 2 || len(reasons) != 1 || reasons[0] != redisfailure.Timeout {
		t.Fatalf("dials %d, retries heard %v; want 2 dials and one timeout", dials, reasons)
	}
}

// Two failed dials fail the call, and its error is classified as the error of
// a client that never retried: the retry adds a second chance, not a new
// reason. There is exactly one second dial.
func TestADialThatFailsTwiceFailsTheCallWithTheSameReason(t *testing.T) {
	address, _ := startPhaseTwoRedis(t)
	reasonOf := func(retry bool) (string, int, []string) {
		dial := &scriptedDial{failures: 1 << 30}
		heard := &retriesHeard{}
		options := &redis.UniversalOptions{Addrs: []string{address}, MaxRetries: -1, PoolSize: poolAboveFailures}
		if retry {
			withDialRetry(options, dial.dial, heard.hear)
		} else {
			options.Dialer = dial.dial
		}
		client := redis.NewUniversalClient(options)
		defer client.Close()
		err := client.Ping(context.Background()).Err()
		if err == nil {
			t.Fatal("a dial that always fails answered")
		}
		return redisfailure.Reason(err), dial.count(), heard.heard()
	}
	without, withoutDials, _ := reasonOf(false)
	with, withDials, heard := reasonOf(true)
	if with != without || with != redisfailure.Timeout || withoutDials != 1 || withDials != 2 || len(heard) != 1 {
		t.Fatalf("reason %q (without retry %q), dials %d (without %d), retries heard %v", with, without, withDials, withoutDials, heard)
	}
}

// A caller that has given up is not dialed for again.
func TestADialForACallerThatGaveUpIsNotRepeated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dials := 0
	options := &redis.UniversalOptions{}
	withDialRetry(options, func(context.Context, string, string) (net.Conn, error) {
		dials++
		cancel()
		return nil, lookupTimeout()
	}, nil)
	if _, err := options.Dialer(ctx, "tcp", "redis.invalid:6379"); err == nil || dials != 1 {
		t.Fatalf("dials %d, error %v; want one dial and its error", dials, err)
	}
}

// The CLI's clients are built with the retry, and a retry is counted under
// the client's own name.
func TestTheCLIClientsDialAgainUnderTheirOwnName(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	heard := map[string][]string{}
	var mu sync.Mutex
	options := cliRedisOptions(config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: address}, "auth",
		func(client, reason string) {
			mu.Lock()
			defer mu.Unlock()
			heard[client] = append(heard[client], reason)
		})
	client := redis.NewUniversalClient(options)
	defer client.Close()
	if err := client.Ping(context.Background()).Err(); err == nil {
		t.Fatal("a closed port answered")
	}
	if len(heard) != 1 || len(heard["auth"]) != 1 || heard["auth"][0] != redisfailure.ConnectionRefused {
		t.Fatalf("retries heard %v, want one connection_refused under auth", heard)
	}
}
