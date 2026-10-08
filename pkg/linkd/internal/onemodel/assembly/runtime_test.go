// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package assembly 为 OneModel 消费方装配独立、有界的只读连接。
package assembly

import (
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
)

func TestDorisAssemblyOpensLazilyAndClosesSharedResources(t *testing.T) {
	resource := &config.OneModelResource{Backend: "doris", Doris: &config.OneModelDorisResource{Address: "127.0.0.1:1", Database: "kingeye", Username: "reader"}}
	_, connections, err := Open(resource, 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if connections.es != nil || connections.db == nil || connections.db.Stats().OpenConnections != 0 {
		t.Fatal("unused backend connected")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := connections.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if _, err = connections.db.QueryContext(t.Context(), "SELECT 1"); err == nil {
		t.Fatal("SQL pool survived Close")
	}
	for _, n := range []int{0, 1025} {
		if _, _, err = Open(resource, n, time.Second); err == nil {
			t.Fatal("unbounded connection budget")
		}
	}
}
