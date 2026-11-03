// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package timesync

import (
	"net"
	"testing"
	"time"

	"github.com/facebook/time/ntp/chrony"
)

func TestChronySourceDetail(t *testing.T) {
	now := time.Unix(1700000000, 0)
	addr := net.ParseIP("10.0.0.31")

	t.Run("client normal", func(t *testing.T) {
		detail, ok := chronySourceDetail(&chrony.SourceData{
			IPAddr: addr, Mode: chrony.SourceModeClient, State: chrony.SourceStateSync, LatestMeas: 1.5,
		}, now)
		if !ok {
			t.Fatal("want detail for client source")
		}
		if !detail.Available || detail.OffsetSeconds != 1.5 {
			t.Fatalf("detail = %+v, want available offset 1.5", detail)
		}
		if detail.Address != "10.0.0.31" || detail.Target != "chrony" {
			t.Fatalf("detail = %+v", detail)
		}
		if !detail.SourceTime.Equal(now) {
			t.Fatalf("source time = %v, want %v", detail.SourceTime, now)
		}
	})

	t.Run("client zero offset", func(t *testing.T) {
		detail, ok := chronySourceDetail(&chrony.SourceData{
			IPAddr: addr, Mode: chrony.SourceModeClient, State: chrony.SourceStateSync, LatestMeas: 0,
		}, now)
		if !ok {
			t.Fatal("want detail for client source")
		}
		if !detail.Available || detail.OffsetSeconds != 0 {
			t.Fatalf("true zero offset must be reported: %+v", detail)
		}
	})

	t.Run("client unreachable", func(t *testing.T) {
		detail, ok := chronySourceDetail(&chrony.SourceData{
			IPAddr: addr, Mode: chrony.SourceModeClient, State: chrony.SourceStateUnreach, LatestMeas: 0,
		}, now)
		if !ok {
			t.Fatal("want detail for unreachable source")
		}
		if detail.Available {
			t.Fatalf("unreachable source must report available=false: %+v", detail)
		}
		if !detail.SourceTime.IsZero() {
			t.Fatalf("unreachable source must not carry source time: %+v", detail)
		}
	})

	t.Run("ref mode skipped", func(t *testing.T) {
		if _, ok := chronySourceDetail(&chrony.SourceData{
			IPAddr: addr, Mode: chrony.SourceModeRef, State: chrony.SourceStateSync, LatestMeas: 1,
		}, now); ok {
			t.Fatal("reference clock/local clock must not produce detail")
		}
	})
}
