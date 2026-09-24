// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"strconv"
	"sync"
	"testing"
)

// The object cache can be configured again while it is being read. Run under
// -race this is the regression test: the field used to be a plain pointer,
// and a caller that replaced it while the maintenance loop looked objects up
// raced every reader. Without -race it still checks that every lookup sees
// either cache whole.
func TestTheObjectCacheCanBeConfiguredWhileItIsRead(t *testing.T) {
	repository := &RedisCatalogRepository{}
	if err := repository.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	const readers, rounds = 4, 500
	var group sync.WaitGroup
	for reader := 0; reader < readers; reader++ {
		group.Add(1)
		go func(reader int) {
			defer group.Done()
			for round := 0; round < rounds; round++ {
				key := "k" + strconv.Itoa(round%16)
				repository.objects().store(key, reader, 8)
				if value, size, ok := repository.objects().lookup(key); ok && (size != 8 || value == nil) {
					t.Errorf("lookup of %s came back (%v, %d)", key, value, size)
					return
				}
			}
		}(reader)
	}
	for round := 0; round < rounds; round++ {
		if err := repository.ConfigureObjectCache(1+round%8, 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	group.Wait()
}
