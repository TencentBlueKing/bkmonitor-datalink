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
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// observeWithheldObjects writes one line per source object whose disposition
// changed this round, naming the strategy.
//
// The counts answer "how many strategies are not running and why"; nothing
// answered "which ones", and that is the question an operator arrives with.
// A count that moved from 41 to 42 overnight cannot be turned into a strategy
// to look at, and the object page it would be read from is a leader-only
// Redis document nobody reaches during an incident.
//
// One line per changed object, not per withheld object: see ChangedWithheld
// for why the steady state writes nothing. The round is a success either way -
// a withheld strategy is a decision the control plane made and recorded, not a
// failure of the round that recorded it, and reporting it as a failure would
// put a healthy leader into a permanently failing state.
// No snapshot revision is stamped. A withheld object belongs to the Catalog
// this round built, and that Catalog's revision is not what the result carries
// on every status: a round that publishes nothing reports the revision the
// fleet is already executing, which is a different Catalog. A line naming the
// wrong revision is worse than a line naming none.
func observeWithheldObjects(
	ctx context.Context,
	observer observability.Observer,
	report controlplane.WithheldReport,
) {
	for index, line := range report.Lines {
		trace := observability.TraceFields{
			StrategyID:    line.SourceID,
			TerminalScope: line.Scope,
		}
		// Only a LEVEL record has a level, and a zero level_id on a PLAN line
		// would read as level zero rather than as "the whole strategy".
		if line.Scope == withheldScopeLevel {
			trace.LevelID = strconv.FormatUint(uint64(line.LevelID), 10)
		}
		facts := observability.SourceWithheldFacts{
			Disposition: string(line.Disposition),
			Reason:      line.Reason,
			Field:       line.FieldPath,
		}
		// The cut is reported on the last line that fitted, so the reader who
		// reaches the end of the round's lines learns there that more were
		// held back. Putting it on every line would say it len(Lines) times
		// and make a counter over it report the drop once per line.
		if index == len(report.Lines)-1 {
			facts.Dropped = report.Dropped
		}
		observeRuntime(ctx, observer, observability.Observation{
			Component:      observability.ComponentControlPlane,
			Stage:          observability.StageSourceWithheld,
			Result:         observability.ResultSuccess,
			Direction:      observability.DirectionInternal,
			Trace:          trace,
			SourceWithheld: &facts,
		})
	}
}

// withheldScopeLevel is the scope a disposition carries when it is about one
// level rather than the whole strategy.
const withheldScopeLevel = "LEVEL"

// observeSuspendedNoDataObjects writes one line per strategy whose no-data
// half changed state this round, naming the strategy.
//
// It is the names behind catalog_no_data_plans{source=SUSPENDED_*}. Without
// them the counts say that some strategies are detecting thresholds and not
// absence, and nothing at all about which -- and the coverage list this
// migration is read from is a list of strategies, not a number. It is the same
// gap the withheld lines were added to close, for the objects that used to be
// withheld and now are not.
//
// Its own stage rather than source_withheld. These strategies are running: a
// reader who has learned that a source_withheld line means a strategy is not
// being evaluated would read these as outages, and there would be no way to
// tell them apart afterwards.
//
// Changed only, same as the withheld lines, and the disposition is not carried
// because there is only one: every one of these is ACCEPTED, and a constant
// field is a column that teaches a reader nothing while looking like it
// varies.
func observeSuspendedNoDataObjects(
	ctx context.Context,
	observer observability.Observer,
	report controlplane.WithheldReport,
) {
	for index, line := range report.Lines {
		facts := observability.SourceWithheldFacts{Reason: line.Reason, Field: line.FieldPath}
		if index == len(report.Lines)-1 {
			facts.Dropped = report.Dropped
		}
		observeRuntime(ctx, observer, observability.Observation{
			Component:      observability.ComponentControlPlane,
			Stage:          observability.StageNoDataSuspended,
			Result:         observability.ResultSuccess,
			Direction:      observability.DirectionInternal,
			Trace:          observability.TraceFields{StrategyID: line.SourceID, TerminalScope: line.Scope},
			SourceWithheld: &facts,
		})
	}
}
