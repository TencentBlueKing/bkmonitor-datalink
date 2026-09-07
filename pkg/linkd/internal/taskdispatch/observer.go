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
	"context"
	"time"

	"linkd/internal/taskdispatch/observation"
)

// Observer 是调度运行时显式注入的观测端口。
type Observer = observation.Observer

// WorkerObservation 是 worker 的聚合状态。
type WorkerObservation = observation.WorkerObservation

type noopObserver struct{}

func (noopObserver) Operation(context.Context, string, bool, time.Duration) {}

func (noopObserver) Transition(context.Context, string, string, string, string, string, time.Duration) {
}

func (noopObserver) ControllerSnapshot(context.Context, observation.ControllerObservation) {}

func (noopObserver) WorkerSnapshot(context.Context, WorkerObservation) {}

func observerOrNoop(observers ...Observer) Observer {
	if len(observers) > 0 && observers[0] != nil {
		return observers[0]
	}
	return noopObserver{}
}

func controllerObservation(state State, now time.Time) observation.ControllerObservation {
	result := observation.ControllerObservation{Tasks: map[string]map[string]int64{}, Workers: map[string]int64{}, Replicas: map[string]map[string]int64{}, Metadata: map[string]int64{}, MetadataAge: -time.Second}
	running := map[string]int64{}
	for _, task := range state.Tasks {
		if result.Tasks[task.Role] == nil {
			result.Tasks[task.Role] = map[string]int64{}
		}
		result.Tasks[task.Role][task.Phase]++
		if task.Phase == "running" {
			running[task.Source+"/"+task.Role]++
		}
	}
	for _, worker := range state.Workers {
		health := "healthy"
		switch {
		case now.Sub(worker.Seen) >= 10*time.Second:
			health = "stale"
		case worker.Draining:
			health = "draining"
		case now.Before(worker.StableAfter) || now.Before(worker.CooldownUntil):
			health = "cooldown"
		}
		result.Workers[health]++
	}
	for _, role := range []string{"cleaner", "lifecycle"} {
		totals := map[string]int64{}
		for _, status := range state.Statuses {
			if status.Role != role {
				continue
			}
			actual := running[status.Source+"/"+role]
			totals["matching"] += int64(status.Matching)
			totals["target"] += int64(status.Target)
			totals["running"] += actual
			totals["shortage"] += max(0, int64(status.Target)-actual)
		}
		result.Replicas[role] = totals
	}
	for _, m := range state.Metadata {
		health := "ready"
		if m.Error != "" {
			health = "error"
		} else if m.Success.IsZero() {
			health = "waiting"
		}
		result.Metadata[health]++
		if !m.Success.IsZero() {
			result.MetadataAge = max(result.MetadataAge, max(0, now.Sub(m.Success)))
		}
	}
	return result
}
