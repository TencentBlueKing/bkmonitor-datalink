// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package labelstore

import (
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/labels"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

func makeTempDir(logf func(format string, args ...any)) string {
	dir, err := os.MkdirTemp("", "stor_test")
	if err != nil {
		panic(err)
	}
	logf("make temp dir: %v", dir)
	return dir
}

func removeTempDir(logf func(format string, args ...any), dir string) {
	err := os.RemoveAll(dir)
	if err != nil {
		panic(err)
	}
	logf("remove temp dir: %v", dir)
}

func TestBuiltinStorage(t *testing.T) {
	InitStorage(".", "builtin")
	defer CleanStorage()

	stor := GetOrCreateStorage(1001)
	testStorage(t, stor)
	assert.NoError(t, stor.Clean())
}

func TestLeveldbStorage(t *testing.T) {
	dir := makeTempDir(t.Logf)
	defer removeTempDir(t.Logf, dir)

	InitStorage(dir, TypeLeveldb)
	defer CleanStorage()

	stor := GetOrCreateStorage(1001)
	testStorage(t, stor)
	assert.NoError(t, stor.Clean())
}

func testStorage(t *testing.T, stor Storage) {
	t.Log("testing storage", stor.Name())
	for i := 0; i < 100; i++ {
		err := stor.SetIf(uint64(i), labels.Labels{
			{
				Name:  "index",
				Value: strconv.FormatInt(int64(i), 10),
			},
		})
		assert.NoError(t, err)
	}

	for i := 0; i < 100; i++ {
		lbs, err := stor.Get(uint64(i))
		assert.NoError(t, err)
		assert.Equal(t, labels.Labels{{Name: "index", Value: strconv.FormatInt(int64(i), 10)}}, lbs)
	}
	assert.NoError(t, stor.Del(1))
}

const (
	setCount = 100000 // 10w
	appCount = 10

	block = false
)

// FailNow 是为了仅执行一次
func blockForever() {
	if block {
		select {}
	}
}

func benchmarkStorageSetIf(stor Storage) {
	for i := 0; i < setCount; i++ {
		lbs := random.FastDimensions(6)
		_ = stor.SetIf(uint64(i), labels.FromMap(lbs))
	}
}

func BenchmarkBuiltinSet(b *testing.B) {
	storMap := make(map[int]Storage)
	start := time.Now()
	wg := sync.WaitGroup{}
	mut := sync.Mutex{}
	for i := 0; i < appCount; i++ {
		id := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			stor := newBuiltinStorage(int32(id))
			benchmarkStorageSetIf(stor)
			mut.Lock()
			storMap[id] = stor
			mut.Unlock()
		}()
	}
	wg.Wait()
	prettyprint.RuntimeMemStats(b.Logf)
	b.Logf("builtinStorage SetIf operation take: %v\n", time.Since(start))
	b.FailNow()
}

func BenchmarkLeveldbSet(b *testing.B) {
	start := time.Now()
	dir := makeTempDir(b.Logf)
	defer removeTempDir(b.Logf, dir)

	ctr := NewStorageController(dir, TypeLeveldb)
	defer ctr.Clean()

	storMap := make(map[int]Storage)
	mut := sync.Mutex{}
	wg := sync.WaitGroup{}
	for i := 0; i < appCount; i++ {
		id := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			mut.Lock()
			stor := ctr.GetOrCreate(int32(id))
			storMap[id] = stor
			mut.Unlock()

			benchmarkStorageSetIf(stor)
		}()
	}
	wg.Wait()
	prettyprint.RuntimeMemStats(b.Logf)
	b.Logf("leveldbStorage SetIf operation take: %v\n", time.Since(start))
	b.FailNow()
}

func benchmarkStorDel(stor Storage) {
	for i := 0; i < setCount; i++ {
		_ = stor.Del(uint64(i))
	}
}

func BenchmarkBuiltinDel(b *testing.B) {
	storMap := make(map[int]Storage)
	for i := 0; i < appCount; i++ {
		stor := newBuiltinStorage(int32(i))
		benchmarkStorageSetIf(stor)
		storMap[i] = stor
	}
	start := time.Now()
	for i := 0; i < appCount; i++ {
		benchmarkStorDel(storMap[i])
	}
	prettyprint.RuntimeMemStats(b.Logf)
	b.Logf("builtinStorage Del operation take: %v\n", time.Since(start))
	b.FailNow()
}

func BenchmarkLeveldbDel(b *testing.B) {
	dir := makeTempDir(b.Logf)
	defer removeTempDir(b.Logf, dir)

	ctr := NewStorageController(dir, TypeLeveldb)
	defer ctr.Clean()

	storMap := make(map[int]Storage)
	for i := 0; i < appCount; i++ {
		stor := ctr.GetOrCreate(int32(i))
		storMap[i] = stor
	}
	start := time.Now()
	for i := 0; i < appCount; i++ {
		benchmarkStorDel(storMap[i])
	}
	prettyprint.RuntimeMemStats(b.Logf)
	b.Logf("leveldbStorage Del operation take: %v\n", time.Since(start))
	b.FailNow()
}

func benchmarkStorGet(stor Storage) {
	for i := 0; i < setCount; i++ {
		stor.Get(uint64(i))
	}
}

func BenchmarkBuiltinGet(b *testing.B) {
	storMap := make(map[int]Storage)
	for i := 0; i < appCount; i++ {
		stor := newBuiltinStorage(int32(i))
		benchmarkStorageSetIf(stor)
		storMap[i] = stor
	}
	start := time.Now()
	for i := 0; i < appCount; i++ {
		benchmarkStorGet(storMap[i])
	}
	prettyprint.RuntimeMemStats(b.Logf)
	b.Logf("builtinStorage Get operation take: %v\n", time.Since(start))
	b.FailNow()
}

func BenchmarkLeveldbGet(b *testing.B) {
	dir := makeTempDir(b.Logf)
	defer removeTempDir(b.Logf, dir)

	ctr := NewStorageController(dir, TypeLeveldb)
	defer ctr.Clean()

	storMap := make(map[int]Storage)
	for i := 0; i < appCount; i++ {
		stor := ctr.GetOrCreate(int32(i))
		storMap[i] = stor
	}
	start := time.Now()
	for i := 0; i < appCount; i++ {
		benchmarkStorGet(storMap[i])
	}
	prettyprint.RuntimeMemStats(b.Logf)
	b.Logf("leveldbStorage Get operation take: %v\n", time.Since(start))
	b.FailNow()
}
