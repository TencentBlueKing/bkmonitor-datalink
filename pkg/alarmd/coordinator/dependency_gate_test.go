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
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
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

	blocker := DependencyBlocker{Dependency: DependencyRedis, ReasonCode: contract.ReasonRedisUnavailable}
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
	if _, err := gate.Pause(DependencyBlocker{Dependency: DependencyRedis, ReasonCode: contract.ReasonRedisUnavailable}); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Pause(DependencyBlocker{Dependency: DependencyOutputKafka, ReasonCode: contract.ReasonKafkaUnavailable}); err != nil {
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
	blocker := DependencyBlocker{Dependency: DependencyOutputKafka, ReasonCode: contract.ReasonKafkaUnavailable}
	if _, err := gate.Pause(blocker); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Pause(blocker); err != nil {
		t.Fatal(err)
	}
	updated := DependencyBlocker{Dependency: DependencyOutputKafka, ReasonCode: contract.ReasonOutputACKUnknown}
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
		{ReasonCode: contract.ReasonRedisUnavailable},
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

func TestCriticalDependencyGateSerializesTransitionCallbacks(t *testing.T) {
	t.Parallel()

	var (
		gate         *CriticalDependencyGate
		observedMu   sync.Mutex
		revisions    []uint64
		firstCalled  = make(chan struct{})
		releaseFirst = make(chan struct{})
	)
	gate = NewCriticalDependencyGate(func(transition DependencyGateTransition) {
		// The observer remains reentrant for state reads while transitions are serialized.
		_ = gate.Snapshot()
		if transition.Current.Revision == 1 {
			close(firstCalled)
			<-releaseFirst
		}
		observedMu.Lock()
		revisions = append(revisions, transition.Current.Revision)
		observedMu.Unlock()
	})

	firstDone := make(chan error, 1)
	go func() {
		_, err := gate.Pause(DependencyBlocker{
			Dependency: DependencyRedis, ReasonCode: contract.ReasonRedisUnavailable,
		})
		firstDone <- err
	}()
	<-firstCalled

	secondDone := make(chan error, 1)
	go func() {
		_, err := gate.Pause(DependencyBlocker{
			Dependency: DependencyProvider, ReasonCode: contract.ReasonProviderUnavailable,
		})
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second transition completed before the first observer: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if snapshot := gate.Snapshot(); snapshot.Revision != 1 {
		t.Fatalf("snapshot revision advanced before the first observer completed: %d", snapshot.Revision)
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	observedMu.Lock()
	defer observedMu.Unlock()
	if !reflect.DeepEqual(revisions, []uint64{1, 2}) {
		t.Fatalf("observer revisions = %v", revisions)
	}
}

func TestCriticalDependencyGateAcceptsOnlyCatalogedRetryableObservationReasons(t *testing.T) {
	t.Parallel()

	accepted := []DependencyBlocker{
		{Dependency: DependencyInputKafka, ReasonCode: contract.ReasonKafkaUnavailable},
		{Dependency: DependencyOutputKafka, ReasonCode: contract.ReasonKafkaUnavailable},
		{Dependency: DependencyOutputKafka, ReasonCode: contract.ReasonOutputACKUnknown},
		{Dependency: DependencyRedis, ReasonCode: contract.ReasonRedisUnavailable},
		{Dependency: DependencyRedis, ReasonCode: contract.ReasonStateWriteRetryable},
		{Dependency: DependencyProvider, ReasonCode: contract.ReasonProviderUnavailable},
	}
	for _, blocker := range accepted {
		gate := NewCriticalDependencyGate(nil)
		if _, err := gate.Pause(blocker); err != nil {
			t.Fatalf("Pause(%#v) = %v", blocker, err)
		}
	}

	rejected := []DependencyBlocker{
		{Dependency: DependencyRedis, ReasonCode: contract.ReasonRecordInvalid},
		{Dependency: DependencyProvider, ReasonCode: contract.ReasonKafkaUnavailable},
		{Dependency: DependencyInputKafka, ReasonCode: contract.ReasonOutputACKUnknown},
		{Dependency: DependencyOutputKafka, ReasonCode: "UNKNOWN_RETRYABLE_REASON"},
	}
	for _, blocker := range rejected {
		gate := NewCriticalDependencyGate(nil)
		if _, err := gate.Pause(blocker); err == nil {
			t.Fatalf("Pause(%#v) succeeded", blocker)
		}
		if snapshot := gate.Snapshot(); snapshot.State != DependencyGateReady || snapshot.Revision != 0 {
			t.Fatalf("invalid blocker changed gate state: %#v", snapshot)
		}
	}
}
