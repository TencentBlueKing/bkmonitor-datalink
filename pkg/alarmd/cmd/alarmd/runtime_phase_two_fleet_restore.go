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
// for objects this replica owns and has not yet determined. The publisher bounds
// how many it asks for per tick.
func progressRestoreSource(store *progress.Store) func(context.Context, execution.QueryGroupIdentity) (fleet.RestoredState, error) {
	if store == nil {
		return nil
	}
	return func(ctx context.Context, queryGroup execution.QueryGroupIdentity) (fleet.RestoredState, error) {
		result, err := store.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: queryGroup})
		if err != nil {
			return fleet.RestoredState{}, err
		}
		if result.Progress == nil {
			return fleet.RestoredState{}, nil
		}
		restored := fleet.RestoredState{
			LastCompletion: string(result.Progress.LastCompletionKind),
			NextSlot:       time.Unix(int64(result.Progress.NextSlot), 0),
		}
		// Zero means no round has ever completed in full, which is a different
		// statement from "it last completed in full at the epoch". Converting it
		// would hand the tracker a timestamp from 1970 and an age to match.
		if result.Progress.LastFullSlot > 0 {
			restored.LastFullSlot = time.Unix(int64(result.Progress.LastFullSlot), 0)
		}
		return restored, nil
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

// A failed read may be retried on later publishes, within the shared read budget.
// Stop after three attempts per ownership tenure rather than polling forever.
const fleetRestoreMaxAttempts = 3
