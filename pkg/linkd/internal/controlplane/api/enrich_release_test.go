// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"testing"

	"linkd/internal/taskdispatch"
)

func TestWorkerHistoricalEnrichReleaseScope(t *testing.T) {
	task := taskdispatch.Task{Worker: "worker-a", Source: "host", Role: "lifecycle", Version: 2, Phase: "running"}
	for _, tc := range []struct {
		name, worker, source, purpose string
		version                       int64
		want                          bool
	}{
		{"assigned", "worker-a", "host", "", 2, true},
		{"ordinary history denied", "worker-a", "host", "", 1, false},
		{"event history", "worker-a", "host", "enrich", 1, true},
		{"new event version", "worker-a", "host", "enrich", 3, true},
		{"target history", "worker-a", "host", "targets", 1, false},
		{"target new event version", "worker-a", "host", "targets", 3, false},
		{"target cross worker", "worker-b", "host", "targets", 1, false},
		{"target cross source", "worker-a", "other", "targets", 1, false},
		{"unsafe version", "worker-a", "host", "targets", 1 << 53, false},
		{"cross worker", "worker-b", "host", "enrich", 1, false},
		{"cross source", "worker-a", "other", "enrich", 1, false},
		{"zero version", "worker-a", "host", "enrich", 0, false},
		{"unknown purpose", "worker-a", "host", "other", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := canReadWorkerRelease(task, tc.worker, tc.source, tc.version, tc.purpose); got != tc.want {
				t.Fatalf("allowed=%v want=%v", got, tc.want)
			}
		})
	}
	task.Role = "cleaner"
	if canReadWorkerRelease(task, "worker-a", "host", 1, "enrich") {
		t.Fatal("cleaner can load historical enrichment")
	}
	task.Role = "lifecycle"
	task.Phase = "stopped"
	if canReadWorkerRelease(task, "worker-a", "host", 1, "enrich") {
		t.Fatal("stopped assignment can load history")
	}
}
