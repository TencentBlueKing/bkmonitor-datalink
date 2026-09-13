// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// The Legacy source reports the cache manager's change signal as written: the
// integer second of the run that last changed something. Anything else it
// finds under that key is reported as no signal, which makes the reconciler
// read everything, as it did before the signal was consulted.
func TestLegacyRedisStrategySourceReadsTheChangeSignalAsWritten(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	source := newRedisStrategySource(t, client)
	if signal, err := source.ChangeSignal(ctx); err != nil || signal != (controlplane.SourceChangeSignal{}) {
		t.Fatalf("ChangeSignal() without the key = (%+v, %v), want absent", signal, err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.last_updated", "1700000000", 0).Err(); err != nil {
		t.Fatal(err)
	}
	signal, err := source.ChangeSignal(ctx)
	if err != nil || !signal.Present || signal.Value != "1700000000" || !signal.WrittenAt.Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("ChangeSignal() = (%+v, %v), want the written second", signal, err)
	}
	for _, unreadable := range []string{"", "not-a-second", "-5", "0", "1700000000.5"} {
		if err := client.Set(ctx, "bkmonitor.cache.last_updated", unreadable, 0).Err(); err != nil {
			t.Fatal(err)
		}
		if signal, err := source.ChangeSignal(ctx); err != nil || signal.Present {
			t.Fatalf("ChangeSignal() with %q = (%+v, %v), want absent", unreadable, signal, err)
		}
	}
}
