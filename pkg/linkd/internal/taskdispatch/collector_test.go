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

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func TestCollectorSingleOwnerAndRemoval(t *testing.T) {
	state, release, now := fixture()
	release.Spec.Collection = &config.SourceCollection{Mode: "pull", Plugin: "uc", IntervalMinutes: 5}
	for id, worker := range state.Workers {
		worker.Roles = []string{"collector"}
		worker.Runtime = WorkerRuntime{Collector: &TaskBudget{Concurrency: 1, InflightBytes: 1024}}
		state.Workers[id] = worker
	}
	Reconcile(&state, []eventsource.Release{release}, now)
	if count(state, "collector") != 1 {
		t.Fatalf("expected one source owner, got %d", count(state, "collector"))
	}
	var taskID string
	for id, task := range state.Tasks {
		if task.Role == "collector" {
			taskID = id
		}
	}
	original := state.Tasks[taskID]
	Reconcile(&state, []eventsource.Release{release}, now.Add(time.Second))
	if state.Tasks[taskID].Worker != original.Worker || state.Tasks[taskID].Epoch != original.Epoch {
		t.Fatal("stable owner moved")
	}
	release.Spec.Collection = nil
	release.Version++
	Reconcile(&state, []eventsource.Release{release}, now.Add(2*time.Second))
	task := state.Tasks[taskID]
	if task.Phase != "stopping" && task.Phase != "stopped" {
		t.Fatalf("removed collection still running: %+v", task)
	}
}

func TestCollectorBudgetRequiresConfiguration(t *testing.T) {
	if err := (WorkerRuntime{}).validate([]string{"collector"}); err == nil {
		t.Fatal("missing execution budget accepted")
	}
	cfg := config.Config{Collector: &config.CollectorConfig{MaxRequestBytes: 1024, MaxResponseBytes: 2048}}
	budget, err := workerRuntime(cfg, []string{"collector"}).budgetFor("collector", config.EventSource{})
	if err != nil || budget.Concurrency != 1 || budget.InflightBytes != 3072 {
		t.Fatalf("wrong collector budget: %+v %v", budget, err)
	}
}

func TestPushRecoveryReceivesCollectorOwner(t *testing.T) {
	state, release, now := fixture()
	release.Spec.Collection = &config.SourceCollection{Mode: "push", Plugin: "custom", Secret: "secret", RecoveryIntervalMinutes: 5}
	for id, worker := range state.Workers {
		worker.Roles = []string{"collector"}
		worker.Runtime = WorkerRuntime{Collector: &TaskBudget{Concurrency: 1, InflightBytes: 1024}}
		state.Workers[id] = worker
	}
	Reconcile(&state, []eventsource.Release{release}, now)
	if count(state, "collector") != 1 {
		t.Fatal("push recovery has no single owner")
	}
}

func TestLegacySourceDoesNotAcquireCollectorTask(t *testing.T) {
	state, release, now := fixture()
	for id, worker := range state.Workers {
		worker.Roles = append(worker.Roles, "collector")
		worker.Runtime.Collector = &TaskBudget{Concurrency: 1, InflightBytes: 1024}
		state.Workers[id] = worker
	}
	Reconcile(&state, []eventsource.Release{release}, now)
	if count(state, "collector") != 0 {
		t.Fatal("legacy source automatically enabled collection")
	}
	if count(state, "cleaner") != 3 || count(state, "lifecycle") != 8 {
		t.Fatal("optional collector changed existing processing roles")
	}
}
