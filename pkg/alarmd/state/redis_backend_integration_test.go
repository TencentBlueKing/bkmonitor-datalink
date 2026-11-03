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
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	return redistest.FreeAddress(t)
}

func startRedisServer(t *testing.T, _ string, address string) *redistest.Instance {
	t.Helper()
	return redistest.StartAt(t, address)
}

func waitRedisReady(t *testing.T, backend *RedisBackend) {
	t.Helper()
	// The wait is bounded by the test's deadline, not by a window. Three
	// seconds encoded an assumption about machine load rather than about redis:
	// under `go test ./...` dozens of packages start their own server at the
	// same moment, and a window that is ample on an idle machine is not on a
	// loaded one - which turned a whole-tree "all green" into a function of load
	// rather than of the code. A healthy server answers Ping in milliseconds;
	// one that genuinely cannot start is read from the server's own output.
	// Each attempt is bounded by the backend's own dial and read timeouts.
	ctx := testwait.Context(t)
	deadline := testwait.Deadline(t)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = backend.Ping(ctx)
		if lastErr == nil {
			return
		}
		time.Sleep(10 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
	t.Fatalf("redis-server at %s did not become ready: %v", fmt.Sprint(backend.Address()), lastErr)
}
