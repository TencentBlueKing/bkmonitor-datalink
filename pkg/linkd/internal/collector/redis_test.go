// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// startReceiptsRedis 仅供显式集成测试使用，创建隔离 Unix socket 并回收进程和目录。
func startReceiptsRedis(t *testing.T) *redis.Client {
	t.Helper()
	folder, err := os.MkdirTemp("/tmp", "linkd-receipts-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(folder); err != nil {
			t.Error(err)
		}
	})
	socket := filepath.Join(folder, "redis.sock")
	//nolint:gosec // 固定 Redis 程序，参数仅含测试生成的临时 socket 路径。
	command := exec.CommandContext(t.Context(), "redis-server", "--port", "0", "--unixsocket", socket, "--save", "", "--appendonly", "no")
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, MaxRetries: -1})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for client.Ping(ctx).Err() != nil {
		select {
		case <-ctx.Done():
			t.Fatal("temporary Redis not ready")
		case <-time.After(20 * time.Millisecond):
		}
	}

	return client
}
