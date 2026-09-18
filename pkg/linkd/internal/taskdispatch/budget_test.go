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

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func TestPlannerUsesWorkerRuntimeBudget(t *testing.T) {
	for _, tc := range []struct {
		name, role  string
		concurrency int
		bytes       int64
		want        int
	}{
		{"lifecycle concurrency", "lifecycle", 64, 256 << 20, 1},
		{"lifecycle inflight", "lifecycle", 128, 4 << 20, 1},
		{"cleaner defaults", "cleaner", 64, 256 << 20, 1},
		{"cleaner inflight", "cleaner", 128, 16 << 20, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, release, now := fixture()
			runtime := workerRuntime(config.Config{Cleaner: config.CleanerRuntimeConfig{WorkerCount: 64}, Lifecycle: &config.LifecycleConfig{Concurrency: 64}}, []string{tc.role})
			if tc.name == "lifecycle inflight" {
				runtime = workerRuntime(config.Config{}, []string{tc.role})
			}
			st.Workers = map[string]Worker{"worker": {ID: "worker", Roles: []string{tc.role}, Runtime: runtime, Seen: now, MaxTasks: 16, MaxConcurrency: tc.concurrency, MaxInflightBytes: tc.bytes}}
			second := release
			second.ID = "second"
			second.Spec = second.Spec.WithDefaults()
			second.Spec.EventSourceID = "second"
			second.Spec.Storage.Kafka.Topic = "other"
			st.Metadata[second.ID] = UpdateMetadata(Metadata{}, second.Spec, "other-topic-id", 3, nil, now)
			Reconcile(&st, []eventsource.Release{release, second}, now)
			if got := count(st, tc.role); got != tc.want {
				t.Fatalf("tasks=%d want %d", got, tc.want)
			}
			total := TaskBudget{}
			for _, task := range st.Tasks {
				total.Concurrency += task.Concurrency
				total.InflightBytes += task.InflightBytes
			}
			if total.Concurrency > tc.concurrency || total.InflightBytes > tc.bytes {
				t.Fatalf("worker budget exceeded: %+v", total)
			}
		})
	}
}

func TestCleanerSourceBudgetOverridesWorkerDefaults(t *testing.T) {
	runtime := workerRuntime(config.Config{Cleaner: config.CleanerRuntimeConfig{WorkerCount: 64, MaxInflightBytes: 32 << 20}}, []string{"cleaner"})
	budget, err := runtime.budgetFor("cleaner", config.EventSource{Cleaner: config.CleanerConfig{Runtime: &config.CleanerRuntimeConfig{WorkerCount: 16}}})
	if err != nil || budget.Concurrency != 16 || budget.InflightBytes != 32<<20 {
		t.Fatalf("budget=%+v err=%v", budget, err)
	}
	if _, err := runtime.budgetFor("cleaner", config.EventSource{Cleaner: config.CleanerConfig{Runtime: &config.CleanerRuntimeConfig{WorkerCount: -1}}}); err == nil {
		t.Fatal("invalid source override accepted")
	}
}

func TestWorkerAdmissionBudgets(t *testing.T) {
	for _, phase := range []string{"prepared", "running", "stopping", "stopped"} {
		t.Run(phase, func(t *testing.T) {
			a := Agent{Roles: []string{"lifecycle"}, Runtime: workerRuntime(config.Config{Lifecycle: &config.LifecycleConfig{Concurrency: 64}}, []string{"lifecycle"}), Worker: config.WorkerConfig{MaxConcurrency: 64}}
			budget := *a.Runtime.Lifecycle
			task := Task{ID: "next", Role: "lifecycle", Concurrency: budget.Concurrency, InflightBytes: budget.InflightBytes}
			locals := map[string]*localTask{"old": {phase: phase, budget: budget}}
			_, err := a.admitTask(task, config.EventSource{}, locals)
			if (err == nil) != (phase == "stopped") {
				t.Fatalf("phase %s admission=%v", phase, err)
			}
		})
	}
	a := Agent{Roles: []string{"cleaner", "lifecycle"}, Runtime: workerRuntime(config.Config{}, []string{"cleaner", "lifecycle"}), Worker: config.WorkerConfig{MaxConcurrency: 40}}
	budget := *a.Runtime.Lifecycle
	task := Task{ID: "next", Role: "lifecycle", Concurrency: budget.Concurrency, InflightBytes: budget.InflightBytes}
	locals := map[string]*localTask{"cleaner": {phase: "running", budget: TaskBudget{Concurrency: 8, InflightBytes: 16 << 20}}}
	if _, err := a.admitTask(task, config.EventSource{}, locals); err != nil {
		t.Fatal("shared all-in-one boundary rejected", err)
	}
	a.Worker.MaxConcurrency = 39
	if _, err := a.admitTask(task, config.EventSource{}, locals); err == nil {
		t.Fatal("all-in-one roles escaped shared limit")
	}
	a.Worker.MaxConcurrency = 40
	a.Worker.MaxInflightBytes = budget.InflightBytes
	if _, err := a.admitTask(task, config.EventSource{}, locals); err == nil {
		t.Fatal("inflight limit ignored")
	}
	a.Worker.MaxInflightBytes = 256 << 20
	a.Config.MaxTasks = 1
	if _, err := a.admitTask(task, config.EventSource{}, locals); err == nil {
		t.Fatal("task count limit ignored")
	}
	task.Concurrency++
	if _, err := a.admitTask(task, config.EventSource{}, nil); err == nil {
		t.Fatal("incorrect assigned cost accepted")
	}
	if err := (WorkerRuntime{}).validate([]string{"lifecycle"}); err == nil {
		t.Fatal("missing worker budget accepted")
	}
}
