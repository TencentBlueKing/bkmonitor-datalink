// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package podterminating

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type ConfigMapClient interface {
	Get(context.Context, string, metav1.GetOptions) (*corev1.ConfigMap, error)
	Patch(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*corev1.ConfigMap, error)
}

type BootstrapConfigMapClient interface {
	Get(context.Context, string, metav1.GetOptions) (*corev1.ConfigMap, error)
	Create(context.Context, *corev1.ConfigMap, metav1.CreateOptions) (*corev1.ConfigMap, error)
}

type pendingWrite struct {
	snapshot Snapshot
	raw      []byte
}

type StateStore struct {
	client         ConfigMapClient
	name           string
	requestTimeout time.Duration
	maxBytes       int

	mu      sync.Mutex
	pending *pendingWrite
}

func NewStateStore(
	client ConfigMapClient,
	name string,
	requestTimeout time.Duration,
	maxBytes int,
) (*StateStore, error) {
	if client == nil {
		return nil, fmt.Errorf("ConfigMap client must not be nil")
	}
	if name == "" {
		return nil, fmt.Errorf("state ConfigMap name must not be empty")
	}
	if requestTimeout <= 0 {
		return nil, fmt.Errorf("request timeout must be positive")
	}
	if maxBytes <= 0 || maxBytes > HardMaxStateBytes {
		return nil, fmt.Errorf("state max bytes %d is outside (0,%d]", maxBytes, HardMaxStateBytes)
	}
	return &StateStore{
		client:         client,
		name:           name,
		requestTimeout: requestTimeout,
		maxBytes:       maxBytes,
	}, nil
}

func (s *StateStore) Load(ctx context.Context) (Snapshot, int, error) {
	configMap, err := s.get(ctx)
	if err != nil {
		return Snapshot{}, 0, fmt.Errorf("get state ConfigMap %s: %w", s.name, err)
	}
	raw, exists := configMap.Data[StateDataKey]
	if !exists || raw == "" {
		return Snapshot{}, 0, fmt.Errorf("state ConfigMap %s must contain non-empty %s", s.name, StateDataKey)
	}
	snapshot, err := UnmarshalSnapshot([]byte(raw), s.maxBytes)
	if err != nil {
		return Snapshot{}, 0, fmt.Errorf("validate state ConfigMap %s: %w", s.name, err)
	}
	canonical, err := MarshalSnapshot(snapshot)
	if err != nil {
		return Snapshot{}, 0, err
	}
	return snapshot, len(canonical), nil
}

func (s *StateStore) Save(ctx context.Context, snapshot Snapshot) (int, error) {
	raw, err := MarshalSnapshot(snapshot)
	if err != nil {
		return 0, err
	}
	if len(raw) > s.maxBytes {
		return 0, fmt.Errorf("persisted state size %d exceeds limit %d", len(raw), s.maxBytes)
	}

	s.mu.Lock()
	if s.pending != nil {
		s.mu.Unlock()
		return 0, fmt.Errorf("previous state PATCH outcome must be resolved before another write")
	}
	s.mu.Unlock()

	patch, err := json.Marshal(map[string]map[string]string{
		"data": {StateDataKey: string(raw)},
	})
	if err != nil {
		return 0, fmt.Errorf("marshal state ConfigMap patch: %w", err)
	}
	requestContext, cancel := context.WithTimeout(ctx, s.requestTimeout)
	_, patchErr := s.client.Patch(
		requestContext,
		s.name,
		types.MergePatchType,
		patch,
		metav1.PatchOptions{},
	)
	cancel()
	if patchErr == nil {
		return len(raw), nil
	}

	equal, readErr := s.readbackEquals(ctx, raw)
	if readErr == nil && equal {
		return len(raw), nil
	}
	if readErr != nil {
		s.mu.Lock()
		s.pending = &pendingWrite{snapshot: snapshot, raw: append([]byte(nil), raw...)}
		s.mu.Unlock()
		return 0, fmt.Errorf(
			"patch state ConfigMap %s failed and readback is ambiguous: %w",
			s.name,
			errors.Join(patchErr, readErr),
		)
	}
	return 0, fmt.Errorf("patch state ConfigMap %s failed and readback differs: %w", s.name, patchErr)
}

func (s *StateStore) ResolvePending(ctx context.Context) (Snapshot, bool, error) {
	s.mu.Lock()
	pending := s.pending
	s.mu.Unlock()
	if pending == nil {
		return Snapshot{}, false, nil
	}
	equal, err := s.readbackEquals(ctx, pending.raw)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("confirm previous state PATCH: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != pending {
		return Snapshot{}, false, fmt.Errorf("pending state PATCH changed during confirmation")
	}
	s.pending = nil
	if !equal {
		return Snapshot{}, false, nil
	}
	return pending.snapshot, true, nil
}

func (s *StateStore) get(ctx context.Context) (*corev1.ConfigMap, error) {
	requestContext, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()
	return s.client.Get(requestContext, s.name, metav1.GetOptions{})
}

func (s *StateStore) readbackEquals(ctx context.Context, raw []byte) (bool, error) {
	configMap, err := s.get(ctx)
	if err != nil {
		return false, err
	}
	return configMap.Data[StateDataKey] == string(raw), nil
}

func EnsureStateConfigMap(
	ctx context.Context,
	client BootstrapConfigMapClient,
	name string,
	requestTimeout time.Duration,
	maxBytes int,
) error {
	if client == nil {
		return fmt.Errorf("ConfigMap client must not be nil")
	}
	if name == "" {
		return fmt.Errorf("state ConfigMap name must not be empty")
	}
	if requestTimeout <= 0 {
		return fmt.Errorf("request timeout must be positive")
	}
	if maxBytes <= 0 || maxBytes > HardMaxStateBytes {
		return fmt.Errorf("state max bytes %d is outside (0,%d]", maxBytes, HardMaxStateBytes)
	}

	get := func() (*corev1.ConfigMap, error) {
		requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		return client.Get(requestContext, name, metav1.GetOptions{})
	}
	configMap, err := get()
	if err == nil {
		return validateExistingStateConfigMap(configMap, maxBytes)
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get state ConfigMap %s: %w", name, err)
	}

	raw, err := MarshalSnapshot(Snapshot{Version: StateVersion})
	if err != nil {
		return err
	}
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	_, createErr := client.Create(requestContext, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Data:       map[string]string{StateDataKey: string(raw)},
	}, metav1.CreateOptions{})
	cancel()
	if createErr == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create state ConfigMap %s: %w", name, createErr)
	}
	configMap, err = get()
	if err != nil {
		return fmt.Errorf("read state ConfigMap %s after create race: %w", name, err)
	}
	return validateExistingStateConfigMap(configMap, maxBytes)
}

func validateExistingStateConfigMap(configMap *corev1.ConfigMap, maxBytes int) error {
	if configMap == nil {
		return fmt.Errorf("state ConfigMap response must not be nil")
	}
	raw, exists := configMap.Data[StateDataKey]
	if !exists || raw == "" {
		return fmt.Errorf("state ConfigMap %s must contain non-empty %s", configMap.Name, StateDataKey)
	}
	if _, err := UnmarshalSnapshot([]byte(raw), maxBytes); err != nil {
		return fmt.Errorf("validate state ConfigMap %s: %w", configMap.Name, err)
	}
	return nil
}
