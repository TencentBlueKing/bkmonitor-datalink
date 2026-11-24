// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
)

// Consul Storage without a usable address fails once at load and starts no reconcile loop.
func TestConsulStorageWithoutAddressStartsNoReconcile(t *testing.T) {
	previous := StorageSource
	t.Cleanup(func() { StorageSource = previous; consul.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range []struct {
		address string
		reason  string
	}{
		{"", consul.ReasonAddressNotConfigured},
		{"http://:8500", consul.ReasonAddressMissingHost},
	} {
		_ = consul.SetInstance(ctx, "", tc.address, "", "", "")
		StorageSource = "consul"
		s := &Service{wg: new(sync.WaitGroup)}
		err := s.loopReloadStorage(ctx)
		var addressErr *consul.AddressError
		if !errors.As(err, &addressErr) || addressErr.Reason != tc.reason || !strings.Contains(err.Error(), "storage.source") {
			t.Fatalf("address %q: want a storage.source error naming %s, got %v", tc.address, tc.reason, err)
		}
		// Nothing was started, so waiting returns at once.
		s.Wait()

		StorageSource = "redis"
		if err := consulStorageAddressError(); err != nil {
			t.Fatalf("address %q: Redis Storage does not depend on Consul: %v", tc.address, err)
		}
	}
	_ = consul.SetInstance(ctx, "", "http://192.0.2.1:8500", "", "", "")
	StorageSource = "consul"
	if err := consulStorageAddressError(); err != nil {
		t.Fatalf("a usable Consul address is not a load error: %v", err)
	}
}
