// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build bbolt
// +build bbolt

package storage_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
)

// BBoltStoreSuite :
type BBoltCacheSuite struct {
	suite.Suite
	dir    string
	ctx    context.Context
	cancel context.CancelFunc
	store  *storage.BBoltStore
}

// SetupTest :
func (s *BBoltCacheSuite) SetupTest() {
	var err error
	s.dir, err = os.MkdirTemp("", "bbolt")

	store, err := storage.NewBBoltStore("test", filepath.Join(s.dir, "transfercache.db"), 0o666, nil)
	s.ctx, s.cancel = context.WithCancel(context.Background())
	storage.InitBboltChanStore(s.ctx, store)
	if err != nil {
		panic(err)
	}
	s.store = store
}

// TearDownTests :
func (s *BBoltCacheSuite) TearDownTests() {
	s.cancel()
	s.NoError(s.store.Close())
	s.NoError(os.Remove(s.dir))
}

// TestPutCahce :
func (s *BBoltCacheSuite) TestPutCahce() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			key := fmt.Sprintf("ttt%d", i)
			s.NoError(s.store.PutCache(key, []byte(time.Now().String()), 10*time.Second))
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			key := fmt.Sprintf("test%d", i)
			s.NoError(s.store.PutCache(key, []byte(time.Now().String()), 10*time.Second))
		}
	}()
	wg.Wait()
}

// TestCommit :
func (s *BBoltCacheSuite) TestCommit() {
	s.NoError(s.store.Commit())
}

// TestParallel :
func (s *BBoltCacheSuite) TestParallel() {
}

// TestBBoltStoreSuite :
func TestBBoltCacheStoreSuite(t *testing.T) {
	suite.Run(t, new(BBoltCacheSuite))
}
