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
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
)

// MapStoreSuite :
type MapStoreSuite struct {
	BaseStoreSuite
	store define.Store
}

// SetupTest :
func (s *MapStoreSuite) SetupTest() {
	s.store = storage.NewMapStore()
}

// TestUsage :
func (s *MapStoreSuite) TestUsage() {
	s.RunStoreUsageCase(s.store)
}

// TestStoreParallel :
func (s *MapStoreSuite) TestStoreParallel() {
	s.RunParallelOperationCase(s.store)
}

// TestStoreExpiresCase :
func (s *MapStoreSuite) TestStoreExpiresCase() {
	s.RunStoreExpiresCase(s.store)
}

// TestStoreScanCase :
func (s *MapStoreSuite) TestStoreScanCase() {
	s.RunStoreScanCase(s.store)
}

// TestStoreScanWithGetCase :
func (s *MapStoreSuite) TestStoreScanWithGetCase() {
	s.RunStoreScanWithGetCase(s.store)
}

// TestMapStoreSuite :
func TestMapStoreSuite(t *testing.T) {
	suite.Run(t, new(MapStoreSuite))
}
