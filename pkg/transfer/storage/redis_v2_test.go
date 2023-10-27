// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build redis_v2
// +build redis_v2

package storage_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
)

// RedisStoreSuite :
type RedisStoreSuite struct {
	BaseStoreSuite
	store     *storage.RedisStore
	minir     *miniredis.Miniredis
	ctxCancel context.CancelFunc
}

// SetupTest :
func (s *RedisStoreSuite) SetupTest() {
	var err error
	s.minir, err = miniredis.Run()
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	addr := s.minir.Addr()
	splitIndex := strings.Index(addr, ":")
	c := config.NewConfiguration()
	c.SetDefault(storage.ConfRedisStorageHost, addr[:splitIndex])
	c.SetDefault(storage.ConfRedisStoragePassword, "")
	c.SetDefault(storage.ConfRedisStoragePort, addr[splitIndex+1:])
	c.SetDefault(storage.ConfRedisStorageDatabase, 0)
	c.SetDefault(storage.ConfRedisStorageKey, "bkmonitorv3.transfer.cmdb.cache")
	c.SetDefault(storage.ConfRedisStorageBatchSize, 100)
	c.SetDefault(storage.ConfRedisStorageMemCheckPeriod, "1s")
	c.SetDefault(storage.ConfRedisStorageMemWaitTime, "1s")
	c.SetDefault(storage.ConfRedisStorageUpdateWaitTime, "1s")
	c.SetDefault(storage.ConfRedisStorageCleanDataPeriod, "2s")
	c.SetDefault(storage.ConfCcCacheSize, 5)
	ctx = context.WithValue(ctx, define.ContextConfigKey, c)
	ctx, s.ctxCancel = context.WithCancel(ctx)
	store, err := define.NewStore(ctx, "redis")
	s.store = store.(*storage.RedisStore)
	storage.WaitCache()
	s.NoError(err)
}

// TearDownTests :
func (s *RedisStoreSuite) TearDownTest() {
	s.ctxCancel()
	s.store.Close()
}

// TestUsage :
func (s *RedisStoreSuite) TestUsage() {
	s.RunStoreUsageCase(s.store)
}

// TestStoreParallel :
func (s *RedisStoreSuite) TestStoreParallel() {
	s.RunParallelOperationCase(s.store)
}

// TestStoreExpiresCase :
func (s *RedisStoreSuite) TestStoreExpiresCase() {
	s.RunStoreExpiresCase(s.store)
}

// TestStoreScanCase :
func (s *RedisStoreSuite) TestStoreScanCase() {
	s.RunStoreScanCase(s.store)
}

// TestStoreScanWithGetCase :
func (s *RedisStoreSuite) TestStoreScanWithGetCase() {
	s.RunStoreScanWithGetCase(s.store)
}

// 测试内存缓存冷数据释放
func (s *RedisStoreSuite) TestMemDataHandle() {
	// 写入数据
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("k-%d", i)
		bytesBuffer := bytes.NewBuffer([]byte{})
		_ = binary.Write(bytesBuffer, binary.BigEndian, i)
		_ = s.store.Set(key, bytesBuffer.Bytes(), 4*time.Second)
	}
	// 使用后2个数据
	time.Sleep(3 * time.Second)
	for i := 3; i < 5; i++ {
		key := fmt.Sprintf("k-%d", i)
		_, _ = s.store.Get(key)
	}
	time.Sleep(3 * time.Second)
	hotData := s.store.HotData()
	expect := map[string]bool{"k-3": true, "k-4": true}
	hotData.Range(func(key, value interface{}) bool {
		sKey, _ := key.(string)
		s.NotEmpty(sKey, "key is not string : ", key)
		found := expect[sKey]
		s.Equal(true, found, "not found key: %s", key)
		return true
	})
}

// 测试PutCache数据的延迟批量写入
func (s *RedisStoreSuite) TestPutCache() {
	for i := 0; i < 5; i++ {
		k := fmt.Sprintf("key-%d", i)
		s.NoError(s.store.PutCache(k, []byte(fmt.Sprintf("%d", i)), time.Second))
		time.Sleep(1 * time.Millisecond)
		keys, err := s.store.AllKeys()
		s.NoError(err)
		if i == 4 {
			s.Equal(5, len(keys))
		} else {
			s.Equal(0, len(keys))
		}
	}
}

// 测试 redis 数据清理
func (s *RedisStoreSuite) TestClean() {
	// 首次写入数据
	for i := 0; i < 6; i++ {
		s.NoError(s.store.PutCache(fmt.Sprintf("key-%d", i), []byte(fmt.Sprintf("%d", i)), 100*time.Millisecond))
	}
	s.NoError(s.store.Commit())
	time.Sleep(2 * time.Second)
	// 第二次更新数据
	for i := 3; i < 6; i++ {
		s.NoError(s.store.PutCache(fmt.Sprintf("key-%d", i), []byte(fmt.Sprintf("%d", i*2)), 5*time.Second))
	}
	s.NoError(s.store.Commit())
	time.Sleep(100 * time.Millisecond)
	// 最终结果
	// mini-redis有点问题
	_, err := s.store.AllKeys()
	s.NoError(err)
	// s.Equal(6, len(keys), keys)
}

// TestSortedSet :
func (s *RedisStoreSuite) TestSortedSets() {
	key := "sortedsets-test"
	memberScorePairs := map[string]float64{}
	for i := 1; i <= 700; i++ {
		memberScorePairs[fmt.Sprintf("%d", i)] = float64(i)
	}
	s.NoError(s.store.ZAddBatch(key, memberScorePairs))

	members, err := s.store.ZRangeByScore(key, 101, 700)
	s.NoError(err)
	s.Equal(600, len(members))
	s.Equal("101", members[0])
	s.Equal("700", members[599])
}

// TestHashes :
func (s *RedisStoreSuite) TestHashes() {
	key := "hashes-test"
	fieldValuePairs := map[string]string{
		"aaa": "good",
		"bbb": "1",
	}
	s.NoError(s.store.HSetBatch(key, fieldValuePairs))

	fields := []string{"aaa", "bbb", "ccc"}
	values, err := s.store.HGetBatch(key, fields)
	s.NoError(err)
	s.Equal(3, len(values))
	s.Equal("good", values[0])
	s.Equal("1", values[1])
	s.Equal(nil, values[2])
}

// TestRedisStoreSuite :
func TestRedisStoreSuite(t *testing.T) {
	suite.Run(t, new(RedisStoreSuite))
}
