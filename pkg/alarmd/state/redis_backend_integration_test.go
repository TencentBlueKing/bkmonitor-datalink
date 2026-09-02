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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestRedisBackendStoreRoundTripTTLAndReconnect(t *testing.T) {
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	address := reserveTCPAddress(t)
	server := startRedisServer(t, executable, address)

	backend, err := NewRedisBackend(RedisBackendOptions{
		Address: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2,
	})
	if err != nil {
		t.Fatalf("NewRedisBackend() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	waitRedisReady(t, backend)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.MGet(cancelled, []string{"cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("MGet(cancelled) error = %v, want context.Canceled", err)
	}

	router, err := NewFixedRouter("monitor-01", backend)
	if err != nil {
		t.Fatalf("NewFixedRouter() error = %v", err)
	}
	codec, err := NewCodec(CodecLimits{MaxLevels: 4, MaxPoints: 16, MaxEncodedBytes: 4096})
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	store, err := NewStore(StoreOptions{
		Prefix: "alarmd-integration", Codec: codec, Router: router,
		Limits: StoreLimits{MaxKeysPerBatch: 4, MaxKeyBytesPerBatch: 4096, MaxLoadedBytes: 16 << 10, MaxWrittenBytes: 16 << 10},
		MinTTL: time.Second, MaxTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	requirement := requirement(5, "5", 1, 1)
	spec := LoadWindowSpec{Identity: storeIdentity("11", "a"), Requirements: []LevelRequirement{requirement}}
	loaded, err := store.LoadWindows(context.Background(), LoadWindowsRequest{Items: []LoadWindowSpec{spec}})
	if err != nil || loaded.Items[0].Status != LoadMissing {
		t.Fatalf("LoadWindows(missing) = (%+v, %v)", loaded, err)
	}
	mustApply(t, loaded.Items[0].Window, []StatePoint{point(100, "a", fact(requirement, LevelFactAnomalous))})
	written, err := store.WriteWindows(context.Background(), WriteWindowsRequest{Items: loaded.Items})
	if err != nil || written.Items[0].Status != WritePersisted {
		t.Fatalf("WriteWindows() = (%+v, %v)", written, err)
	}
	reloaded, err := store.LoadWindows(context.Background(), LoadWindowsRequest{Items: []LoadWindowSpec{spec}})
	if err != nil || reloaded.Items[0].Status != LoadFound {
		t.Fatalf("LoadWindows(found) = (%+v, %v)", reloaded, err)
	}
	history, _ := reloaded.Items[0].Window.History(5)
	if history.Summarize(100, 1).AnomalyCount != 1 {
		t.Fatal("binary packed state did not round-trip through Redis")
	}

	binaryValue := []byte{0, 1, 2, 0xff}
	if err := backend.SetMany(context.Background(), []BackendWrite{{Key: "binary", Value: binaryValue, TTL: 150 * time.Millisecond}}); err != nil {
		t.Fatalf("SetMany(binary) error = %v", err)
	}
	values, err := backend.MGet(context.Background(), []string{"missing", "binary"})
	if err != nil || values[0] != nil || !bytes.Equal(values[1], binaryValue) {
		t.Fatalf("MGet(binary) = (%v, %v)", values, err)
	}
	time.Sleep(250 * time.Millisecond)
	values, err = backend.MGet(context.Background(), []string{"binary"})
	if err != nil || values[0] != nil {
		t.Fatalf("MGet(expired) = (%v, %v)", values, err)
	}

	stopRedisServer(t, server)
	server = startRedisServer(t, executable, address)
	waitRedisReady(t, backend)
	if err := backend.Ping(context.Background()); err != nil {
		t.Fatalf("Ping(after restart) error = %v", err)
	}
	stopRedisServer(t, server)
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close() error = %v", err)
	}
	return address
}

func startRedisServer(t *testing.T, executable, address string) *exec.Cmd {
	t.Helper()
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi(port) error = %v", err)
	}
	command := exec.Command(executable,
		"--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--save", "", "--appendonly", "no",
		"--dir", t.TempDir(), "--daemonize", "no", "--loglevel", "warning",
	)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatalf("redis-server start error = %v", err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil || !command.ProcessState.Exited() {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	return command
}

func stopRedisServer(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if command == nil || command.ProcessState != nil {
		return
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("redis-server kill error = %v", err)
	}
	if err := command.Wait(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("redis-server wait error = %v", err)
		}
	}
}

func waitRedisReady(t *testing.T, backend *RedisBackend) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		lastErr = backend.Ping(ctx)
		cancel()
		if lastErr == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("redis-server at %s did not become ready: %v", fmt.Sprint(backend.Address()), lastErr)
}
