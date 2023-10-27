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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
)

// BBoltStoreSuite :
type BBoltStoreSuite struct {
	BaseStoreSuite
	dir    string
	store  define.Store
	cancel context.CancelFunc
}

// SetupTest :
func (s *BBoltStoreSuite) SetupTest() {
	var err error
	s.dir, err = os.MkdirTemp("", "bbolt")
	s.NoError(err)

	store, err := storage.NewBBoltStore("test", filepath.Join(s.dir, "transfer.db"), 0o666, nil)
	ctx := context.Background()
	ctx, s.cancel = context.WithCancel(ctx)
	storage.InitBboltChanStore(ctx, store)
	if err != nil {
		panic(err)
	}
	s.store = store
}

// TearDownTests :
func (s *BBoltStoreSuite) TearDownTests() {
	s.cancel()
	s.NoError(s.store.Close())
	s.NoError(os.Remove(s.dir))
}

// TestUsage :
func (s *BBoltStoreSuite) TestUsage() {
	s.RunStoreUsageCase(s.store)
}

// TestStoreParallel :
func (s *BBoltStoreSuite) TestStoreParallel() {
	s.RunParallelOperationCase(s.store)
}

// TestStoreExpiresCase :
func (s *BBoltStoreSuite) TestStoreExpiresCase() {
	s.RunStoreExpiresCase(s.store)
}

// TestStoreScanCase :
func (s *BBoltStoreSuite) TestStoreScanCase() {
	s.RunStoreScanCase(s.store)
}

// TestStoreScanWithGetCase :
func (s *BBoltStoreSuite) TestStoreScanWithGetCase() {
	s.RunStoreScanWithGetCase(s.store)
}

// TestBBoltStoreSuite :
func TestBBoltStoreSuite(t *testing.T) {
	suite.Run(t, new(BBoltStoreSuite))
}
