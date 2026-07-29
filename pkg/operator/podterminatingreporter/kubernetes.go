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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const StateDataKey = "state.json"

var errRepeatedContinueToken = errors.New("Kubernetes API repeated Pod continue token")

const (
	OperationListPods   = "list_pods"
	OperationGetState   = "get_state"
	OperationPatchState = "patch_state"
)

var kubernetesAPIOperations = [...]string{
	OperationListPods,
	OperationGetState,
	OperationPatchState,
}

type KubernetesAPIError struct {
	Operation string
	Err       error
}

func (e *KubernetesAPIError) Error() string {
	return fmt.Sprintf("Kubernetes API %s failed: %v", e.Operation, e.Err)
}

func (e *KubernetesAPIError) Unwrap() error {
	return e.Err
}

func PodToMetricRow(pod *corev1.Pod, now time.Time) (MetricRow, bool) {
	if pod.DeletionTimestamp == nil {
		return MetricRow{}, false
	}
	graceSeconds := int64(0)
	if pod.DeletionGracePeriodSeconds != nil && *pod.DeletionGracePeriodSeconds > 0 {
		graceSeconds = *pod.DeletionGracePeriodSeconds
	}
	requestTime := pod.DeletionTimestamp.Time.Add(-time.Duration(graceSeconds) * time.Second)
	elapsed := int64(now.Sub(requestTime) / time.Second)
	if elapsed < 0 {
		elapsed = 0
	}
	return MetricRow{
		Namespace: pod.Namespace,
		Pod:       pod.Name,
		Node:      pod.Spec.NodeName,
		Seconds:   elapsed,
	}, true
}

func ListPodRows(
	ctx context.Context,
	client kubernetes.Interface,
	pageLimit int64,
	requestTimeout time.Duration,
	now time.Time,
) ([]MetricRow, error) {
	if pageLimit <= 0 {
		return nil, fmt.Errorf("page limit must be positive")
	}
	if requestTimeout <= 0 {
		return nil, fmt.Errorf("request timeout must be positive")
	}
	var rows []MetricRow
	continueToken := ""
	seenContinueTokens := make(map[string]struct{})
	for {
		pageContext, cancel := context.WithTimeout(ctx, requestTimeout)
		page, err := client.CoreV1().Pods("").List(pageContext, metav1.ListOptions{
			Limit:    pageLimit,
			Continue: continueToken,
		})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("list Pods page: %w", &KubernetesAPIError{
				Operation: OperationListPods,
				Err:       err,
			})
		}
		for index := range page.Items {
			if row, ok := PodToMetricRow(&page.Items[index], now); ok {
				rows = append(rows, row)
			}
		}
		if page.Continue == "" {
			return rows, nil
		}
		if _, exists := seenContinueTokens[page.Continue]; exists {
			return nil, fmt.Errorf("%w %q", errRepeatedContinueToken, page.Continue)
		}
		seenContinueTokens[page.Continue] = struct{}{}
		continueToken = page.Continue
	}
}

type StateStore struct {
	client         kubernetes.Interface
	namespace      string
	name           string
	requestTimeout time.Duration
	maxBytes       int
}

func NewStateStore(
	client kubernetes.Interface,
	namespace string,
	name string,
	requestTimeout time.Duration,
	maxBytes int,
) (*StateStore, error) {
	if client == nil {
		return nil, fmt.Errorf("Kubernetes client must not be nil")
	}
	if namespace == "" {
		return nil, fmt.Errorf("state ConfigMap namespace must not be empty")
	}
	if name == "" {
		return nil, fmt.Errorf("state ConfigMap name must not be empty")
	}
	if requestTimeout <= 0 {
		return nil, fmt.Errorf("request timeout must be positive")
	}
	if maxBytes <= 0 || maxBytes > HardMaxStateBytes {
		return nil, fmt.Errorf("state max bytes %d exceeds hard limit %d or is not positive", maxBytes, HardMaxStateBytes)
	}
	return &StateStore{
		client:         client,
		namespace:      namespace,
		name:           name,
		requestTimeout: requestTimeout,
		maxBytes:       maxBytes,
	}, nil
}

func (s *StateStore) Load(ctx context.Context) (Snapshot, int, error) {
	requestContext, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()
	configMap, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(requestContext, s.name, metav1.GetOptions{})
	if err != nil {
		return Snapshot{}, 0, fmt.Errorf("get state ConfigMap %s/%s: %w", s.namespace, s.name, &KubernetesAPIError{
			Operation: OperationGetState,
			Err:       err,
		})
	}
	raw, exists := configMap.Data[StateDataKey]
	if !exists || raw == "" {
		return Snapshot{}, 0, fmt.Errorf("state ConfigMap %s/%s must contain non-empty %s", s.namespace, s.name, StateDataKey)
	}
	snapshot, err := UnmarshalSnapshot([]byte(raw), s.maxBytes)
	if err != nil {
		return Snapshot{}, 0, fmt.Errorf("validate state ConfigMap %s/%s: %w", s.namespace, s.name, err)
	}
	return snapshot, len([]byte(raw)), nil
}

func (s *StateStore) Save(ctx context.Context, snapshot Snapshot) (int, error) {
	raw, err := MarshalSnapshot(snapshot)
	if err != nil {
		return 0, fmt.Errorf("marshal state: %w", err)
	}
	if len(raw) > s.maxBytes {
		return 0, fmt.Errorf("persisted state size %d exceeds limit %d", len(raw), s.maxBytes)
	}
	patch, err := json.Marshal(map[string]map[string]string{
		"data": {StateDataKey: string(raw)},
	})
	if err != nil {
		return 0, fmt.Errorf("marshal ConfigMap patch: %w", err)
	}
	requestContext, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()
	_, err = s.client.CoreV1().ConfigMaps(s.namespace).Patch(
		requestContext,
		s.name,
		types.MergePatchType,
		patch,
		metav1.PatchOptions{},
	)
	if err != nil {
		return 0, fmt.Errorf("patch state ConfigMap %s/%s: %w", s.namespace, s.name, &KubernetesAPIError{
			Operation: OperationPatchState,
			Err:       err,
		})
	}
	return len(raw), nil
}

func RefreshOnce(
	ctx context.Context,
	state *State,
	store *StateStore,
	client kubernetes.Interface,
	pageLimit int64,
	requestTimeout time.Duration,
	now time.Time,
) error {
	started := time.Now()
	defer func() {
		state.SetRefreshDuration(time.Since(started))
	}()
	rows, err := ListPodRows(ctx, client, pageLimit, requestTimeout, now)
	if err != nil {
		state.ObserveKubernetesAPIError(err)
		state.MarkFailure()
		return err
	}
	if err := state.ApplySuccess(ctx, rows, now, store.Save); err != nil {
		state.ObserveKubernetesAPIError(err)
		state.MarkFailure()
		return err
	}
	return nil
}

func EnsureStateConfigMap(
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
	name string,
	requestTimeout time.Duration,
	maxBytes int,
) error {
	store, err := NewStateStore(client, namespace, name, requestTimeout, maxBytes)
	if err != nil {
		return err
	}

	configMap, err := getStateConfigMap(ctx, client, namespace, name, requestTimeout)
	if err == nil {
		return validateStateConfigMap(configMap, maxBytes)
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get state ConfigMap %s/%s: %w", namespace, name, err)
	}

	emptyRaw, err := MarshalSnapshot(Snapshot{Version: StateVersion})
	if err != nil {
		return err
	}
	if len(emptyRaw) > store.maxBytes {
		return fmt.Errorf("empty persisted state size %d exceeds limit %d", len(emptyRaw), store.maxBytes)
	}
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	_, createErr := client.CoreV1().ConfigMaps(namespace).Create(requestContext, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Data: map[string]string{StateDataKey: string(emptyRaw)},
	}, metav1.CreateOptions{})
	cancel()
	if createErr == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create state ConfigMap %s/%s: %w", namespace, name, createErr)
	}

	configMap, err = getStateConfigMap(ctx, client, namespace, name, requestTimeout)
	if err != nil {
		return fmt.Errorf("get state ConfigMap %s/%s after create conflict: %w", namespace, name, err)
	}
	return validateStateConfigMap(configMap, maxBytes)
}

func getStateConfigMap(
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
	name string,
	requestTimeout time.Duration,
) (*corev1.ConfigMap, error) {
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return client.CoreV1().ConfigMaps(namespace).Get(requestContext, name, metav1.GetOptions{})
}

func validateStateConfigMap(configMap *corev1.ConfigMap, maxBytes int) error {
	raw, exists := configMap.Data[StateDataKey]
	if !exists || raw == "" {
		return fmt.Errorf("state ConfigMap %s/%s must contain non-empty %s", configMap.Namespace, configMap.Name, StateDataKey)
	}
	if _, err := UnmarshalSnapshot([]byte(raw), maxBytes); err != nil {
		return fmt.Errorf("validate state ConfigMap %s/%s: %w", configMap.Namespace, configMap.Name, err)
	}
	return nil
}
