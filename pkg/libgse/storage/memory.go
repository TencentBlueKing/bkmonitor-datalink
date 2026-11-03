// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrStorageClosed is returned when a closed memory storage is used again.
var ErrStorageClosed = errors.New("storage is closed")

// MemoryStorage is an in-memory Storage implementation. It is selected by the
// -T startup path so that diagnostics do not create or open the local progress
// database. Values are process local and are discarded when the process exits.
type MemoryStorage struct {
	mu     sync.RWMutex
	values map[string]string
	closed bool
}

var _ Storage = (*MemoryStorage)(nil)

// NewMemoryStorage creates an empty in-memory storage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{values: make(map[string]string)}
}

// InitMemory initializes the package level storage with a MemoryStorage. It is
// an explicit choice of implementation instead of a nil-storage fallback. If a
// previous storage is still registered it is closed, so repeated initialization
// does not leak a local database or its background flusher.
func InitMemory() {
	previous := _storage
	_storage = NewMemoryStorage()
	if previous != nil {
		_ = previous.Close()
	}
}

// Set stores value by key. Empty key or value is ignored, which keeps the same
// rule as the package level Set helper.
func (s *MemoryStorage) Set(key, value string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStorageClosed
	}
	if len(key) == 0 || len(value) == 0 {
		return nil
	}
	s.values[key] = value
	return nil
}

// Get returns the value of key, or ErrNotFound when the key does not exist.
func (s *MemoryStorage) Get(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return "", ErrStorageClosed
	}

	value, ok := s.values[key]
	if !ok || len(value) == 0 {
		return "", ErrNotFound
	}
	return value, nil
}

// List returns a snapshot of the key/value pairs with the given prefix. The
// returned map is owned by the caller and never exposes the internal map.
func (s *MemoryStorage) List(prefix string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStorageClosed
	}

	values := make(map[string]string)
	for key, value := range s.values {
		if strings.HasPrefix(key, prefix) {
			values[key] = value
		}
	}
	return values, nil
}

// Del removes key. Deleting a missing key succeeds.
func (s *MemoryStorage) Del(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStorageClosed
	}
	delete(s.values, key)
	return nil
}

// SetFlushInterval is a no-op: memory values never need flushing and there is
// no background ticker.
func (s *MemoryStorage) SetFlushInterval(_ time.Duration) {}

// Close discards all values and marks this storage closed. Close can be called
// multiple times; data operations after Close return ErrStorageClosed.
func (s *MemoryStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	s.values = nil
	return nil
}

// Destroy closes this storage and drops its contents. It never removes files or
// directories.
func (s *MemoryStorage) Destroy() error {
	return s.Close()
}
