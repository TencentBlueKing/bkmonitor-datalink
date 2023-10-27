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
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

func withClosingStore(fn func(*testing.B, define.Store), b *testing.B, store define.Store) {
	fn(b, store)
	err := store.Close()
	if err != nil {
		panic(err)
	}
}

func initItems(b *testing.B, store define.Store, n int) []string {
	name := b.Name()
	keys := make([]string, n)
	expires := 10 * time.Minute
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("benchmark-store-%s-%d", name, rand.Int())
		keys[i] = key
		value := []byte(strconv.Itoa(rand.Int()))
		err := store.Set(key, value, expires)
		if err != nil {
			panic(err)
		}
	}
	return keys
}

func getSize() int {
	v := os.Getenv("TRANSFER_BENCH_SIZE")
	if v == "" {
		return 100
	}
	size, err := strconv.Atoi(v)
	if err != nil {
		panic(err)
	}
	return size
}

func shuffleKeys(keys []string) {
	size := len(keys)
	for i := 0; i < size; i++ {
		n := rand.Intn(size)
		key := keys[n]
		keys[n] = keys[i]
		keys[i] = key
	}
}

func benchmarkStoreSet(b *testing.B, store define.Store) {
	size := getSize()

	keys := initItems(b, store, size)

	expires := 10 * time.Minute
	value := []byte(strconv.Itoa(rand.Int()))
	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		err := store.Set(keys[rand.Intn(size)], value, expires)
		b.StopTimer()
		if err != nil {
			panic(err)
		}
	}
}

func benchmarkStoreUpdate(b *testing.B, store define.Store) {
	size := getSize()

	keys := initItems(b, store, size)

	expires := 10 * time.Minute
	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		key := keys[rand.Intn(size)]
		value := []byte(strconv.Itoa(rand.Int()))
		b.StartTimer()
		err := store.Set(key, value, expires)
		b.StopTimer()
		if err != nil {
			panic(err)
		}
	}
}

func benchmarkStoreGet(b *testing.B, store define.Store) {
	size := getSize()

	keys := initItems(b, store, size)

	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		_, err := store.Get(keys[rand.Intn(size)])
		b.StopTimer()
		if err != nil {
			panic(err)
		}
	}
	b.StopTimer()
}

func benchmarkStoreGetHotPot(b *testing.B, store define.Store) {
	size := getSize()

	keys := initItems(b, store, size)
	index := rand.Intn(size)

	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		_, err := store.Get(keys[index])
		b.StopTimer()
		if err != nil {
			panic(err)
		}
	}
	b.StopTimer()
}

func benchmarkStoreExistsMissing(b *testing.B, store define.Store) {
	size := getSize()

	initItems(b, store, size)

	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		_, err := store.Exists("missing")
		b.StopTimer()
		if err != nil {
			panic(err)
		}
	}
}

func benchmarkStoreExists(b *testing.B, store define.Store) {
	size := getSize()

	keys := initItems(b, store, size)

	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		key := keys[rand.Intn(size)]
		b.StartTimer()
		_, err := store.Exists(key)
		b.StopTimer()
		if err != nil {
			panic(err)
		}
	}
}

func benchmarkStoreDelete(b *testing.B, store define.Store) {
	size := getSize()

	keys := initItems(b, store, size)
	shuffleKeys(keys)

	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		err := store.Delete(keys[i])
		b.StopTimer()
		if err != nil {
			panic(err)
		}
	}
	b.StopTimer()
}

func benchmarkStoreScan(b *testing.B, store define.Store) {
	size := getSize()

	initItems(b, store, size)

	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		err := store.Scan("benchmark-", func(key string, data []byte) bool {
			return true
		})
		b.StopTimer()
		if err != nil {
			panic(err)
		}
	}
	b.StopTimer()
}

func benchmarkStoreCommit(b *testing.B, store define.Store) {
	size := getSize()

	initItems(b, store, size)

	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		err := store.Commit()
		b.StopTimer()
		if err != nil {
			panic(err)
		}
	}
}
