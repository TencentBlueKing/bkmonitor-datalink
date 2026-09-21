// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"os/exec"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestSeriesSamplePreservesWorkerNoDataTriggerAndRecovery(t *testing.T) {
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skip("redis-server is required for the NoData sampling regression")
	}
	address := startG3ARedis(t)
	off, offBackend := openG3AStateStore(t, address, "sample-off")
	on, onBackend := openG3AStateStore(t, address, "sample-on")
	t.Cleanup(func() { _ = offBackend.Close(); _ = onBackend.Close() })
	worker.CheckSeriesSampleNoDataWorkerRegression(t, off, on)
}
