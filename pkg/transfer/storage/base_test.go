// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// BaseStoreSuite :
type BaseStoreSuite struct {
	suite.Suite
}

// RunParallelOperationCase :
func (s *BaseStoreSuite) RunParallelOperationCase(store define.Store) {
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	n := runtime.NumCPU() + 1
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(index int) {
			key := fmt.Sprintf("test-%d", index)
			logf := s.T().Logf
			var value []byte
		loop:
			for j := 0; j < 10000; j++ {
				select {
				case <-ctx.Done():
					logf("context done when i=%d", j)
					break loop
				default:
					switch rand.Intn(4) {
					case 0:
						value = make([]byte, rand.Intn(10)+1)
						rand.Read(value)
						s.NoError(store.Set(key, value, define.StoreNoExpires))
					case 1:
						data, err := store.Get(key)
						if value == nil {
							s.Equal(define.ErrItemNotFound, err)
						} else {
							s.Equal(value, data)
						}
					case 2:
						s.NoError(store.Delete(key))
						value = nil
					case 3:
						s.NoError(store.Commit())
					}
				}
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
	cancel()
	// commit操作是发送信号给另一个goroutine，此时不能立即关掉store，会导致处理数据的goroutine无store可用
	time.Sleep(time.Second)
	s.NoError(store.Close())
}

// RunStoreUsageCase :
func (s *BaseStoreSuite) RunStoreUsageCase(store define.Store) {
	var (
		key    = "test"
		data   = []byte(key)
		result []byte
		err    error
	)

	s.NoError(store.Set(key, data, define.StoreNoExpires))

	found, err := store.Exists(key)
	s.NoError(err)
	s.True(found)

	result, err = store.Get(key)
	s.Nil(err)
	s.Equal(data, result)

	s.NoError(store.Delete(key))

	found, err = store.Exists(key)
	s.NoError(err)
	s.False(found)

	result, err = store.Get(key)
	s.Equal(define.ErrItemNotFound, err)
	s.Nil(result)

	s.NoError(store.Delete(key))

	s.NoError(store.Close())
}

// RunStoreExpiresCase :
func (s *BaseStoreSuite) RunStoreExpiresCase(store define.Store) {
	key := "test"
	value := []byte("x")
	begin := time.Now()

	s.NoError(store.Set("x", value, define.StoreNoExpires))
	data, err := store.Get("x")
	s.NotNil(data)
	s.NoError(err)

	end := time.Now()
	expires := 2*end.Sub(begin) + time.Millisecond

	s.NoError(store.Set(key, value, expires))

	time.Sleep(expires)
	data, err = store.Get(key)
	s.NotNil(data)
	s.NotEqual(define.ErrItemNotFound, err)
}

// RunStoreScanCase :
func (s *BaseStoreSuite) RunStoreScanCase(store define.Store) {
	size := unsafe.Sizeof(int64(0))
	for i := 0; i < 100; i++ {
		var key string
		if i%2 == 0 {
			key = fmt.Sprintf("even-%d", i)
		} else {
			key = fmt.Sprintf("odd-%d", i)
		}

		value := uint64(i)
		data := make([]byte, size)
		binary.LittleEndian.PutUint64(data, value)
		s.NoError(store.Set(key, data, define.StoreNoExpires))
	}

	var total uint64
	s.NoError(store.Scan("odd-", func(key string, data []byte) bool {
		s.True(strings.HasPrefix(key, "odd-"))
		value := binary.LittleEndian.Uint64(data)
		total += value
		return true
	}))

	s.Equal(uint64(2500), total)
}

// RunStoreScanCase :
func (s *BaseStoreSuite) RunStoreScanWithGetCase(store define.Store) {
	s.NoError(store.Set("test", []byte("x"), define.StoreNoExpires))
	s.NoError(store.Scan("test", func(key string, data []byte) bool {
		value, err := store.Get(key)
		s.NoError(err)
		s.Equal(data, value)
		return true
	}))
}
