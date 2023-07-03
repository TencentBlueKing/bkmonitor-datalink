// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"context"
	"time"
)

const (
	StoreNoExpires time.Duration = 0
	StoreFlag                    = "bootstrap_update"
)

// BaseStore :
type BaseStore struct{}

// Set :
func (s *BaseStore) Set(key string, data []byte, expires time.Duration) error {
	return ErrNotImplemented
}

// Get :
func (s *BaseStore) Get(key string) ([]byte, error) {
	return nil, ErrNotImplemented
}

// Exists :
func (s *BaseStore) Exists(key string) (bool, error) {
	return false, ErrNotImplemented
}

// Delete :
func (s *BaseStore) Delete(key string) error {
	return ErrNotImplemented
}

// Close :
func (s *BaseStore) Close() error {
	return nil
}

// Commit :
func (s *BaseStore) Commit() error {
	return nil
}

// Scan :
func (s *BaseStore) Scan(prefix string, callback StoreScanCallback, withAll ...bool) error {
	return ErrNotImplemented
}

// PutCache :
func (s *BaseStore) PutCache(key string, data []byte, expires time.Duration) error {
	return ErrNotImplemented
}

// Batch :
func (s *BaseStore) Batch() error {
	return ErrNotImplemented
}

// NewBaseStore :
func NewBaseStore() *BaseStore {
	return &BaseStore{}
}

// StoreIntoContext :
func StoreIntoContext(ctx context.Context, store Store) context.Context {
	return context.WithValue(ctx, ContextStoreKey, store)
}

// StoreFromContext :
func StoreFromContext(ctx context.Context) Store {
	v := ctx.Value(ContextStoreKey)
	store, ok := v.(Store)
	if !ok {
		return nil
	}
	return store
}

var exposeStoreMap = make(map[string]Store)

// ExposeStore:
func ExposeStore(s Store, storeType string) {
	exposeStoreMap[storeType] = s
}

// GetStore:
func GetStore(t string) (s Store, storeType string) {
	if t != "" {
		return exposeStoreMap[t], t
	}
	for key, s := range exposeStoreMap {
		return s, key
	}
	return nil, t
}

// StoreItem :
type StoreItem struct {
	Data      []byte     `json:"data"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// SetExpires :
func (s *StoreItem) SetExpires(t time.Duration) {
	if t != StoreNoExpires {
		now := time.Now()
		expiresAt := now.Add(t)
		s.ExpiresAt = &expiresAt
	} else {
		s.ExpiresAt = nil
	}
}

// SetExpiresAt :
func (s *StoreItem) SetExpiresAt(t time.Time) {
	s.ExpiresAt = &t
}

// IsExpired :
func (s *StoreItem) IsExpired() bool {
	if s.ExpiresAt == nil {
		return false
	}
	now := time.Now()
	return now.After(*s.ExpiresAt)
}

// GetData :
func (s *StoreItem) GetData(copies bool) []byte {
	if !copies {
		return s.Data
	}

	result := make([]byte, len(s.Data))
	copy(result, s.Data)
	return result
}

// Update :
func (s *StoreItem) Update(data []byte, expires time.Duration) {
	s.Data = data
	s.SetExpires(expires)
}

// NewStoreItem :
func NewStoreItem(data []byte, expires time.Duration) *StoreItem {
	item := &StoreItem{
		Data: data,
	}
	item.SetExpires(expires)
	return item
}

// RespCacheData:
type RespCacheData struct {
	Result  bool                  `json:"result"`
	Data    map[string]*StoreItem `json:"data"`
	Message string                `json:"message,omitempty"`
}
