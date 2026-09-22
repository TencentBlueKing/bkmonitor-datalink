// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtimeconfig

import (
	"sync"
	"testing"

	"linkd/internal/config"
)

func TestAtomicSeveritySnapshotsAreIsolatedAndValidated(t *testing.T) {
	state := NewSeverity(config.DefaultSeverityConfig())
	before := state.SeveritySnapshot()
	bad := before
	bad.Digest = "incorrect"
	if state.Install(bad) == nil || state.SeveritySnapshot().Digest != before.Digest {
		t.Fatal("invalid digest replaced snapshot")
	}
	var group sync.WaitGroup
	group.Go(func() {
		for range 100 {
			if err := state.Install(before); err != nil {
				t.Error(err)
			}
		}
	})
	for range 4 {
		group.Go(func() {
			for range 100 {
				snapshot := state.SeveritySnapshot()
				snapshot.Severity.Levels[0].Name = "mutated"
				table, _ := state.FreezeSeverity()
				if _, ok := table.Priority("critical"); !ok {
					t.Error("partial or externally changed snapshot")
				}
			}
		})
	}
	group.Wait()
}
