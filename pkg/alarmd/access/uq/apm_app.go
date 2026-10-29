// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"regexp"
	"slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The APM application a series belongs to is asked of the provider
// alongside the strategy's own dimensions, so that the event can name its
// APM object from the data rather than from a guess.
//
// An APM metric or trace table holds one application: the platform creates
// one per application, under the application's normalised name, and keeps
// those names unique. Grouping such a query by app_name therefore returns
// the same series it returned without, each carrying its application -- the
// name as the application wrote it, hyphens included, which the table's own
// name cannot give back. It is added here, to the request body, and nowhere
// else: the compiled plan, the Query Group's identity, the series identity,
// the state keys and the alert fingerprint are all what they were, because
// none of them is built from the body. The record carries app_name beside
// its identity dimensions; only the APM object reads it.
//
// It is added only when every clause reads the same APM table. Clauses on
// different tables are joined by the provider on their group keys, and a
// key one side has and the other does not, or that differs between two
// applications, would change the join.
const apmAppDimension = "app_name"

var apmTable = regexp.MustCompile(`^(?:space_)?\d+_bkapm_(metric|trace)_[A-Za-z0-9_-]+(?:\.__default__)?$`)

// carriesAPMApp says whether spec's request asks for the APM application.
func carriesAPMApp(spec execution.PhysicalQuerySpec) bool {
	facts := spec.PlanFacts
	if facts.PromQL != nil || len(facts.QueryList) == 0 {
		return false
	}
	table := facts.QueryList[0].TableID
	if !apmTable.MatchString(table) {
		return false
	}
	for _, clause := range facts.QueryList {
		if clause.TableID != table {
			return false
		}
	}
	return true
}

// withAPMApp is clause grouped by app_name as well: its dimensions, the
// columns it keeps, and each aggregation grouped by the clause's dimensions.
// A clause that already groups by app_name is returned as it is.
func withAPMApp(clause execution.QueryClause) execution.QueryClause {
	if slices.Contains(clause.Dimensions, apmAppDimension) {
		return clause
	}
	groupBy := clause.Dimensions
	clause.Dimensions = append(append([]string(nil), groupBy...), apmAppDimension)
	if len(clause.KeepColumns) > 0 && !slices.Contains(clause.KeepColumns, apmAppDimension) {
		clause.KeepColumns = append(append([]string(nil), clause.KeepColumns...), apmAppDimension)
	}
	functions := append([]execution.QueryFunction(nil), clause.Functions...)
	for index := range functions {
		if slices.Equal(functions[index].Dimensions, groupBy) {
			functions[index].Dimensions = append(append([]string(nil), groupBy...), apmAppDimension)
		}
	}
	clause.Functions = functions
	return clause
}

// APMCarriedResponse is one response read with the APM application carried:
// the kind of APM table (metric or trace), the series and bytes it held
// before it was handed on, and whether an identity came back more than once.
type APMCarriedResponse struct {
	Table         string
	Series, Bytes uint64
	NotUnique     bool
}

// APM table kinds, the label APMCarriedResponse reports them under.
const (
	APMTableMetric = "metric"
	APMTableTrace  = "trace"
)

// WithAPMObserver hears every response read with the APM application
// carried; nil keeps none.
func (client *Client) WithAPMObserver(observe func(APMCarriedResponse)) *Client {
	if client != nil {
		client.observeAPM = observe
	}
	return client
}

// executeCarryingAPMApp reads a query with the APM application carried and
// hands its series on only once it has checked the premise the carry rests
// on: that the extra key splits nothing. Each identity must come back once.
// An identity that comes back twice is a table holding two applications;
// grouping by app_name has then split one series into two, each with part
// of the data, and evaluating them would be evaluating values the strategy
// never asked for. The response is discarded, said (NotUnique), and the
// query read again exactly as the strategy asks it, without the
// application: the values are the ones the strategy always had and the
// records carry no app_name, so no APM object is named from a guess either.
//
// The series wait for the whole response, bounded by the response limits
// every read has; APMCarriedResponse reports what that held. While they
// wait they are held to the Slot's retained budget as the series of a
// plain read are: each is reserved through the sink as it arrives
// (execution.RetainedBytesReserver), and the read is refused, with the
// Slot's own refusal, at the first one the Slot has no room for, not once
// the whole response is in. A series' reservation is given back the moment
// it is handed on, when the sink takes its own charge for it, and whatever
// is still reserved is given back when the read ends, before a read again.
func (client *Client) executeCarryingAPMApp(callerCtx, ctx context.Context, attempt queryIdentity, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	reserver, _ := sink.(execution.RetainedBytesReserver)
	held := &apmHeldSeries{seen: map[string]struct{}{}, reserver: reserver}
	defer held.release()
	completion, err := client.executeRequest(callerCtx, ctx, attempt, held, nil)
	if client.observeAPM != nil {
		client.observeAPM(APMCarriedResponse{Table: apmTableKind(attempt.Spec), Series: uint64(len(held.batches)), Bytes: held.bytes, NotUnique: held.repeated})
	}
	if err != nil {
		return completion, err
	}
	if held.repeated {
		held.release()
		attempt.NoAppCarry = true
		return client.executeRequest(callerCtx, ctx, attempt, sink, nil)
	}
	for index := range held.batches {
		if err := sink.ConsumeProviderSeries(ctx, held.handOff(index)); err != nil {
			return execution.ProviderCompletion{}, err
		}
	}
	return completion, nil
}

func apmTableKind(spec execution.PhysicalQuerySpec) string {
	if len(spec.PlanFacts.QueryList) > 0 {
		if match := apmTable.FindStringSubmatch(spec.PlanFacts.QueryList[0].TableID); len(match) == 2 && match[1] == APMTableTrace {
			return APMTableTrace
		}
	}
	return APMTableMetric
}

// apmHeldSeries keeps a carried response's series until it is known whole.
// reserver is what they are reserved through, nil for a sink that reserves
// nothing, and reserved what is still reserved for the series not yet
// handed on: each series' delivered bytes, what the sink charges for its
// data when it takes it.
type apmHeldSeries struct {
	batches  []execution.ProviderSeriesBatch
	seen     map[string]struct{}
	bytes    uint64
	repeated bool
	reserver execution.RetainedBytesReserver
	reserved uint64
}

func (held *apmHeldSeries) ConsumeProviderSeries(ctx context.Context, batch execution.ProviderSeriesBatch) error {
	if held.reserver != nil {
		if err := held.reserver.ReserveRetainedBytes(ctx, batch.Delivery.Bytes); err != nil {
			return err
		}
		held.reserved += batch.Delivery.Bytes
	}
	held.bytes += batch.Delivery.Bytes
	if records := batch.Dataset.Records(); len(records) > 0 {
		identity := records[0].DimensionIdentity.Digest
		if _, again := held.seen[identity]; again {
			held.repeated = true
		}
		held.seen[identity] = struct{}{}
	}
	held.batches = append(held.batches, batch)
	return nil
}

// handOff is the series held at index, let go of here and its reservation
// given back, for the sink it is handed to to charge as its own. Given back
// first rather than after: kept until the sink had charged it, the series
// would be counted twice for a moment, and a Slot with room for it once
// refused.
func (held *apmHeldSeries) handOff(index int) execution.ProviderSeriesBatch {
	batch := held.batches[index]
	held.batches[index] = execution.ProviderSeriesBatch{}
	held.giveBack(batch.Delivery.Bytes)
	return batch
}

// release lets go of every series still held and gives back what is still
// reserved for them.
func (held *apmHeldSeries) release() {
	held.giveBack(held.reserved)
	held.batches, held.seen = nil, nil
}

func (held *apmHeldSeries) giveBack(bytes uint64) {
	bytes = min(bytes, held.reserved)
	if bytes == 0 {
		return
	}
	held.reserved -= bytes
	held.reserver.ReleaseRetainedBytes(bytes)
}
