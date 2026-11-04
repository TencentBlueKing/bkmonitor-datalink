// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/newopens"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// KindNewAlertsBurst: a strategy that opened more alerts in one clock hour,
// across every replica, than the threshold (newopens.Threshold), named with
// its dimension that took the most distinct values among them - the shape
// of a strategy grouped by span_name, opening one alert per request
// name. Nothing is limited or closed: alarmd keeps no
// per-strategy rate guard (the user's ruling); the line tells the
// strategy's owner which dimension to narrow. Counted for Plans on the
// standard protocol with the link only: the python-compatible protocol
// carries no open signal.
//
// Each replica lists its own share of a strategy's hour once it passes
// newopens.ReportFloor; Aggregate adds the shares of one strategy and hour
// up (FoldNewAlertsBursts) and keeps one row per strategy whose sum is past
// the threshold. A strategy's opens are split across replicas when its
// Query Groups sit on different ones or one moves within the hour.
const KindNewAlertsBurst = "NEW_ALERTS_BURST"

// NewAlertsBurst is a NEW_ALERTS_BURST row's count, for one strategy and
// one clock hour: how many alerts opened in it against the threshold, the
// dimension with the most distinct values and how many, and how many
// replicas the count is the sum of.
type NewAlertsBurst struct {
	Strategy    StrategyRef `json:"strategy"`
	WindowStart time.Time   `json:"window_start"`
	Opened      int         `json:"opened"`
	Threshold   int         `json:"threshold"`
	Dimension   string      `json:"dimension"`
	Distinct    int         `json:"distinct"`
	// DistinctCapped is a Distinct that is at least that many: the
	// dimension passed newopens.MaxDistinct, or the hour is the sum of more
	// than one replica, whose values may overlap and are not added.
	DistinctCapped bool `json:"distinct_capped,omitempty"`
	Replicas       int  `json:"replicas"`
	// ObservedAt is when a process last wrote the count. A strategy that
	// opens nothing more writes nothing more; its hours end within the
	// next one, so two hours without one is let go.
	ObservedAt time.Time `json:"observed_at"`
}

// newOpensAccount is one strategy's hours as this process last wrote them,
// and the object whose round wrote them.
type newOpensAccount struct {
	queryGroup string
	hours      []observability.NewOpensHourFact
	observedAt time.Time
}

// newAlertsBurstStale is how long a count written no more is kept.
const newAlertsBurstStale = 2 * time.Hour

// noteNewOpens keeps a new_opens line's hours for its strategy: replaced
// while any is past the floor, removed when none is - for the strategy, in
// whichever object's round says so. Called with the lock held.
func (tracker *Tracker) noteNewOpens(queryGroup string, trace observability.TraceFields, fact *observability.NewOpensFact) {
	if fact == nil || trace.StrategyID == "" {
		return
	}
	strategy := StrategyRef{StrategyID: trace.StrategyID, BusinessID: trace.BusinessID}
	if len(fact.Hours) == 0 {
		delete(tracker.newOpens, strategy)
		return
	}
	if tracker.newOpens == nil {
		tracker.newOpens = map[StrategyRef]newOpensAccount{}
	}
	if _, known := tracker.newOpens[strategy]; !known && len(tracker.newOpens) >= tracker.maxTracked {
		tracker.noteDropped(TrackerDroppedObservation, queryGroup)
		return
	}
	tracker.newOpens[strategy] = newOpensAccount{queryGroup: queryGroup,
		hours: append([]observability.NewOpensHourFact(nil), fact.Hours...), observedAt: tracker.now()}
}

// newAlertsBurstAnomalies is this replica's share of each strategy's hours
// past the report floor, one row an hour, written within two hours; not
// compared with the threshold here - Aggregate adds the replicas' shares
// up first. Called with the lock held.
func (tracker *Tracker) newAlertsBurstAnomalies() []Anomaly {
	var anomalies []Anomaly
	now := tracker.now()
	for strategy, account := range tracker.newOpens {
		if now.Sub(account.observedAt) > newAlertsBurstStale {
			delete(tracker.newOpens, strategy)
			continue
		}
		for _, hour := range account.hours {
			start := time.Unix(hour.StartUnix, 0).UTC()
			burst := NewAlertsBurst{Strategy: strategy, WindowStart: start, Opened: hour.Opened, Threshold: newopens.Threshold,
				Dimension: hour.Dimension, Distinct: hour.Distinct, DistinctCapped: hour.DistinctCapped, Replicas: 1,
				ObservedAt: account.observedAt}
			anomalies = append(anomalies, Anomaly{QueryGroup: account.queryGroup, Kind: KindNewAlertsBurst,
				ReasonCode: "NEW_ALERTS_PAST_THRESHOLD", Since: start, SinceFrom: SinceSnapshotContinuity,
				Replica: tracker.replica, Strategies: []StrategyRef{strategy}, NewAlertsBurst: &burst})
		}
	}
	sort.Slice(anomalies, func(left, right int) bool {
		if anomalies[left].Strategies[0].StrategyID != anomalies[right].Strategies[0].StrategyID {
			return anomalies[left].Strategies[0].StrategyID < anomalies[right].Strategies[0].StrategyID
		}
		return anomalies[left].Since.Before(anomalies[right].Since)
	})
	return anomalies
}

// FoldNewAlertsBursts adds the replicas' shares of each strategy's hour up
// and keeps, per strategy, one row for its latest hour whose sum is past
// the threshold; every other row passes through as it is. The row is the
// largest share's, with the sum, the widest dimension any share names
// (at least that many when more than one replica counted), and how many
// replicas the sum is of.
func FoldNewAlertsBursts(rows []Anomaly) []Anomaly {
	type hourKey struct {
		strategy StrategyRef
		start    int64
	}
	sums := map[hourKey]*Anomaly{}
	largest := map[hourKey]int{}
	var order []hourKey
	kept := make([]Anomaly, 0, len(rows))
	for _, row := range rows {
		if row.Kind != KindNewAlertsBurst || row.NewAlertsBurst == nil {
			kept = append(kept, row)
			continue
		}
		share := *row.NewAlertsBurst
		key := hourKey{strategy: share.Strategy, start: share.WindowStart.Unix()}
		sum, ok := sums[key]
		if !ok {
			row.NewAlertsBurst = &share
			sums[key], largest[key] = &row, share.Opened
			order = append(order, key)
			continue
		}
		total := sum.NewAlertsBurst
		if share.Opened > largest[key] {
			sum.QueryGroup, sum.Replica, largest[key] = row.QueryGroup, row.Replica, share.Opened
		}
		total.Opened += share.Opened
		total.Replicas += share.Replicas
		total.DistinctCapped = true
		if share.Distinct > total.Distinct {
			total.Dimension, total.Distinct = share.Dimension, share.Distinct
		}
		if share.ObservedAt.After(total.ObservedAt) {
			total.ObservedAt = share.ObservedAt
		}
	}
	latestHour := map[StrategyRef]*Anomaly{}
	for _, key := range order {
		sum := sums[key]
		if sum.NewAlertsBurst.Opened <= newopens.Threshold {
			continue
		}
		if held, ok := latestHour[key.strategy]; !ok || sum.NewAlertsBurst.WindowStart.After(held.NewAlertsBurst.WindowStart) {
			latestHour[key.strategy] = sum
		}
	}
	strategies := make([]StrategyRef, 0, len(latestHour))
	for strategy := range latestHour {
		strategies = append(strategies, strategy)
	}
	sortStrategies(strategies)
	for _, strategy := range strategies {
		kept = append(kept, *latestHour[strategy])
	}
	return kept
}

// newAlertsBurstClause is the line's numbers: the hour's opens against the
// threshold, and the dimension that took the most values.
func newAlertsBurstClause(burst *NewAlertsBurst) string {
	if burst == nil {
		return ""
	}
	clause := fmt.Sprintf("一小时内新开告警 %d 条（发现线 %d 条）", burst.Opened, burst.Threshold)
	if burst.Replicas > 1 {
		clause = fmt.Sprintf("一小时内新开告警 %d 条（%d 个副本合计，发现线 %d 条）", burst.Opened, burst.Replicas, burst.Threshold)
	}
	if burst.Dimension == "" {
		return clause
	}
	if burst.DistinctCapped {
		return clause + fmt.Sprintf("，取值最多的维度 %s 有至少 %d 个不同取值", burst.Dimension, burst.Distinct)
	}
	return clause + fmt.Sprintf("，取值最多的维度 %s 有 %d 个不同取值", burst.Dimension, burst.Distinct)
}
