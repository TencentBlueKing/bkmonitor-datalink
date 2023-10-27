// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build skiplist
// +build skiplist

package storage

import (
	"context"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/MauriceGit/skiplist"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// SkipListElement :
type SkipListElement struct {
	extractKey float64
	key        string
	*define.StoreItem
}

// SetKey :
func (e *SkipListElement) SetKey(key string) {
	hash := fnv.New64a()
	_, err := hash.Write([]byte(key))
	if err != nil {
		panic(err)
	}

	e.extractKey = float64(hash.Sum64())
	e.key = key
}

// String :
func (e *SkipListElement) String() string {
	return e.key
}

// ExtractKey :
func (e *SkipListElement) ExtractKey() float64 {
	return e.extractKey
}

// NewSkipListElement :
func NewSkipListElement(key string) *SkipListElement {
	el := &SkipListElement{}
	el.SetKey(key)
	return el
}

// NewSkipListElementWithValue :
func NewSkipListElementWithValue(key string, value []byte, expires time.Duration) *SkipListElement {
	el := &SkipListElement{
		StoreItem: define.NewStoreItem(value, expires),
	}
	el.SetKey(key)
	return el
}

// SkipListStore :
type SkipListStore struct {
	*define.BaseStore
	lock sync.RWMutex
	data skiplist.SkipList
}

// Exists :
func (s *SkipListStore) Exists(key string) (bool, error) {
	el := NewSkipListElement(key)
	s.lock.RLock()
	_, ok := s.data.Find(el)
	s.lock.RUnlock()
	return ok, nil
}

// Set :
func (s *SkipListStore) Set(key string, data []byte, expires time.Duration) error {
	el := NewSkipListElementWithValue(key, data, expires)
	s.lock.Lock()
	node, ok := s.data.Find(el)
	if ok {
		s.data.ChangeValue(node, el)
	} else {
		s.data.Insert(el)
	}
	s.lock.Unlock()
	return nil
}

// Get :
func (s *SkipListStore) Get(key string) ([]byte, error) {
	el := NewSkipListElement(key)
	s.lock.RLock()
	defer s.lock.RUnlock()
	result, ok := s.data.Find(el)
	if !ok {
		return nil, define.ErrItemNotFound
	}

	value := result.GetValue()
	data := value.(*SkipListElement).GetData(true)
	if data == nil {
		return nil, define.ErrItemNotFound
	}
	return data, nil
}

// Delete :
func (s *SkipListStore) Delete(key string) error {
	el := NewSkipListElement(key)
	s.lock.Lock()
	s.data.Delete(el)
	s.lock.Unlock()
	return nil
}

// Commit :
func (s *SkipListStore) Commit() error {
	items := make([]*SkipListElement, 0)
	s.lock.Lock()
	defer s.lock.Unlock()
	first := s.data.GetSmallestNode()
	node := first
	for {
		value := node.GetValue()
		item := value.(*SkipListElement)
		if item.IsExpired() {
			items = append(items, item)
		}
		node = s.data.Next(node)
		if node == first {
			break
		}
	}
	for _, item := range items {
		s.data.Delete(item)
	}
	return nil
}

// Scan :
func (s *SkipListStore) Scan(prefix string, callback define.StoreScanCallback, withAll ...bool) error {
	first := s.data.GetSmallestNode()
	node := first
	for {
		value := node.GetValue()
		item := value.(*SkipListElement)

		if !strings.HasPrefix(item.key, prefix) {
			continue
		}

		data := item.GetData(true)
		if data != nil && !callback(item.key, data) {
			break
		}
		node = s.data.Next(node)
		if node == first {
			break
		}
	}
	return nil
}

// PutCache :
func (s *SkipListStore) PutCache(key string, data []byte, expires time.Duration) error {
	return nil
}

// Batch :
func (s *SkipListStore) Batch() error {
	return nil
}

// NewSkipListStore :
func NewSkipListStore() *SkipListStore {
	return &SkipListStore{
		BaseStore: define.NewBaseStore(),
		data:      skiplist.New(),
	}
}

func init() {
	define.RegisterStore("skiplist", func(ctx context.Context, name string) (define.Store, error) {
		return NewSkipListStore(), nil
	})
}
