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

	"github.com/emirpasic/gods/maps"
	"github.com/emirpasic/gods/maps/linkedhashmap"
	"github.com/emirpasic/gods/maps/treemap"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// MapIterator :
type MapIterator interface {
	Next() bool
	Prev() bool
	Key() interface{}
	Value() interface{}
}

// Map :
type Map interface {
	maps.Map
	Iterator() MapIterator
}

// MemoryStore :
type MemoryStore struct {
	*define.BaseStore
	lock sync.RWMutex
	data Map
}

// NewMemoryStore :
func NewMemoryStore(data Map) define.Store {
	return &MemoryStore{
		BaseStore: define.NewBaseStore(),
		data:      data,
	}
}

// View :
func (s *MemoryStore) View(fn func() error) error {
	s.lock.RLock()
	err := fn()
	s.lock.RUnlock()
	return err
}

// Update :
func (s *MemoryStore) Update(fn func() error) error {
	s.lock.Lock()
	err := fn()
	s.lock.Unlock()
	return err
}

// Set :
func (s *MemoryStore) Set(key string, data []byte, expires time.Duration) error {
	return s.Update(func() error {
		v, ok := s.data.Get(key)
		if ok { // reuse
			item := v.(*define.StoreItem)
			item.Update(data, expires)
		} else {
			s.data.Put(key, define.NewStoreItem(data, expires))
		}
		return nil
	})
}

func (s *MemoryStore) getItem(key string) (*define.StoreItem, error) {
	var (
		result *define.StoreItem
		value  interface{}
		ok     bool
	)
	err := s.View(func() error {
		value, ok = s.data.Get(key)
		return nil
	})

	if err != nil {
		return nil, err
	} else if !ok {
		return nil, define.ErrItemNotFound
	}

	result = value.(*define.StoreItem)
	return result, err
}

// Get :
func (s *MemoryStore) Get(key string) ([]byte, error) {
	item, err := s.getItem(key)
	if err != nil {
		return nil, err
	}

	result := item.GetData(true)
	return result, nil
}

// Exists :
func (s *MemoryStore) Exists(key string) (bool, error) {
	item, err := s.getItem(key)
	if errors.Cause(err) == define.ErrItemNotFound {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return item != nil, nil
}

// Delete :
func (s *MemoryStore) Delete(key string) error {
	return s.Update(func() error {
		s.data.Remove(key)
		return nil
	})
}

// Commit :
func (s *MemoryStore) Commit() error {
	return s.Update(func() error {
		keys := make([]interface{}, 0)
		iterator := s.data.Iterator()
		for iterator.Next() {
			item := iterator.Value().(*define.StoreItem)
			if item.IsExpired() {
				keys = append(keys, iterator.Key())
			}
		}
		for _, key := range keys {
			s.data.Remove(key)
		}
		return nil
	})
}

// Scan :
func (s *MemoryStore) Scan(prefix string, callback define.StoreScanCallback, withAll ...bool) error {
	return s.View(func() error {
		iterator := s.data.Iterator()
		for iterator.Next() {
			key := iterator.Key().(string)
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			item := iterator.Value().(*define.StoreItem)
			data := item.GetData(true)
			if data != nil {
				if !callback(key, data) {
					break
				}
			}
		}
		return nil
	})
}

// PutCache :
func (s *MemoryStore) PutCache(key string, data []byte, expires time.Duration) error {
	return s.Update(func() error {
		v, ok := s.data.Get(key)
		if ok { // reuse
			item := v.(*define.StoreItem)
			item.Update(data, expires)
		} else {
			s.data.Put(key, define.NewStoreItem(data, expires))
		}
		return nil
	})
}

// Batch :
func (s *MemoryStore) Batch() error {
	return nil
}

// TreeMap :
type TreeMap struct {
	*treemap.Map
}

// Iterator :
func (m *TreeMap) Iterator() MapIterator {
	iterator := m.Map.Iterator()
	return &iterator
}

// NewTreeMemoryStore :
func NewTreeMemoryStore() define.Store {
	return NewMemoryStore(&TreeMap{
		Map: treemap.NewWithStringComparator(),
	})
}

// HashMap :
type HashMap struct {
	*linkedhashmap.Map
}

// Iterator :
func (m *HashMap) Iterator() MapIterator {
	iterator := m.Map.Iterator()
	return &iterator
}

// NewHashMemoryStore :
func NewHashMemoryStore() define.Store {
	return NewMemoryStore(&HashMap{
		Map: linkedhashmap.New(),
	})
}

func init() {
	define.RegisterStore("hashmap", func(ctx context.Context, name string) (define.Store, error) {
		return NewHashMemoryStore(), nil
	})
	define.RegisterStore("bstmap", func(ctx context.Context, name string) (define.Store, error) {
		return NewTreeMemoryStore(), nil
	})
}
