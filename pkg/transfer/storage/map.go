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
	"context"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// MapStore :
type MapStore struct {
	*define.BaseStore
	data sync.Map
}

// Exists :
func (s *MapStore) Exists(key string) (bool, error) {
	_, ok := s.data.Load(key)
	return ok, nil
}

// Set :
func (s *MapStore) Set(key string, data []byte, expires time.Duration) error {
	s.data.Store(key, define.NewStoreItem(data, expires))
	return nil
}

// Get :
func (s *MapStore) Get(key string) ([]byte, error) {
	result, ok := s.data.Load(key)
	if !ok {
		return nil, define.ErrItemNotFound
	}
	data := result.(*define.StoreItem).GetData(true)
	if data == nil {
		return nil, define.ErrItemNotFound
	}
	return data, nil
}

// Delete :
func (s *MapStore) Delete(key string) error {
	s.data.Delete(key)
	return nil
}

// Commit :
func (s *MapStore) Commit() error {
	keys := make([]string, 0)
	s.data.Range(func(key, value interface{}) bool {
		item := value.(*define.StoreItem)
		if item.IsExpired() {
			keys = append(keys, key.(string))
		}
		return true
	})
	for _, key := range keys {
		s.data.Delete(key)
	}
	return nil
}

// Scan :
func (s *MapStore) Scan(prefix string, callback define.StoreScanCallback, withAll ...bool) error {
	s.data.Range(func(key, value interface{}) bool {
		k := key.(string)
		if strings.HasPrefix(k, prefix) {
			data := value.(*define.StoreItem).GetData(true)
			if data != nil {
				if !callback(k, data) {
					return false
				}
			}
		}
		return true
	})
	return nil
}

// PutCache :
func (s *MapStore) PutCache(key string, data []byte, expires time.Duration) error {
	s.data.Store(key, define.NewStoreItem(data, expires))
	return nil
}

// Batch :
func (s *MapStore) Batch() error {
	return nil
}

// NewMapStore :
func NewMapStore() *MapStore {
	return &MapStore{
		BaseStore: define.NewBaseStore(),
	}
}

func init() {
	define.RegisterStore("memory", func(ctx context.Context, name string) (define.Store, error) {
		return NewMapStore(), nil
	})
}
