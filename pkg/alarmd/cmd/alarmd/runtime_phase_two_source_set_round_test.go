// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// The round handed to the ledger carries the catalog's own word on when each
// absent strategy was first found absent, and no word where the catalog had
// none: the ledger's account of a strategy under grace then starts where the
// catalog says it did, across a leader restart, rather than where this
// process first saw it.
func TestTheLedgersRoundCarriesTheCatalogsAbsentSince(t *testing.T) {
	at := time.Date(2026, 9, 22, 10, 5, 0, 0, time.UTC)
	since := at.Add(-4 * time.Minute)
	composition := &controlplane.CatalogComposition{
		ListedStrategies: []string{"s-1"},
		WithheldObjects: []controlplane.ObjectDisposition{
			{SourceID: "s-2", Scope: "STRATEGY", Disposition: controlplane.DispositionPendingRemoval, Reason: "REMOVED_FROM_ACTIVE_SET", AbsentSince: since.Unix()},
			{SourceID: "s-3", Scope: "STRATEGY", Disposition: controlplane.DispositionPendingRemoval, Reason: "REMOVED_FROM_ACTIVE_SET"},
			{SourceID: "s-4", Scope: "STRATEGY", Disposition: controlplane.DispositionRemoved, Reason: "ABSENT_FROM_ACTIVE_SET", AbsentSince: since.Unix()},
			{SourceID: "s-5", Scope: "STRATEGY", Disposition: controlplane.DispositionSourceIncomplete, Reason: "SOURCE_IDENTITY_UNAVAILABLE"},
		},
	}
	round := sourceSetRoundOf(composition, at)
	if !round.At.Equal(at) || len(round.Listed) != 1 || round.Listed[0] != "s-1" {
		t.Fatalf("round = %+v", round)
	}
	if len(round.PendingRemoval) != 2 || round.PendingRemoval[0].StrategyID != "s-2" || !round.PendingRemoval[0].AbsentSince.Equal(since) ||
		round.PendingRemoval[1].StrategyID != "s-3" || !round.PendingRemoval[1].AbsentSince.IsZero() {
		t.Fatalf("pending = %+v, want s-2 with the catalog's start and s-3 with none", round.PendingRemoval)
	}
	if len(round.Removed) != 1 || round.Removed[0].StrategyID != "s-4" || !round.Removed[0].AbsentSince.Equal(since) {
		t.Fatalf("removed = %+v, want s-4 with the catalog's start", round.Removed)
	}
}
