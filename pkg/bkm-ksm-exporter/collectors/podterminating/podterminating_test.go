// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podterminating

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkm-ksm-exporter/exporter"
)

type scriptedPodClient struct {
	list  func(context.Context, metav1.ListOptions) (*corev1.PodList, error)
	watch func(context.Context, metav1.ListOptions) (watch.Interface, error)
}

func (c *scriptedPodClient) List(ctx context.Context, options metav1.ListOptions) (*corev1.PodList, error) {
	return c.list(ctx, options)
}

func (c *scriptedPodClient) Watch(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
	return c.watch(ctx, options)
}

func deletingPod(uid, namespace, name, node string, requestedAt time.Time, graceSeconds int64) corev1.Pod {
	deletionTimestamp := metav1.NewTime(requestedAt.Add(time.Duration(graceSeconds) * time.Second))
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:                        types.UID(uid),
			Namespace:                  namespace,
			Name:                       name,
			ResourceVersion:            uid + "-rv",
			DeletionTimestamp:          &deletionTimestamp,
			DeletionGracePeriodSeconds: &graceSeconds,
		},
		Spec: corev1.PodSpec{NodeName: node},
	}
}

func TestListSnapshotStreamsPagesAndKeepsOnlyDeletingPods(t *testing.T) {
	now := time.Unix(10_000, 0)
	var options []metav1.ListOptions
	client := &scriptedPodClient{
		list: func(_ context.Context, option metav1.ListOptions) (*corev1.PodList, error) {
			options = append(options, option)
			switch option.Continue {
			case "":
				return &corev1.PodList{
					ListMeta: metav1.ListMeta{ResourceVersion: "42", Continue: "next"},
					Items: []corev1.Pod{
						{ObjectMeta: metav1.ObjectMeta{UID: "normal", Namespace: "ns", Name: "normal"}},
						deletingPod("deleting-1", "ns", "pod-1", "node-1", now.Add(-time.Minute), 30),
					},
				}, nil
			case "next":
				return &corev1.PodList{
					ListMeta: metav1.ListMeta{ResourceVersion: "42"},
					Items: []corev1.Pod{
						deletingPod("deleting-2", "ns", "pod-2", "node-2", now.Add(-2*time.Minute), 0),
					},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected continue token %q", option.Continue)
			}
		},
	}

	snapshot, resourceVersion, err := ListSnapshot(context.Background(), client, 1_000, time.Second)
	if err != nil {
		t.Fatalf("ListSnapshot: %v", err)
	}
	if resourceVersion != "42" {
		t.Fatalf("resourceVersion=%q, want 42", resourceVersion)
	}
	if len(snapshot) != 2 {
		t.Fatalf("retained entries=%d, want 2 deleting Pods", len(snapshot))
	}
	if got := snapshot[types.UID("deleting-1")].DeletionStartedAt; !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("deletion start=%s, want %s", got, now.Add(-time.Minute))
	}
	if len(options) != 2 || options[0].Limit != 1_000 || options[0].Continue != "" || options[1].Continue != "next" {
		t.Fatalf("unexpected pagination options: %#v", options)
	}
}

func TestListSnapshotRejectsInconsistentResourceVersion(t *testing.T) {
	client := &scriptedPodClient{
		list: func(_ context.Context, option metav1.ListOptions) (*corev1.PodList, error) {
			if option.Continue == "" {
				return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "42", Continue: "next"}}, nil
			}
			return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "43"}}, nil
		},
	}
	_, _, err := ListSnapshot(context.Background(), client, 1_000, time.Second)
	if err == nil || !strings.Contains(err.Error(), "resourceVersion") {
		t.Fatalf("error=%v, want inconsistent resourceVersion failure", err)
	}
}

func TestListSnapshotRejectsRepeatedContinueToken(t *testing.T) {
	client := &scriptedPodClient{
		list: func(_ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
			return &corev1.PodList{
				ListMeta: metav1.ListMeta{ResourceVersion: "42", Continue: "same"},
			}, nil
		},
	}
	_, _, err := ListSnapshot(context.Background(), client, 1_000, time.Second)
	if err == nil || !strings.Contains(err.Error(), "repeated continue token") {
		t.Fatalf("error=%v, want repeated token failure", err)
	}
}

func TestApplyWatchEventUsesUIDForSameNameReplacement(t *testing.T) {
	now := time.Unix(10_000, 0)
	oldPod := deletingPod("old", "ns", "same-name", "node", now.Add(-time.Hour), 0)
	newPod := deletingPod("new", "ns", "same-name", "node", now.Add(-time.Minute), 0)
	observed := map[types.UID]Observation{}

	if changed, err := ApplyWatchEvent(observed, watch.Event{Type: watch.Added, Object: &oldPod}); err != nil || !changed {
		t.Fatalf("add old: changed=%v err=%v", changed, err)
	}
	if changed, err := ApplyWatchEvent(observed, watch.Event{Type: watch.Added, Object: &newPod}); err != nil || !changed {
		t.Fatalf("add new: changed=%v err=%v", changed, err)
	}
	if changed, err := ApplyWatchEvent(observed, watch.Event{Type: watch.Deleted, Object: &oldPod}); err != nil || !changed {
		t.Fatalf("delete old: changed=%v err=%v", changed, err)
	}
	if len(observed) != 1 {
		t.Fatalf("entries=%d, want replacement only", len(observed))
	}
	if _, exists := observed[newPod.UID]; !exists {
		t.Fatal("old Delete event removed the new Pod with the same namespace/name/node")
	}
}

type memoryPersister struct {
	mu        sync.Mutex
	failures  int
	snapshots []Snapshot
}

func (p *memoryPersister) Persist(_ context.Context, snapshot Snapshot) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshots = append(p.snapshots, snapshot)
	if p.failures > 0 {
		p.failures--
		return 0, errors.New("patch failed")
	}
	raw, err := MarshalSnapshot(snapshot)
	return len(raw), err
}

func TestRecoveryHoldStartsAtFirstSuccessfulPersistence(t *testing.T) {
	dimension := Dimension{Namespace: "ns", Pod: "pod", Node: "node"}
	state := NewState(10*time.Minute, 15*time.Minute)
	if err := state.Restore(Snapshot{Version: StateVersion, Active: []Dimension{dimension}}, 64, time.Unix(1_000, 0)); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	persister := &memoryPersister{failures: 1}

	if err := state.Checkpoint(context.Background(), nil, time.Unix(1_060, 0), persister.Persist); err == nil {
		t.Fatal("first persistence must fail")
	}
	if err := state.Checkpoint(context.Background(), nil, time.Unix(1_720, 0), persister.Persist); err != nil {
		t.Fatalf("retry persistence: %v", err)
	}

	got := state.Snapshot(time.Unix(1_720, 0))
	if len(got.Rows) != 1 || got.Rows[0].Seconds != 0 {
		t.Fatalf("rows=%#v, want one recovery zero", got.Rows)
	}
	if len(persister.snapshots) != 2 || len(persister.snapshots[1].Recovery) != 1 {
		t.Fatalf("persisted snapshots=%#v", persister.snapshots)
	}
	if expires := persister.snapshots[1].Recovery[0].ExpiresAt; expires != 2_320 {
		t.Fatalf("expiresAt=%v, want 2320 (full hold from successful retry)", expires)
	}
}

func TestDelayedPendingConfirmationRestartsHoldForChangedRecoveryOnly(t *testing.T) {
	newDimension := Dimension{Namespace: "ns", Pod: "new", Node: "node"}
	restartDimension := Dimension{Namespace: "ns", Pod: "restart", Node: "node"}
	existingDimension := Dimension{Namespace: "ns", Pod: "existing", Node: "node"}
	state := NewState(10*time.Minute, 15*time.Minute)
	if err := state.Restore(Snapshot{
		Version: StateVersion,
		Active:  []Dimension{newDimension},
		Recovery: []RecoveryDimension{
			{
				Dimension: restartDimension,
				ExpiresAt: 2_000,
			},
			{
				Dimension:            existingDimension,
				ExpiresAt:            2_000,
				RestartExtensionUsed: true,
			},
		},
	}, 64, time.Unix(1_000, 0)); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	var serverState string
	patchCalls := 0
	getCalls := 0
	client := &scriptedConfigMapClient{
		patch: func(
			_ context.Context,
			_ string,
			_ types.PatchType,
			data []byte,
			_ metav1.PatchOptions,
			_ ...string,
		) (*corev1.ConfigMap, error) {
			patchCalls++
			var patch map[string]map[string]string
			if err := json.Unmarshal(data, &patch); err != nil {
				return nil, err
			}
			serverState = patch["data"][StateDataKey]
			if patchCalls == 1 {
				return nil, context.DeadlineExceeded
			}
			return &corev1.ConfigMap{Data: map[string]string{StateDataKey: serverState}}, nil
		},
		get: func(context.Context, string, metav1.GetOptions) (*corev1.ConfigMap, error) {
			getCalls++
			if getCalls == 1 {
				return nil, context.DeadlineExceeded
			}
			return &corev1.ConfigMap{Data: map[string]string{StateDataKey: serverState}}, nil
		},
	}
	store, err := NewStateStore(client, "state", time.Second, HardMaxStateBytes)
	if err != nil {
		t.Fatalf("NewStateStore: %v", err)
	}
	if err := state.Checkpoint(context.Background(), nil, time.Unix(1_060, 0), store.Save); err == nil {
		t.Fatal("ambiguous persistence must fail")
	}
	pending, ok, err := store.ResolvePending(context.Background())
	if err != nil {
		t.Fatalf("ResolvePending: %v", err)
	}
	if !ok {
		t.Fatal("server-side write was not confirmed")
	}
	raw, err := MarshalSnapshot(pending)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	confirmedAt := time.Unix(1_720, 0)
	if err := state.AcceptPersisted(pending, nil, len(raw), confirmedAt); err != nil {
		t.Fatalf("AcceptPersisted: %v", err)
	}

	if err := state.Checkpoint(context.Background(), nil, confirmedAt, store.Save); err != nil {
		t.Fatalf("Checkpoint after delayed confirmation: %v", err)
	}
	persisted, err := UnmarshalSnapshot([]byte(serverState), HardMaxStateBytes)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot: %v", err)
	}
	if len(persisted.Recovery) != 3 {
		t.Fatalf("recovery=%#v, want confirmed, restarted, and existing dimensions", persisted.Recovery)
	}
	expiries := map[Dimension]float64{}
	for _, recovery := range persisted.Recovery {
		expiries[recovery.Dimension] = recovery.ExpiresAt
	}
	if expires := expiries[newDimension]; expires != 2_320 {
		t.Fatalf("new recovery expiresAt=%v, want 2320", expires)
	}
	if expires := expiries[restartDimension]; expires != 2_320 {
		t.Fatalf("restart recovery expiresAt=%v, want 2320", expires)
	}
	if expires := expiries[existingDimension]; expires != 2_000 {
		t.Fatalf("existing recovery expiresAt=%v, want unchanged 2000", expires)
	}
}

func TestContinuousMetricIsComputedAtScrapeAndThenRecoversWithZero(t *testing.T) {
	now := time.Unix(10_000, 0)
	state := NewState(10*time.Minute, 15*time.Minute)
	persister := &memoryPersister{}
	observed := map[types.UID]Observation{
		"uid": {
			UID:               "uid",
			Dimension:         Dimension{Namespace: "ns", Pod: "pod", Node: "node"},
			DeletionStartedAt: now.Add(-30 * time.Second),
		},
	}
	if err := state.Checkpoint(context.Background(), observed, now, persister.Persist); err != nil {
		t.Fatalf("initial checkpoint: %v", err)
	}
	if got := state.Snapshot(now.Add(25 * time.Second)).Rows; len(got) != 1 || got[0].Seconds != 55 {
		t.Fatalf("rows=%#v, want continuously computed value 55", got)
	}

	if err := state.Checkpoint(context.Background(), nil, now.Add(time.Minute), persister.Persist); err != nil {
		t.Fatalf("recovery checkpoint: %v", err)
	}
	if got := state.Snapshot(now.Add(time.Minute)).Rows; len(got) != 1 || got[0].Seconds != 0 {
		t.Fatalf("rows=%#v, want same-dimension recovery zero", got)
	}
}

type scriptedConfigMapClient struct {
	patch  func(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*corev1.ConfigMap, error)
	get    func(context.Context, string, metav1.GetOptions) (*corev1.ConfigMap, error)
	create func(context.Context, *corev1.ConfigMap, metav1.CreateOptions) (*corev1.ConfigMap, error)
}

func (c *scriptedConfigMapClient) Patch(
	ctx context.Context,
	name string,
	patchType types.PatchType,
	data []byte,
	options metav1.PatchOptions,
	subresources ...string,
) (*corev1.ConfigMap, error) {
	return c.patch(ctx, name, patchType, data, options, subresources...)
}

func (c *scriptedConfigMapClient) Get(
	ctx context.Context,
	name string,
	options metav1.GetOptions,
) (*corev1.ConfigMap, error) {
	return c.get(ctx, name, options)
}

func (c *scriptedConfigMapClient) Create(
	ctx context.Context,
	configMap *corev1.ConfigMap,
	options metav1.CreateOptions,
) (*corev1.ConfigMap, error) {
	return c.create(ctx, configMap, options)
}

func TestStateStoreTreatsPatchTimeoutWithEqualReadbackAsSuccess(t *testing.T) {
	snapshot := Snapshot{
		Version: StateVersion,
		Active:  []Dimension{{Namespace: "ns", Pod: "pod", Node: "node"}},
	}
	raw, err := MarshalSnapshot(snapshot)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	client := &scriptedConfigMapClient{
		patch: func(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*corev1.ConfigMap, error) {
			return nil, context.DeadlineExceeded
		},
		get: func(context.Context, string, metav1.GetOptions) (*corev1.ConfigMap, error) {
			return &corev1.ConfigMap{Data: map[string]string{StateDataKey: string(raw)}}, nil
		},
	}
	store, err := NewStateStore(client, "state", time.Second, HardMaxStateBytes)
	if err != nil {
		t.Fatalf("NewStateStore: %v", err)
	}
	if _, err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatalf("Save should accept equal readback after ambiguous PATCH: %v", err)
	}
}

func TestStateStoreRejectsPatchErrorWithDifferentReadback(t *testing.T) {
	snapshot := Snapshot{
		Version: StateVersion,
		Active:  []Dimension{{Namespace: "ns", Pod: "candidate", Node: "node"}},
	}
	different, err := MarshalSnapshot(Snapshot{
		Version: StateVersion,
		Active:  []Dimension{{Namespace: "ns", Pod: "different", Node: "node"}},
	})
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	client := &scriptedConfigMapClient{
		patch: func(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*corev1.ConfigMap, error) {
			return nil, context.DeadlineExceeded
		},
		get: func(context.Context, string, metav1.GetOptions) (*corev1.ConfigMap, error) {
			return &corev1.ConfigMap{Data: map[string]string{StateDataKey: string(different)}}, nil
		},
	}
	store, err := NewStateStore(client, "state", time.Second, HardMaxStateBytes)
	if err != nil {
		t.Fatalf("NewStateStore: %v", err)
	}
	if _, err := store.Save(context.Background(), snapshot); err == nil || !strings.Contains(err.Error(), "readback differs") {
		t.Fatalf("Save error=%v, want different readback failure", err)
	}
	if _, ok, err := store.ResolvePending(context.Background()); err != nil || ok {
		t.Fatalf("different readback must not leave an ambiguous pending write: ok=%v err=%v", ok, err)
	}
}

func TestStateStoreConfirmsPreviousAmbiguousWriteBeforeNextPatch(t *testing.T) {
	first := Snapshot{
		Version: StateVersion,
		Active:  []Dimension{{Namespace: "ns", Pod: "first", Node: "node"}},
	}
	second := Snapshot{
		Version: StateVersion,
		Active:  []Dimension{{Namespace: "ns", Pod: "second", Node: "node"}},
	}
	firstRaw, err := MarshalSnapshot(first)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	getCalls := 0
	patchCalls := 0
	client := &scriptedConfigMapClient{
		patch: func(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*corev1.ConfigMap, error) {
			patchCalls++
			return nil, context.DeadlineExceeded
		},
		get: func(context.Context, string, metav1.GetOptions) (*corev1.ConfigMap, error) {
			getCalls++
			if getCalls == 1 {
				return nil, context.DeadlineExceeded
			}
			return &corev1.ConfigMap{Data: map[string]string{StateDataKey: string(firstRaw)}}, nil
		},
	}
	store, err := NewStateStore(client, "state", time.Second, HardMaxStateBytes)
	if err != nil {
		t.Fatalf("NewStateStore: %v", err)
	}
	if _, err := store.Save(context.Background(), first); err == nil {
		t.Fatal("first ambiguous Save must remain unresolved")
	}
	resolved, ok, err := store.ResolvePending(context.Background())
	if err != nil {
		t.Fatalf("ResolvePending: %v", err)
	}
	if !ok || len(resolved.Active) != 1 || resolved.Active[0].Pod != "first" {
		t.Fatalf("resolved=%#v ok=%v, want first snapshot", resolved, ok)
	}
	if patchCalls != 1 {
		t.Fatalf("patch calls=%d, pending confirmation must happen before another PATCH", patchCalls)
	}
	if _, err := store.Save(context.Background(), second); err == nil {
		t.Fatal("second scripted PATCH must still fail")
	}
	if patchCalls != 2 {
		t.Fatalf("patch calls=%d, want second PATCH only after pending resolution", patchCalls)
	}
}

func TestEnsureStateConfigMapCreatesOnlyWhenAbsentAndPreservesExistingState(t *testing.T) {
	getCalls := 0
	createCalls := 0
	existing := Snapshot{
		Version: StateVersion,
		Active:  []Dimension{{Namespace: "ns", Pod: "existing", Node: "node"}},
	}
	existingRaw, err := MarshalSnapshot(existing)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	client := &scriptedConfigMapClient{
		get: func(context.Context, string, metav1.GetOptions) (*corev1.ConfigMap, error) {
			getCalls++
			if getCalls == 1 {
				return nil, apierrors.NewNotFound(corev1.Resource("configmaps"), "state")
			}
			return &corev1.ConfigMap{Data: map[string]string{StateDataKey: string(existingRaw)}}, nil
		},
		create: func(_ context.Context, configMap *corev1.ConfigMap, _ metav1.CreateOptions) (*corev1.ConfigMap, error) {
			createCalls++
			if configMap.Data[StateDataKey] != `{"version":2,"active":null,"recovery":null}` {
				t.Fatalf("unexpected initial state: %s", configMap.Data[StateDataKey])
			}
			return configMap, nil
		},
	}
	if err := EnsureStateConfigMap(context.Background(), client, "state", time.Second, HardMaxStateBytes); err != nil {
		t.Fatalf("create absent state: %v", err)
	}
	if err := EnsureStateConfigMap(context.Background(), client, "state", time.Second, HardMaxStateBytes); err != nil {
		t.Fatalf("validate existing state: %v", err)
	}
	if createCalls != 1 {
		t.Fatalf("create calls=%d, upgrade must not overwrite existing state", createCalls)
	}
}

func TestRestartRecoveryExtensionIsPersistedOnlyOnce(t *testing.T) {
	dimension := Dimension{Namespace: "ns", Pod: "pod", Node: "node"}
	restored := Snapshot{
		Version: StateVersion,
		Recovery: []RecoveryDimension{{
			Dimension: dimension,
			ExpiresAt: 1_100,
		}},
	}
	firstState := NewState(10*time.Minute, 15*time.Minute)
	if err := firstState.Restore(restored, 64, time.Unix(1_000, 0)); err != nil {
		t.Fatalf("Restore first process: %v", err)
	}
	firstPersist := &memoryPersister{}
	if err := firstState.Checkpoint(context.Background(), nil, time.Unix(1_050, 0), firstPersist.Persist); err != nil {
		t.Fatalf("first restart checkpoint: %v", err)
	}
	persisted := firstPersist.snapshots[0]
	if persisted.Recovery[0].ExpiresAt != 1_650 || !persisted.Recovery[0].RestartExtensionUsed {
		t.Fatalf("first restart extension=%#v, want expiry 1650 and used=true", persisted.Recovery[0])
	}

	secondState := NewState(10*time.Minute, 15*time.Minute)
	raw, err := MarshalSnapshot(persisted)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	if err := secondState.Restore(persisted, len(raw), time.Unix(1_060, 0)); err != nil {
		t.Fatalf("Restore second process: %v", err)
	}
	secondPersist := &memoryPersister{}
	if err := secondState.Checkpoint(context.Background(), nil, time.Unix(1_070, 0), secondPersist.Persist); err != nil {
		t.Fatalf("second restart checkpoint: %v", err)
	}
	if expires := secondPersist.snapshots[0].Recovery[0].ExpiresAt; expires != 1_650 {
		t.Fatalf("second restart extended expiry to %v, want unchanged 1650", expires)
	}
}

func TestExpiredRecoveryIsRemovedAtCheckpoint(t *testing.T) {
	dimension := Dimension{Namespace: "ns", Pod: "pod", Node: "node"}
	state := NewState(10*time.Minute, 15*time.Minute)
	if err := state.Restore(Snapshot{
		Version: StateVersion,
		Recovery: []RecoveryDimension{{
			Dimension:            dimension,
			ExpiresAt:            1_100,
			RestartExtensionUsed: true,
		}},
	}, 64, time.Unix(1_000, 0)); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	persister := &memoryPersister{}
	if err := state.Checkpoint(context.Background(), nil, time.Unix(1_101, 0), persister.Persist); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(persister.snapshots[0].Recovery) != 0 {
		t.Fatalf("expired recovery persisted: %#v", persister.snapshots[0].Recovery)
	}
}

func TestSnapshotCapacityFailsClosedInsteadOfTruncatingDimensions(t *testing.T) {
	snapshot := Snapshot{Version: StateVersion}
	for index := 0; index < 2_000; index++ {
		snapshot.Recovery = append(snapshot.Recovery, RecoveryDimension{
			Dimension: Dimension{
				Namespace: strings.Repeat("n", 63),
				Pod:       fmt.Sprintf("%06d-%s", index, strings.Repeat("p", 240)),
				Node:      fmt.Sprintf("%06d-%s", index, strings.Repeat("d", 240)),
			},
			ExpiresAt: 10_000,
		})
	}
	if raw, err := MarshalSnapshot(snapshot); err == nil {
		t.Fatalf("oversize snapshot unexpectedly serialized to %d bytes", len(raw))
	} else if !strings.Contains(err.Error(), "exceeds hard limit") {
		t.Fatalf("error=%v, want hard capacity failure", err)
	}
}

func TestCollectorWritesAllDeletingPodsWithoutAlertThreshold(t *testing.T) {
	now := time.Unix(10_000, 0)
	state := NewState(10*time.Minute, 15*time.Minute)
	persister := &memoryPersister{}
	if err := state.Checkpoint(context.Background(), map[types.UID]Observation{
		"uid": {
			UID:               "uid",
			Dimension:         Dimension{Namespace: "ns", Pod: "pod", Node: "node"},
			DeletionStartedAt: now.Add(-30 * time.Second),
		},
	}, now, persister.Persist); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	collector := NewCollector(state, func() time.Time { return now })
	var output strings.Builder
	if err := collector.Write(&output); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(output.String(), `pod_terminating_seconds{namespace="ns",pod="pod",node="node"} 30`) {
		t.Fatalf("output missing 30-second terminating Pod:\n%s", output.String())
	}
}

type runnerStore struct {
	memoryPersister
}

func (s *runnerStore) Save(ctx context.Context, snapshot Snapshot) (int, error) {
	return s.Persist(ctx, snapshot)
}

func (s *runnerStore) ResolvePending(context.Context) (Snapshot, bool, error) {
	return Snapshot{}, false, nil
}

func TestRunnerRuntimeFailureRemainsScrapeable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scrapeNow := time.Now()
	pod := deletingPod("uid", "ns", "pod", "node", scrapeNow.Add(-30*time.Second), 0)
	firstWatcher := watch.NewRaceFreeFake()
	var watchCalls atomic.Int32
	client := &scriptedPodClient{
		list: func(_ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
			return &corev1.PodList{
				ListMeta: metav1.ListMeta{ResourceVersion: "42"},
				Items:    []corev1.Pod{pod},
			}, nil
		},
		watch: func(_ context.Context, _ metav1.ListOptions) (watch.Interface, error) {
			if watchCalls.Add(1) == 1 {
				return firstWatcher, nil
			}
			return nil, errors.New("watch failed")
		},
	}
	state := NewState(10*time.Minute, 15*time.Minute)
	server := exporter.New("127.0.0.1:0")
	server.Register(NewCollector(state, func() time.Time { return scrapeNow }))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("/metrics before initial checkpoint=%d, want 503", recorder.Code)
	}
	runner, err := NewRunner(client, state, &runnerStore{}, RunnerOptions{
		PageLimit:          1_000,
		RequestTimeout:     time.Second,
		CheckpointInterval: 5 * time.Millisecond,
		RetryInterval:      5 * time.Millisecond,
		SetReady:           server.SetReady,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if watchCalls.Load() == 1 && recorder.Code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runner did not complete its initial checkpoint and Watch connection")
		}
		time.Sleep(time.Millisecond)
	}

	firstWatcher.Stop()
	deadline = time.Now().Add(time.Second)
	for state.Snapshot(time.Now()).RefreshSuccess != 0 || watchCalls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("runner did not report the runtime Watch failure")
		}
		time.Sleep(time.Millisecond)
	}

	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics after runtime failure=%d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "pod_terminating_reporter_refresh_success 0") {
		t.Fatalf("/metrics missing refresh failure:\n%s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `pod_terminating_seconds{namespace="ns",pod="pod",node="node"}`) {
		t.Fatalf("/metrics hid business rows before staleAfter:\n%s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/readyz after runtime failure=%d, want initialized 200", recorder.Code)
	}

	scrapeNow = scrapeNow.Add(20 * time.Minute)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), "pod_terminating_reporter_refresh_success 0") {
		t.Fatalf("/metrics after staleAfter did not retain health metrics:\n%s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "pod_terminating_seconds{") {
		t.Fatalf("/metrics retained stale business rows:\n%s", recorder.Body.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunnerRelistsAfterExpiredResourceVersion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var listCalls atomic.Int32
	var watchCalls atomic.Int32
	client := &scriptedPodClient{
		list: func(_ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
			call := listCalls.Add(1)
			return &corev1.PodList{
				ListMeta: metav1.ListMeta{ResourceVersion: fmt.Sprintf("%d", call)},
			}, nil
		},
		watch: func(_ context.Context, _ metav1.ListOptions) (watch.Interface, error) {
			call := watchCalls.Add(1)
			watcher := watch.NewRaceFreeFake()
			if call == 1 {
				watcher.Error(&apierrors.NewResourceExpired("compacted").ErrStatus)
				watcher.Stop()
			} else if call == 2 {
				cancel()
			}
			return watcher, nil
		},
	}
	state := NewState(10*time.Minute, 15*time.Minute)
	store := &runnerStore{}
	runner, err := NewRunner(client, state, store, RunnerOptions{
		PageLimit:          1_000,
		RequestTimeout:     time.Second,
		CheckpointInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if listCalls.Load() != 2 {
		t.Fatalf("list calls=%d, want relist after 410", listCalls.Load())
	}
	if watchCalls.Load() != 2 {
		t.Fatalf("watch calls=%d, want one watch per snapshot RV", watchCalls.Load())
	}
}

func TestRunnerReconnectsClosedWatchWithoutRelist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var listCalls atomic.Int32
	var watchCalls atomic.Int32
	client := &scriptedPodClient{
		list: func(_ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
			listCalls.Add(1)
			return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "42"}}, nil
		},
		watch: func(_ context.Context, _ metav1.ListOptions) (watch.Interface, error) {
			call := watchCalls.Add(1)
			watcher := watch.NewRaceFreeFake()
			if call == 1 {
				watcher.Stop()
				return watcher, nil
			}
			pod := deletingPod("uid", "ns", "pod", "node", time.Unix(10_000, 0), 0)
			go func() {
				watcher.Add(&pod)
				time.Sleep(20 * time.Millisecond)
				cancel()
			}()
			return watcher, nil
		},
	}
	state := NewState(10*time.Minute, 15*time.Minute)
	runner, err := NewRunner(client, state, &runnerStore{}, RunnerOptions{
		PageLimit:          1_000,
		RequestTimeout:     time.Second,
		CheckpointInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if listCalls.Load() != 1 {
		t.Fatalf("list calls=%d, normal Watch reconnect must not relist", listCalls.Load())
	}
	if watchCalls.Load() < 2 {
		t.Fatalf("watch calls=%d, closed Watch was not reconnected", watchCalls.Load())
	}
}

func TestRunnerReconnectsFromLastObservedResourceVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var watchCalls atomic.Int32
	var secondResourceVersion string
	client := &scriptedPodClient{
		list: func(_ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
			return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "42"}}, nil
		},
		watch: func(_ context.Context, options metav1.ListOptions) (watch.Interface, error) {
			call := watchCalls.Add(1)
			watcher := watch.NewRaceFreeFake()
			if call == 1 {
				pod := deletingPod("uid", "ns", "pod", "node", time.Unix(10_000, 0), 0)
				watcher.Add(&pod)
				watcher.Stop()
				return watcher, nil
			}
			secondResourceVersion = options.ResourceVersion
			cancel()
			return watcher, nil
		},
	}
	runner, err := NewRunner(
		client,
		NewState(10*time.Minute, 15*time.Minute),
		&runnerStore{},
		RunnerOptions{
			PageLimit:          1_000,
			RequestTimeout:     time.Second,
			CheckpointInterval: 5 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if secondResourceVersion != "uid-rv" {
		t.Fatalf("reconnect resourceVersion=%q, want last event uid-rv", secondResourceVersion)
	}
}

func TestRunnerSilentWatchDoesNotRefreshHealthFromCheckpointTicker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchStarted := make(chan metav1.ListOptions, 1)
	client := &scriptedPodClient{
		list: func(_ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
			return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "42"}}, nil
		},
		watch: func(_ context.Context, options metav1.ListOptions) (watch.Interface, error) {
			watchStarted <- options
			return watch.NewRaceFreeFake(), nil
		},
	}
	state := NewState(10*time.Minute, 15*time.Minute)
	runner, err := NewRunner(client, state, &runnerStore{}, RunnerOptions{
		PageLimit:          1_000,
		RequestTimeout:     200 * time.Millisecond,
		CheckpointInterval: 5 * time.Millisecond,
		RetryInterval:      5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx)
	}()

	options := <-watchStarted
	if options.TimeoutSeconds == nil || *options.TimeoutSeconds != 1 {
		t.Fatalf("watch timeoutSeconds=%v, want 1", options.TimeoutSeconds)
	}
	state.MarkFailure()
	time.Sleep(30 * time.Millisecond)
	if success := state.Snapshot(time.Now()).RefreshSuccess; success != 0 {
		t.Fatalf("refresh_success=%v, silent checkpoint ticks must not mark the Watch healthy", success)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunnerReconnectsSilentWatchWithoutResettingReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var watchCalls atomic.Int32
	var sawNotReady atomic.Bool
	var sawReady atomic.Bool
	client := &scriptedPodClient{
		list: func(_ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
			return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "42"}}, nil
		},
		watch: func(_ context.Context, _ metav1.ListOptions) (watch.Interface, error) {
			call := watchCalls.Add(1)
			watcher := watch.NewRaceFreeFake()
			if call == 2 {
				cancel()
			}
			return watcher, nil
		},
	}
	runner, err := NewRunner(
		client,
		NewState(10*time.Minute, 15*time.Minute),
		&runnerStore{},
		RunnerOptions{
			PageLimit:          1_000,
			RequestTimeout:     20 * time.Millisecond,
			CheckpointInterval: 5 * time.Millisecond,
			RetryInterval:      5 * time.Millisecond,
			SetReady: func(ready bool) {
				if !ready {
					sawNotReady.Store(true)
					return
				}
				sawReady.Store(true)
			},
		},
	)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if watchCalls.Load() < 2 {
		t.Fatalf("watch calls=%d, silent Watch was not reconnected", watchCalls.Load())
	}
	if !sawReady.Load() {
		t.Fatal("initial checkpoint did not open the readiness gate")
	}
	if sawNotReady.Load() {
		t.Fatal("runtime Watch failure reset the startup readiness gate")
	}
}

func TestRunnerWatchConnectionUsesClientDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var watchCalls atomic.Int32
	var firstWatchHadBoundedDeadline atomic.Bool
	client := &scriptedPodClient{
		list: func(_ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
			return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "42"}}, nil
		},
		watch: func(watchContext context.Context, _ metav1.ListOptions) (watch.Interface, error) {
			call := watchCalls.Add(1)
			if call == 1 {
				deadline, hasDeadline := watchContext.Deadline()
				firstWatchHadBoundedDeadline.Store(
					hasDeadline && time.Until(deadline) < 100*time.Millisecond,
				)
				<-watchContext.Done()
				return nil, watchContext.Err()
			}
			cancel()
			return watch.NewRaceFreeFake(), nil
		},
	}
	readyTransitions := make(chan bool, 8)
	runner, err := NewRunner(
		client,
		NewState(10*time.Minute, 15*time.Minute),
		&runnerStore{},
		RunnerOptions{
			PageLimit:          1_000,
			RequestTimeout:     20 * time.Millisecond,
			CheckpointInterval: 5 * time.Millisecond,
			RetryInterval:      5 * time.Millisecond,
			SetReady: func(ready bool) {
				readyTransitions <- ready
			},
		},
	)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !firstWatchHadBoundedDeadline.Load() {
		t.Fatal("Watch connection did not receive the configured short client deadline")
	}
	if watchCalls.Load() < 2 {
		t.Fatalf("watch calls=%d, blocked Watch connection was not retried", watchCalls.Load())
	}
	close(readyTransitions)
	var sawReady, sawNotReady bool
	for ready := range readyTransitions {
		if ready {
			sawReady = true
		} else {
			sawNotReady = true
		}
	}
	if !sawReady {
		t.Fatal("initial checkpoint did not open the readiness gate")
	}
	if sawNotReady {
		t.Fatal("runtime Watch connection failure reset the startup readiness gate")
	}
}

func TestListSnapshotMillionPodsRetainsOnlyDeletingSubset(t *testing.T) {
	if testing.Short() {
		t.Skip("million-Pod streaming regression")
	}
	const (
		pageSize  = int64(1_000)
		pageCount = 1_000
	)
	now := time.Unix(10_000, 0)
	calls := 0
	client := &scriptedPodClient{
		list: func(_ context.Context, option metav1.ListOptions) (*corev1.PodList, error) {
			calls++
			page := calls - 1
			items := make([]corev1.Pod, pageSize)
			for index := range items {
				uid := fmt.Sprintf("%d-%d", page, index)
				items[index] = corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID(uid)}}
			}
			if page == 0 {
				items[0] = deletingPod("kept", "ns", "pod", "node", now.Add(-time.Minute), 0)
			}
			next := ""
			if calls < pageCount {
				next = fmt.Sprintf("page-%d", calls)
			}
			return &corev1.PodList{
				ListMeta: metav1.ListMeta{ResourceVersion: "42", Continue: next},
				Items:    items,
			}, nil
		},
	}
	snapshot, _, err := ListSnapshot(context.Background(), client, pageSize, time.Second)
	if err != nil {
		t.Fatalf("ListSnapshot: %v", err)
	}
	if calls != pageCount {
		t.Fatalf("list calls=%d, want %d", calls, pageCount)
	}
	if len(snapshot) != 1 {
		t.Fatalf("retained=%d, want only the deleting subset", len(snapshot))
	}
}

func BenchmarkListSnapshotMillionPods(b *testing.B) {
	const (
		pageSize  = int64(1_000)
		pageCount = 1_000
	)
	for iteration := 0; iteration < b.N; iteration++ {
		calls := 0
		client := &scriptedPodClient{
			list: func(_ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
				calls++
				items := make([]corev1.Pod, pageSize)
				next := ""
				if calls < pageCount {
					next = fmt.Sprintf("page-%d", calls)
				}
				return &corev1.PodList{
					ListMeta: metav1.ListMeta{ResourceVersion: "42", Continue: next},
					Items:    items,
				}, nil
			},
		}
		if _, _, err := ListSnapshot(context.Background(), client, pageSize, time.Second); err != nil {
			b.Fatal(err)
		}
	}
}
