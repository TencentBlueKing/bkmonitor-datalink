// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podterminatingreporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
)

func deletingPod(namespace, name, node string, deletionTime time.Time, grace int64) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:                  namespace,
			Name:                       name,
			DeletionTimestamp:          &metav1.Time{Time: deletionTime},
			DeletionGracePeriodSeconds: &grace,
		},
		Spec: corev1.PodSpec{NodeName: node},
	}
}

func TestPodToMetricRowUsesDeletionRequestTime(t *testing.T) {
	now := time.Date(2026, 7, 28, 10, 2, 30, 0, time.UTC)
	pod := deletingPod("default", "example", "node-1", time.Date(2026, 7, 28, 10, 0, 30, 123456000, time.UTC), 30)

	row, ok := PodToMetricRow(&pod, now)

	require.True(t, ok)
	require.Equal(t, testRow(149), row)
	pod.DeletionTimestamp = nil
	_, ok = PodToMetricRow(&pod, now)
	require.False(t, ok)
}

func TestListPodRowsPaginatesWithoutKeepingPodPages(t *testing.T) {
	client := fake.NewSimpleClientset()
	var mu sync.Mutex
	var options []metav1.ListOptions
	page := 0
	client.PrependReactor("list", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		listAction := action.(clienttesting.ListActionImpl)
		options = append(options, listAction.GetListOptions())
		page++
		switch page {
		case 1:
			pod := deletingPod("default", "one", "node-1", time.Unix(-7200, 0), 0)
			return true, &corev1.PodList{
				ListMeta: metav1.ListMeta{Continue: "a+b/c=="},
				Items:    []corev1.Pod{pod},
			}, nil
		case 2:
			pod := deletingPod("default", "two", "node-2", time.Unix(-7200, 0), 0)
			return true, &corev1.PodList{Items: []corev1.Pod{pod}}, nil
		default:
			t.Fatalf("unexpected page %d", page)
			return true, nil, nil
		}
	})

	rows, err := ListPodRows(context.Background(), client, 200, 15*time.Second, time.Unix(0, 0))

	require.NoError(t, err)
	require.Equal(t, []MetricRow{
		{Namespace: "default", Pod: "one", Node: "node-1", Seconds: 7200},
		{Namespace: "default", Pod: "two", Node: "node-2", Seconds: 7200},
	}, rows)
	require.Equal(t, []metav1.ListOptions{
		{Limit: 200},
		{Limit: 200, Continue: "a+b/c=="},
	}, options)
}

func TestListPodRowsDoesNotApplyAlertThreshold(t *testing.T) {
	pod := deletingPod(
		"default",
		"short-lived",
		"node-1",
		time.Unix(-30, 0),
		0,
	)
	client := fake.NewSimpleClientset(&pod)

	rows, err := ListPodRows(
		context.Background(),
		client,
		200,
		15*time.Second,
		time.Unix(0, 0),
	)

	require.NoError(t, err)
	require.Equal(t, []MetricRow{{
		Namespace: "default",
		Pod:       "short-lived",
		Node:      "node-1",
		Seconds:   30,
	}}, rows)
}

func TestListPodRowsAppliesTimeoutToEachPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	client, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)

	started := time.Now()
	_, err = ListPodRows(context.Background(), client, 200, 30*time.Millisecond, time.Now())

	require.Error(t, err)
	require.Less(t, time.Since(started), time.Second)
}

func TestListPodRowsKeeps4019ShortLivedDeletingPods(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "state"},
		Data:       map[string]string{StateDataKey: `{"version":2,"active":[],"recovery":[]}`},
	})
	const total = 4019
	page := 0
	client.PrependReactor("list", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		start := page * 200
		end := min(start+200, total)
		items := make([]corev1.Pod, 0, end-start)
		for index := start; index < end; index++ {
			items = append(items, deletingPod("default", fmt.Sprintf("pod-%04d", index), "node-1", time.Unix(-30, 0), 0))
		}
		page++
		continueToken := ""
		if end < total {
			continueToken = strconv.Itoa(page)
		}
		return true, &corev1.PodList{
			ListMeta: metav1.ListMeta{Continue: continueToken},
			Items:    items,
		}, nil
	})

	rows, err := ListPodRows(context.Background(), client, 200, 15*time.Second, time.Unix(0, 0))

	require.NoError(t, err)
	require.Len(t, rows, total)
	require.Equal(t, int64(30), rows[0].Seconds)
	require.Equal(t, int64(30), rows[total-1].Seconds)
	require.Equal(t, 21, page)

	store, err := NewStateStore(client, "default", "state", 15*time.Second, HardMaxStateBytes)
	require.NoError(t, err)
	state := NewState(10*time.Minute, 3*time.Minute)
	require.NoError(t, state.ApplySuccess(context.Background(), rows, time.Unix(0, 0), store.Save))
	require.Equal(t, total, state.MetricsSnapshot(time.Unix(0, 0)).ActiveEntries)
	require.Greater(t, state.MetricsSnapshot(time.Unix(0, 0)).StateBytes, 39)
	require.LessOrEqual(t, state.MetricsSnapshot(time.Unix(0, 0)).StateBytes, HardMaxStateBytes)
}

func TestListPodRowsRejectsRepeatedContinueToken(t *testing.T) {
	client := fake.NewSimpleClientset()
	calls := 0
	client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		calls++
		return true, &corev1.PodList{ListMeta: metav1.ListMeta{Continue: "same-token"}}, nil
	})

	_, err := ListPodRows(context.Background(), client, 200, time.Second, time.Now())

	require.ErrorIs(t, err, errRepeatedContinueToken)
	require.Equal(t, 2, calls)
}

func TestRefreshOnceDoesNotCommitPartialLaterPage(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	oldNow := time.Unix(900, 0)
	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{
		{Namespace: "default", Pod: "old", Node: "node-1", Seconds: 7200},
	}, oldNow, nil))

	client := fake.NewSimpleClientset()
	page := 0
	client.PrependReactor("list", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		page++
		if page == 1 {
			pod := deletingPod("default", "partial", "node-2", time.Unix(-7200, 0), 0)
			return true, &corev1.PodList{
				ListMeta: metav1.ListMeta{Continue: "next"},
				Items:    []corev1.Pod{pod},
			}, nil
		}
		return true, nil, errors.New("second page failed")
	})
	store, err := NewStateStore(client, "bkmonitor-operator", "state", 15*time.Second, HardMaxStateBytes)
	require.NoError(t, err)

	err = RefreshOnce(context.Background(), state, store, client, 200, 15*time.Second, time.Unix(1000, 0))

	require.ErrorContains(t, err, "second page failed")
	metrics := state.MetricsSnapshot(time.Unix(1000, 0))
	require.Equal(t, float64(900), metrics.LastSuccessTimestamp)
	require.Equal(t, []MetricRow{{Namespace: "default", Pod: "old", Node: "node-1", Seconds: 7200}}, metrics.Rows)
	require.Equal(t, float64(0), metrics.RefreshSuccess)
}

func TestRefreshObservabilityCountsOnlyKubernetesRequestErrors(t *testing.T) {
	t.Run("list Pods", func(t *testing.T) {
		state := NewState(10*time.Minute, 3*time.Minute)
		client := fake.NewSimpleClientset()
		client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("list transport failed")
		})
		store, err := NewStateStore(client, "default", "state", time.Second, HardMaxStateBytes)
		require.NoError(t, err)

		err = RefreshOnce(context.Background(), state, store, client, 200, time.Second, time.Now())

		require.Error(t, err)
		metrics := state.MetricsSnapshot(time.Now())
		require.Positive(t, metrics.RefreshDurationSeconds)
		require.Equal(t, float64(1), metrics.KubernetesAPIErrors[OperationListPods])
		require.Equal(t, float64(0), metrics.KubernetesAPIErrors[OperationGetState])
		require.Equal(t, float64(0), metrics.KubernetesAPIErrors[OperationPatchState])
	})

	t.Run("patch state", func(t *testing.T) {
		state := NewState(10*time.Minute, 3*time.Minute)
		client := fake.NewSimpleClientset(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "state"},
			Data:       map[string]string{StateDataKey: `{"version":2,"active":[],"recovery":[]}`},
		})
		client.PrependReactor("patch", "configmaps", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("patch transport failed")
		})
		store, err := NewStateStore(client, "default", "state", time.Second, HardMaxStateBytes)
		require.NoError(t, err)

		err = RefreshOnce(context.Background(), state, store, client, 200, time.Second, time.Now())

		require.Error(t, err)
		metrics := state.MetricsSnapshot(time.Now())
		require.Positive(t, metrics.RefreshDurationSeconds)
		require.Equal(t, float64(1), metrics.KubernetesAPIErrors[OperationPatchState])
	})

	t.Run("get state and local validation", func(t *testing.T) {
		state := NewState(10*time.Minute, 3*time.Minute)
		client := fake.NewSimpleClientset()
		client.PrependReactor("get", "configmaps", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("get transport failed")
		})
		store, err := NewStateStore(client, "default", "state", time.Second, HardMaxStateBytes)
		require.NoError(t, err)
		_, _, err = store.Load(context.Background())
		state.ObserveKubernetesAPIError(err)
		require.Equal(t, float64(1), state.MetricsSnapshot(time.Now()).KubernetesAPIErrors[OperationGetState])

		localError := fmt.Errorf("validate state: %w", errors.New("invalid JSON"))
		state.ObserveKubernetesAPIError(localError)
		require.Equal(t, float64(1), state.MetricsSnapshot(time.Now()).KubernetesAPIErrors[OperationGetState])
	})
}

func TestServerAppliedPatchTimeoutDoesNotExtendRecoveryAfterNextRestart(t *testing.T) {
	source := NewState(10*time.Minute, 3*time.Minute)
	var sourceSnapshots []Snapshot
	require.NoError(t, source.ApplySuccess(
		context.Background(),
		[]MetricRow{testRow(7200)},
		time.Unix(1000, 0),
		persistSnapshots(&sourceSnapshots),
	))
	require.NoError(t, source.ApplySuccess(
		context.Background(),
		nil,
		time.Unix(1060, 0),
		persistSnapshots(&sourceSnapshots),
	))
	baseline := sourceSnapshots[len(sourceSnapshots)-1]
	require.False(t, baseline.Recovery[0].RestartExtensionUsed)
	require.Equal(t, float64(1660), baseline.Recovery[0].ExpiresAt)

	firstRestart := NewState(10*time.Minute, 3*time.Minute)
	baselineRaw, err := MarshalSnapshot(baseline)
	require.NoError(t, err)
	require.NoError(t, firstRestart.Restore(baseline, len(baselineRaw), time.Unix(1300, 0)))

	client := fake.NewSimpleClientset()
	var serverAppliedRaw string
	client.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		var patch struct {
			Data map[string]string `json:"data"`
		}
		require.NoError(t, json.Unmarshal(action.(clienttesting.PatchAction).GetPatch(), &patch))
		serverAppliedRaw = patch.Data[StateDataKey]
		return true, nil, context.DeadlineExceeded
	})
	store, err := NewStateStore(client, "default", "state", time.Second, HardMaxStateBytes)
	require.NoError(t, err)

	err = RefreshOnce(
		context.Background(),
		firstRestart,
		store,
		client,
		200,
		time.Second,
		time.Unix(1300, 0),
	)

	require.Error(t, err)
	var apiError *KubernetesAPIError
	require.ErrorAs(t, err, &apiError)
	require.Equal(t, OperationPatchState, apiError.Operation)
	require.Equal(t, float64(1), firstRestart.MetricsSnapshot(time.Unix(1301, 0)).KubernetesAPIErrors[OperationPatchState])
	require.NotEmpty(t, serverAppliedRaw)
	serverApplied, err := UnmarshalSnapshot([]byte(serverAppliedRaw), HardMaxStateBytes)
	require.NoError(t, err)
	require.True(t, serverApplied.Recovery[0].RestartExtensionUsed)
	require.Equal(t, float64(1900), serverApplied.Recovery[0].ExpiresAt)

	secondRestart := NewState(10*time.Minute, 3*time.Minute)
	require.NoError(t, secondRestart.Restore(serverApplied, len(serverAppliedRaw), time.Unix(1400, 0)))
	var secondRestartSnapshots []Snapshot
	require.NoError(t, secondRestart.ApplySuccess(
		context.Background(),
		nil,
		time.Unix(1400, 0),
		persistSnapshots(&secondRestartSnapshots),
	))
	require.True(t, secondRestartSnapshots[0].Recovery[0].RestartExtensionUsed)
	require.Equal(t, float64(1900), secondRestartSnapshots[0].Recovery[0].ExpiresAt)

	thirdRestart := NewState(10*time.Minute, 3*time.Minute)
	secondRaw, err := MarshalSnapshot(secondRestartSnapshots[0])
	require.NoError(t, err)
	require.NoError(t, thirdRestart.Restore(secondRestartSnapshots[0], len(secondRaw), time.Unix(1901, 0)))
	require.NoError(t, thirdRestart.ApplySuccess(context.Background(), nil, time.Unix(1901, 0), nil))
	require.Empty(t, thirdRestart.MetricsSnapshot(time.Unix(1902, 0)).Rows)
}

func TestStateStoreLoadPatchAndSizeLimit(t *testing.T) {
	raw := `{"version":2,"active":[],"recovery":[]}`
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "bkmonitor-operator", Name: "state"},
		Data:       map[string]string{StateDataKey: raw},
	})
	store, err := NewStateStore(client, "bkmonitor-operator", "state", 15*time.Second, HardMaxStateBytes)
	require.NoError(t, err)

	snapshot, stateBytes, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, StateVersion, snapshot.Version)
	require.Equal(t, len(raw), stateBytes)

	snapshot.Active = []Dimension{{Namespace: "default", Pod: "example", Node: "node-1"}}
	savedBytes, err := store.Save(context.Background(), snapshot)
	require.NoError(t, err)
	expected, err := MarshalSnapshot(snapshot)
	require.NoError(t, err)
	require.Equal(t, len(expected), savedBytes)

	var patchAction clienttesting.PatchAction
	for _, action := range client.Actions() {
		if action.GetVerb() == "patch" {
			patchAction = action.(clienttesting.PatchAction)
		}
	}
	require.NotNil(t, patchAction)
	require.Equal(t, types.MergePatchType, patchAction.GetPatchType())
	var patch map[string]map[string]string
	require.NoError(t, json.Unmarshal(patchAction.GetPatch(), &patch))
	require.Equal(t, string(expected), patch["data"][StateDataKey])

	limited, err := NewStateStore(client, "bkmonitor-operator", "state", 15*time.Second, 10)
	require.NoError(t, err)
	_, err = limited.Save(context.Background(), snapshot)
	require.ErrorContains(t, err, "exceeds")
	_, err = NewStateStore(client, "bkmonitor-operator", "state", 15*time.Second, HardMaxStateBytes+1)
	require.ErrorContains(t, err, "hard limit")
}

func TestStateStoreFailsClosedOnMissingOrMalformedState(t *testing.T) {
	for name, data := range map[string]map[string]string{
		"missing":   {},
		"empty":     {StateDataKey: ""},
		"malformed": {StateDataKey: `{"version":1,"active":[],"recovery":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			client := fake.NewSimpleClientset(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "bkmonitor-operator", Name: "state"},
				Data:       data,
			})
			store, err := NewStateStore(client, "bkmonitor-operator", "state", 15*time.Second, HardMaxStateBytes)
			require.NoError(t, err)
			_, _, err = store.Load(context.Background())
			require.Error(t, err)
		})
	}
}

func TestEnsureStateConfigMapCreateValidateAndConflictReadback(t *testing.T) {
	t.Run("create absent", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		require.NoError(t, EnsureStateConfigMap(context.Background(), client, "ns", "state", 15*time.Second, HardMaxStateBytes))
		created, err := client.CoreV1().ConfigMaps("ns").Get(context.Background(), "state", metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, `{"version":2,"active":[],"recovery":[]}`, created.Data[StateDataKey])
	})

	t.Run("validate without overwrite", func(t *testing.T) {
		raw := `{"version":2,"active":[{"namespace":"n","pod":"p","node":"x"}],"recovery":[]}`
		client := fake.NewSimpleClientset(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "state"},
			Data:       map[string]string{StateDataKey: raw, "owned": "keep"},
		})
		require.NoError(t, EnsureStateConfigMap(context.Background(), client, "ns", "state", 15*time.Second, HardMaxStateBytes))
		require.Len(t, client.Actions(), 1)
	})

	t.Run("conflict then get", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		gets := 0
		client.PrependReactor("get", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
			gets++
			if gets == 1 {
				return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "state")
			}
			return true, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "state"},
				Data:       map[string]string{StateDataKey: `{"version":2,"active":[],"recovery":[]}`},
			}, nil
		})
		client.PrependReactor("create", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "configmaps"}, "state")
		})
		require.NoError(t, EnsureStateConfigMap(context.Background(), client, "ns", "state", 15*time.Second, HardMaxStateBytes))
		require.Equal(t, 2, gets)
	})

	t.Run("invalid existing blocks", func(t *testing.T) {
		client := fake.NewSimpleClientset(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "state"},
			Data:       map[string]string{StateDataKey: `{"version":1,"active":[],"recovery":[]}`},
		})
		err := EnsureStateConfigMap(context.Background(), client, "ns", "state", 15*time.Second, HardMaxStateBytes)
		require.Error(t, err)
		require.Len(t, client.Actions(), 1)
	})
}

func BenchmarkRefreshOnce4019PodsPaged(b *testing.B) {
	const (
		total     = 4019
		pageLimit = 200
	)
	now := time.Unix(30, 0)
	pages := make([][]byte, 0, 21)
	for start := 0; start < total; start += pageLimit {
		end := min(start+pageLimit, total)
		items := make([]corev1.Pod, 0, end-start)
		for index := start; index < end; index++ {
			items = append(items, deletingPod(
				"benchmark",
				fmt.Sprintf("pod-%04d", index),
				fmt.Sprintf("node-%03d", index%100),
				time.Unix(0, 0),
				0,
			))
		}
		continueToken := ""
		if end < total {
			continueToken = strconv.Itoa(len(pages) + 1)
		}
		raw, err := json.Marshal(corev1.PodList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
			ListMeta: metav1.ListMeta{Continue: continueToken},
			Items:    items,
		})
		if err != nil {
			b.Fatal(err)
		}
		pages = append(pages, raw)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/pods":
			if request.URL.Query().Get("limit") != strconv.Itoa(pageLimit) {
				http.Error(response, "unexpected limit", http.StatusBadRequest)
				return
			}
			page := 0
			if token := request.URL.Query().Get("continue"); token != "" {
				var err error
				page, err = strconv.Atoi(token)
				if err != nil || page >= len(pages) {
					http.Error(response, "unexpected continue token", http.StatusBadRequest)
					return
				}
			}
			_, _ = response.Write(pages[page])
		case request.Method == http.MethodPatch &&
			request.URL.Path == "/api/v1/namespaces/benchmark/configmaps/state":
			_, _ = io.Copy(io.Discard, request.Body)
			_, _ = response.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"namespace":"benchmark","name":"state"}}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		b.Fatal(err)
	}
	store, err := NewStateStore(client, "benchmark", "state", 15*time.Second, HardMaxStateBytes)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		state := NewState(10*time.Minute, 3*time.Minute)
		if err := RefreshOnce(
			context.Background(),
			state,
			store,
			client,
			pageLimit,
			15*time.Second,
			now,
		); err != nil {
			b.Fatal(err)
		}
		capacity := state.MetricsSnapshot(now)
		if capacity.ActiveEntries != total || capacity.StateBytes == 0 {
			b.Fatalf("unexpected persisted capacity: %+v", capacity)
		}
	}
}
