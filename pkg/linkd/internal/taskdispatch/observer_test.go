// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"testing"
	"time"
)

func TestControllerObservationUsesCommittedTaskState(t *testing.T) {
	state, _, now := fixture()
	state.Tasks["one"] = Task{Source: "source", Role: "cleaner", Phase: "stopping"}
	state.Tasks["two"] = Task{Source: "source", Role: "cleaner", Phase: "running"}
	state.Statuses = []Status{{Source: "source", Role: "cleaner", Matching: 4, Target: 3, Running: 99}}
	worker := state.Workers["0"]
	worker.Seen = now.Add(-time.Minute)
	state.Workers["0"] = worker
	snapshot := controllerObservation(state, now)
	if snapshot.Replicas["cleaner"]["running"] != 1 || snapshot.Replicas["cleaner"]["shortage"] != 2 || snapshot.Tasks["cleaner"]["stopping"] != 1 || snapshot.Workers["stale"] != 1 {
		t.Fatalf("incorrect snapshot: %+v", snapshot)
	}
	empty := controllerObservation(newState(), now)
	if len(empty.Tasks) != 0 || empty.MetadataAge != -time.Second {
		t.Fatalf("empty snapshot: %+v", empty)
	}
}
