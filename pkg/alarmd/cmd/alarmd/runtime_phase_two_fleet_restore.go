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
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
)

// progressRestoreSource reads what the control plane already recorded about an
// object, so a replica that just started does not have to watch a fresh round
// before it can say anything.
//
// It reads the same Progress the scheduler reads, one object at a time and only
// for objects this replica owns and has not yet observed. The publisher bounds
// how many it asks for per tick.
func progressRestoreSource(store *progress.Store) func(context.Context, execution.QueryGroupIdentity) (fleet.RestoredState, bool) {
	if store == nil {
		return nil
	}
	return func(ctx context.Context, queryGroup execution.QueryGroupIdentity) (fleet.RestoredState, bool) {
		result, err := store.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: queryGroup})
		if err != nil || result.Progress == nil {
			// A failed read leaves the object unknown, which is the same answer
			// the replica would have given without this at all. Diagnostics must
			// not turn a control plane hiccup into a worse verdict than silence.
			return fleet.RestoredState{}, false
		}
		return fleet.RestoredState{
			LastCompletion: string(result.Progress.LastCompletionKind),
			NextSlot:       time.Unix(int64(result.Progress.NextSlot), 0),
		}, true
	}
}

// fleetRestoreBudgetPerPublish is how many objects one publish may read back.
//
// It is a rate, not a cap: every owned object is eventually restored, just
// spread across publishes rather than read in one burst at the moment the
// process is least settled. At the current publish cadence a full deployment is
// covered in well under a minute, against the many minutes of unknown a restart
// otherwise costs.
const fleetRestoreBudgetPerPublish = 128
