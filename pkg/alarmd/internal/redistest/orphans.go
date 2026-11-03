// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package redistest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// A server is stopped by the test's cleanup. A test binary that never runs
// its cleanups - killed, or ended by -timeout, which exits from the timer
// without them - leaves its servers running with no parent, and nothing on
// macOS ends a child with its parent. Each server is therefore registered
// under registryDir with the test process that owns it, and the first Start
// of every test process shuts down the servers whose owner is gone.

// registryDir holds one file per running server.
var registryDir = filepath.Join(os.TempDir(), "alarmd-redistest")

var reapOnce sync.Once

// registration is one running server and the test process that owns it.
type registration struct {
	Owner  int    `json:"owner"`
	Server int    `json:"server"`
	Addr   string `json:"addr"`
	// Dir is the server's own directory as it reports it (CONFIG GET dir):
	// what tells this server apart from a later one on the same port or pid.
	Dir string `json:"dir"`
}

// register records a started server and returns the file to remove when it
// stops. A server that cannot be recorded still runs; the test says so.
func register(tb testing.TB, entry registration) string {
	if err := os.MkdirAll(registryDir, 0o700); err != nil {
		tb.Logf("redistest: cannot record server %d for orphan cleanup: %v", entry.Server, err)
		return ""
	}
	body, _ := json.Marshal(entry)
	path := filepath.Join(registryDir, fmt.Sprintf("%d.json", entry.Server))
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, body, 0o600); err != nil {
		tb.Logf("redistest: cannot record server %d for orphan cleanup: %v", entry.Server, err)
		return ""
	}
	if err := os.Rename(temporary, path); err != nil {
		tb.Logf("redistest: cannot record server %d for orphan cleanup: %v", entry.Server, err)
		return ""
	}
	return path
}

// reapOrphansOnce runs reapOrphans once per test process.
func reapOrphansOnce(tb testing.TB) {
	reapOnce.Do(func() {
		if reaped := reapOrphans(registryDir); len(reaped) > 0 {
			tb.Logf("redistest: shut down %d servers left by test processes that are gone: %s", len(reaped), strings.Join(reaped, ", "))
		}
	})
}

// reapOrphans shuts down every server in dir whose owner is gone and that is
// still the server recorded: it answers on the recorded address from the
// recorded directory, or, when it asks for a password, it is a redis-server
// process titled with that address. A recording that no longer matches a
// running server is removed. It returns the addresses it shut down.
func reapOrphans(dir string) []string {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil
	}
	var reaped []string
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var entry registration
		if json.Unmarshal(body, &entry) != nil || entry.Owner <= 0 || entry.Server <= 0 || entry.Addr == "" {
			_ = os.Remove(path)
			continue
		}
		if !processGone(entry.Owner) {
			continue
		}
		if shutDown(entry) {
			reaped = append(reaped, entry.Addr)
		}
		_ = os.Remove(path)
	}
	return reaped
}

// shutDown ends the recorded server if it is still the one recorded, and
// says whether it did.
func shutDown(entry registration) bool {
	client := redis.NewClient(&redis.Options{Addr: entry.Addr, MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second) // fixed-wait: one local round trip to a server that is up or refuses at once
	defer cancel()
	served, err := client.ConfigGet(ctx, "dir").Result()
	switch {
	case err == nil:
		if len(served) != 2 || fmt.Sprint(served[1]) != entry.Dir {
			return false
		}
		// The server closes the connection as it exits, so the reply is an
		// error whether or not it went; whether it went is read after.
		_ = client.ShutdownNoSave(ctx).Err()
		return !answers(ctx, client)
	case strings.Contains(err.Error(), "NOAUTH"):
		// Started open and given a password since: not askable, so the
		// process is read instead.
		return killServerProcess(entry.Server, entry.Addr)
	default:
		return false
	}
}

func answers(ctx context.Context, client *redis.Client) bool {
	err := client.Ping(ctx).Err()
	return err == nil || errors.Is(err, context.DeadlineExceeded)
}
