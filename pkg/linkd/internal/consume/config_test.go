// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import (
	"strings"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	valid := DefaultConfig()
	tests := []struct {
		name      string
		change    func(*Config)
		wantError string
	}{
		{name: "valid"},
		{name: "worker count", change: func(config *Config) { config.WorkerCount = 0 }, wantError: "worker_count"},
		{name: "batch exceeds inflight", change: func(config *Config) { config.MaxBatchMessages = config.MaxInflightMessages + 1 }, wantError: "max_batch_messages"},
		{name: "batch exceeds lane", change: func(config *Config) { config.MaxBatchMessages = config.MaxInflightPerLane + 1 }, wantError: "max_batch_messages"},
		{name: "batch bytes", change: func(config *Config) { config.MaxBatchBytes = config.MaxInflightBytes + 1 }, wantError: "max_batch_bytes"},
		{name: "retry queue", change: func(config *Config) { config.MaxRetryMessages = config.MaxInflightMessages + 1 }, wantError: "max_retry_messages"},
		{name: "retry backoff", change: func(config *Config) { config.RetryBackoffMin = config.RetryBackoffMax + 1 }, wantError: "retry_backoff_min"},
		{name: "shutdown", change: func(config *Config) { config.ShutdownDrainTimeout = 0 }, wantError: "shutdown_drain_timeout"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			if test.change != nil {
				test.change(&config)
			}
			err := config.Validate()
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}
