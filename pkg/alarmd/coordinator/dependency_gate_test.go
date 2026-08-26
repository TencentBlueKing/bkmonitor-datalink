// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCriticalDependencyGatePausesAndResumesAdmission(t *testing.T) {
	t.Parallel()

	var gate *CriticalDependencyGate
	transitions := make([]DependencyGateTransition, 0, 2)
	gate = NewCriticalDependencyGate(func(transition DependencyGateTransition) {
		// Observers must be able to read the current snapshot without deadlock.
		_ = gate.Snapshot()
		transitions = append(transitions, transition)
	})
	if snapshot := gate.Snapshot(); snapshot.State != DependencyGateReady || snapshot.Revision != 0 || len(snapshot.Blockers) != 0 {
		t.Fatalf("initial snapshot = %#v", snapshot)
	}

	blocker := DependencyBlocker{Dependency: DependencyRedis, ReasonCode: "REDIS_UNAVAILABLE"}
	paused, err := gate.Pause(blocker)
	if err != nil {
		t.Fatal(err)
	}
	if paused.State != DependencyGatePaused || paused.Revision != 1 || !reflect.DeepEqual(paused.Blockers, []DependencyBlocker{blocker}) {
		t.Fatalf("paused snapshot = %#v", paused)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gate.WaitAdmission(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitAdmission() while paused = %v, want context canceled", err)
	}

	ready, err := gate.Resume(DependencyRedis)
	if err != nil {
		t.Fatal(err)
	}
	if ready.State != DependencyGateReady || ready.Revision != 2 || len(ready.Blockers) != 0 {
		t.Fatalf("resumed snapshot = %#v", ready)
	}
	if err := gate.WaitAdmission(context.Background()); err != nil {
		t.Fatalf("WaitAdmission() while ready = %v", err)
	}
	if len(transitions) != 2 || transitions[0].Previous.State != DependencyGateReady || transitions[0].Current.State != DependencyGatePaused ||
		transitions[1].Previous.State != DependencyGatePaused || transitions[1].Current.State != DependencyGateReady {
		t.Fatalf("transitions = %#v", transitions)
	}
}

func TestCriticalDependencyGateWaitsForEveryBlocker(t *testing.T) {
	t.Parallel()

	gate := NewCriticalDependencyGate(nil)
	if _, err := gate.Pause(DependencyBlocker{Dependency: DependencyRedis, ReasonCode: "REDIS_UNAVAILABLE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Pause(DependencyBlocker{Dependency: DependencyOutputKafka, ReasonCode: "KAFKA_UNAVAILABLE"}); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := gate.Resume(DependencyRedis); err != nil {
		t.Fatal(err)
	} else if snapshot.State != DependencyGatePaused || len(snapshot.Blockers) != 1 || snapshot.Blockers[0].Dependency != DependencyOutputKafka {
		t.Fatalf("partial resume snapshot = %#v", snapshot)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gate.WaitAdmission(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitAdmission() with one blocker = %v", err)
	}
	if _, err := gate.Resume(DependencyOutputKafka); err != nil {
		t.Fatal(err)
	}
	if err := gate.WaitAdmission(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCriticalDependencyGatePublishesOnlySnapshotChanges(t *testing.T) {
	t.Parallel()

	transitions := make([]DependencyGateTransition, 0, 2)
	gate := NewCriticalDependencyGate(func(transition DependencyGateTransition) {
		transitions = append(transitions, transition)
	})
	blocker := DependencyBlocker{Dependency: DependencyProvider, ReasonCode: "PROVIDER_UNAVAILABLE"}
	if _, err := gate.Pause(blocker); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Pause(blocker); err != nil {
		t.Fatal(err)
	}
	updated := DependencyBlocker{Dependency: DependencyProvider, ReasonCode: "PROVIDER_TIMEOUT"}
	if _, err := gate.Pause(updated); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Resume(DependencyInputKafka); err != nil {
		t.Fatal(err)
	}

	snapshot := gate.Snapshot()
	if snapshot.State != DependencyGatePaused || snapshot.Revision != 2 || !reflect.DeepEqual(snapshot.Blockers, []DependencyBlocker{updated}) {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(transitions) != 2 || transitions[0].Current.Revision != 1 || transitions[1].Current.Revision != 2 {
		t.Fatalf("transitions = %#v", transitions)
	}
	snapshot.Blockers[0].ReasonCode = "MUTATED"
	if got := gate.Snapshot().Blockers[0].ReasonCode; got != updated.ReasonCode {
		t.Fatalf("Snapshot() leaked blocker backing storage: %q", got)
	}
}

func TestCriticalDependencyGateRejectsIncompleteBlockerIdentity(t *testing.T) {
	t.Parallel()

	gate := NewCriticalDependencyGate(nil)
	for _, blocker := range []DependencyBlocker{
		{ReasonCode: "REDIS_UNAVAILABLE"},
		{Dependency: DependencyRedis},
	} {
		if _, err := gate.Pause(blocker); err == nil {
			t.Fatalf("Pause(%#v) succeeded", blocker)
		}
	}
	if _, err := gate.Resume(""); err == nil {
		t.Fatal("Resume() accepted an empty dependency")
	}
}
