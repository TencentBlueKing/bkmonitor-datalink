// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The states an output of a named_outputs/v1 answer is in, as the provider
// writes them.
const (
	answerSuccess      = "SUCCESS"
	answerSuccessEmpty = "SUCCESS_EMPTY"
	answerPartial      = "PARTIAL"
	answerError        = "ERROR"
)

// namedOutputName is the label PromQL names a series' metric by. It is left
// out of the key the outputs are aligned on, as the platform leaves it out
// (bk-monitor 458f31c537, bkmonitor/data_source/unify_query/query.py:516,
// from #12789; the UQ 007 spec's alignment rule, spec.md:49): a bare
// selector keeps it and an expression over it drops it, so two outputs of
// one strategy would otherwise never meet. Only the key leaves it out: the
// series' own identity keeps every label it has.
const namedOutputName = "__name__"

// namedOutputAnswer is one entry of a named_outputs/v1 answer's outputs.
type namedOutputAnswer struct {
	ReferenceName string            `json:"reference_name"`
	State         string            `json:"state"`
	Series        []json.RawMessage `json:"series"`
	Status        *responseStatus   `json:"status"`
	IsPartial     *bool             `json:"is_partial"`
}

// alignKey is where a point sits: its source time, as detection's records
// keep it, and its labels without __name__.
type alignKey struct {
	time   int64
	labels string
}

// legacyPoint is one record the legacy output delivered, as the alignment
// needs it.
type legacyPoint struct {
	recordID string
	key      alignKey
	value    json.RawMessage
}

// answeredOutput is a non-legacy output as it answered: its state and its
// points by where they sit.
type answeredOutput struct {
	state  execution.NamedOutputState
	points map[alignKey]json.RawMessage
}

// namedOutputDecoding is a named-outputs query's answer as it is read: the
// legacy output goes the way every query's series go and is noted point by
// point as it is delivered; every other output is indexed. Nothing is
// aligned until the whole answer is in, because the outputs come in the
// order the request listed them and an output's state is not known before
// its entry ends.
type namedOutputDecoding struct {
	outputs *execution.NamedOutputs
	// contract is the answer's contract_version, empty for a provider that
	// answered as if nothing was asked (the older provider).
	contract string
	legacy   *namedOutputAnswer
	answered map[string]*answeredOutput
	points   []legacyPoint
	// malformed is an answer that broke the contract: an output named twice
	// or by no name. duplicate is an output with two points at one key.
	malformed bool
	duplicate bool
	// held is what the alignment holds so far, by the estimates below, and
	// reserved what it has reserved for it through reserver
	// (queryIdentity.NamedOutputs), in chunks ahead of held; nil reserves
	// nothing.
	held     uint64
	reserved uint64
	reserver execution.RetainedBytesReserver
}

// namedReserveChunk is how far ahead of what it holds the alignment
// reserves: one reservation for many points rather than one for each.
const namedReserveChunk = 64 << 10

// What the alignment holds while an answer is read, measured
// (TestTheNamedOutputHoldingsAreWhatTheyCost): an indexed point of an
// output, its key and value in the index, and a delivered point of the
// legacy output, kept until the answer ends; each beside the bytes of its
// value, and the legacy point beside its RecordID. An output's series are
// held as they arrived while it is indexed, and charged as they are.
const (
	namedIndexPointBytes  = 96
	namedLegacyPointBytes = 96
)

// errNamedOutputBytesExceeded is an alignment its Slot refused to reserve
// for: past the share one Query Group's Slot may hold, or past what the
// pool has left, as a series past either is refused. The answer is too
// large for the Slot to hold, as an answer over the body limit is.
var errNamedOutputBytesExceeded error = &responseLimitError{code: "NAMED_OUTPUT_BYTES_EXCEEDED",
	class: execution.ResponseFailureLimitResponseBytes}

func newNamedOutputDecoding(attempt queryIdentity) *namedOutputDecoding {
	if attempt.Spec.PlanFacts.NamedOutputs == nil {
		return nil
	}
	return &namedOutputDecoding{outputs: attempt.Spec.PlanFacts.NamedOutputs, answered: map[string]*answeredOutput{},
		reserver: attempt.NamedOutputs}
}

// namedOutputReserver is what a query with named outputs reserves its
// alignment through: the sink its series go to, when it reserves
// (execution.RetainedBytesReserver), so the alignment is held to the Slot's
// own budget as it grows. Nil for a query without named outputs.
func namedOutputReserver(spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) execution.RetainedBytesReserver {
	if spec.PlanFacts.NamedOutputs == nil {
		return nil
	}
	reserver, _ := sink.(execution.RetainedBytesReserver)
	return reserver
}

// hold counts bytes the alignment holds and reserves them before it holds
// them, a chunk ahead, refusing them the moment its Slot refuses.
func (named *namedOutputDecoding) hold(ctx context.Context, bytes uint64) error {
	named.held += bytes
	if named.reserver == nil || named.held <= named.reserved {
		return nil
	}
	need := named.held - named.reserved
	chunk := max(need, namedReserveChunk)
	err := named.reserver.ReserveRetainedBytes(ctx, chunk)
	if err != nil && chunk > need {
		// The chunk ahead is a convenience: what is held now may still fit.
		chunk = need
		err = named.reserver.ReserveRetainedBytes(ctx, chunk)
	}
	if err != nil {
		return errNamedOutputBytesExceeded
	}
	named.reserved += chunk
	return nil
}

// settle gives back what the alignment reserved beyond what it keeps once
// its answer is read: the aligned table of a completion that carries one
// (NamedOutputRecords.RetainedBytes), which its Slot then holds; nothing of
// an answer that failed or was refused. The index and the kept points are
// let go with it. Safe on a query without named outputs.
func (named *namedOutputDecoding) settle(completion execution.ProviderCompletion, err error) {
	if named == nil {
		return
	}
	named.answered, named.points, named.legacy = nil, nil, nil
	keep := uint64(0)
	if err == nil {
		keep = min(completion.NamedOutputs.RetainedBytes(), named.reserved)
	}
	if named.reserver != nil && named.reserved > keep {
		named.reserver.ReleaseRetainedBytes(named.reserved - keep)
	}
	named.reserved, named.held = keep, keep
}

// alignLabels is a series' labels as the alignment keys them, as the
// platform keys them (extract_unify_query_series_dimensions, then
// _index_named_output_records): each group key without its _tableN suffix,
// a later key standing for an earlier one it then equals, __name__ left
// out, and the pairs sorted, each value as the provider wrote it.
func alignLabels(series responseSeries) string {
	labels := make(map[string]string, len(series.GroupKeys))
	for index, key := range series.GroupKeys {
		value := nullDimension
		if index < len(series.GroupValues) {
			value = series.GroupValues[index]
		}
		labels[stripTableSuffix(key)] = string(value)
	}
	delete(labels, namedOutputName)
	pairs := make([]string, 0, len(labels))
	for key, value := range labels {
		pairs = append(pairs, key+"\x00"+value)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "\x01")
}

// acceptOutputs reads the outputs array of a named_outputs/v1 answer: the
// legacy output's series are accepted as any query's are, every other
// output is indexed.
func (session *seriesDecoder) acceptOutputs(ctx context.Context, decoder *json.Decoder) error {
	named := session.named
	start, err := decoder.Token()
	if err != nil || start != json.Delim('[') {
		return fmt.Errorf("alarmd access uq: outputs must be an array")
	}
	seen := make(map[string]bool, len(named.outputs.Outputs))
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var answer namedOutputAnswer
		if err := decoder.Decode(&answer); err != nil {
			return fmt.Errorf("alarmd access uq: decode output: %w", err)
		}
		if answer.ReferenceName == "" || seen[answer.ReferenceName] {
			named.malformed = true
			continue
		}
		seen[answer.ReferenceName] = true
		if answer.ReferenceName == named.outputs.LegacyRef {
			for _, raw := range answer.Series {
				if err := session.acceptRaw(ctx, raw); err != nil {
					return err
				}
			}
			answer.Series = nil
			named.legacy = &answer
			continue
		}
		if !named.asked(answer.ReferenceName) {
			// An output nobody asked for is no output of this query.
			continue
		}
		if err := session.indexOutput(ctx, answer); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func (named *namedOutputDecoding) asked(ref string) bool {
	for _, output := range named.outputs.Outputs {
		if output.Ref == ref {
			return true
		}
	}
	return false
}

// acceptRaw is one series of the answer as it arrived, accepted.
func (session *seriesDecoder) acceptRaw(ctx context.Context, raw json.RawMessage) error {
	if int64(len(raw)) > session.client.limits.MaxSeriesBytes {
		return ErrSeriesBytesExceeded
	}
	var series responseSeries
	if err := json.Unmarshal(raw, &series); err != nil {
		return fmt.Errorf("alarmd access uq: decode series: %w", err)
	}
	return session.accept(ctx, series, uint64(len(raw)))
}

// indexOutput keeps every point of one non-legacy output by where it sits.
// Its series count against the same response limits the legacy output's
// do: they are read into memory as theirs are.
func (session *seriesDecoder) indexOutput(ctx context.Context, answer namedOutputAnswer) error {
	spec := session.attempt.Spec
	output := &answeredOutput{state: answeredState(answer), points: map[alignKey]json.RawMessage{}}
	session.named.answered[answer.ReferenceName] = output
	for _, raw := range answer.Series {
		if err := ctx.Err(); err != nil {
			return err
		}
		if int64(len(raw)) > session.client.limits.MaxSeriesBytes {
			return ErrSeriesBytesExceeded
		}
		if err := session.named.hold(ctx, uint64(len(raw))); err != nil {
			return err
		}
		var series responseSeries
		if err := json.Unmarshal(raw, &series); err != nil {
			return fmt.Errorf("alarmd access uq: decode series: %w", err)
		}
		session.totalSeries++
		if session.totalSeries > session.client.limits.MaxSeries {
			return ErrTotalSeriesExceeded
		}
		if uint64(len(series.Values)) > session.client.limits.MaxRecords-session.totalRecords {
			return ErrTotalRecordsExceeded
		}
		session.totalRecords += uint64(len(series.Values))
		if len(series.Columns) == 0 || len(series.Columns) != len(series.Types) {
			return fmt.Errorf("alarmd access uq: invalid series schema")
		}
		labels := alignLabels(series)
		for _, row := range series.Values {
			if len(row) != len(series.Columns) {
				return fmt.Errorf("alarmd access uq: row width differs from columns")
			}
			timestamp, value, err := rowFacts(series.Columns, row, spec.PlanFacts.QueryList)
			if err != nil {
				return err
			}
			sourceTime, err := spec.PlanFacts.Normalization.NormalizeSourceTime(timestamp)
			if err != nil {
				return err
			}
			key := alignKey{time: sourceTime, labels: labels}
			if _, exists := output.points[key]; exists {
				session.named.duplicate = true
				continue
			}
			if err := session.named.hold(ctx, namedIndexPointBytes+uint64(len(value))); err != nil {
				return err
			}
			output.points[key] = value
		}
	}
	return nil
}

// answeredState is the state an output answered in, as an aligned value
// reads it: a state the provider left out is read from is_partial, as the
// platform reads it; an empty success is a success with no points; and a
// state this build does not know is read as an error, whose value nobody
// shows.
func answeredState(answer namedOutputAnswer) execution.NamedOutputState {
	switch answer.State {
	case answerSuccess, answerSuccessEmpty:
		return execution.NamedOutputSuccess
	case answerPartial:
		return execution.NamedOutputPartial
	case answerError:
		return execution.NamedOutputError
	case "":
		if answer.IsPartial != nil && *answer.IsPartial {
			return execution.NamedOutputPartial
		}
		return execution.NamedOutputSuccess
	default:
		return execution.NamedOutputError
	}
}

// noteDelivered keeps the points of a legacy series that was delivered.
func (named *namedOutputDecoding) noteDelivered(ctx context.Context, series responseSeries, dataset *execution.Dataset, valueField string) error {
	labels := alignLabels(series)
	for index := 0; index < dataset.Len(); index++ {
		record, _ := dataset.Record(index)
		value, _ := record.Value(valueField)
		if err := named.hold(ctx, namedLegacyPointBytes+uint64(len(record.RecordID())+len(value))); err != nil {
			return err
		}
		named.points = append(named.points, legacyPoint{recordID: record.RecordID(),
			key: alignKey{time: record.SourceTime(), labels: labels}, value: value})
	}
	return nil
}

// legacyAnswer is the status and is_partial the legacy output answered with,
// read where the older answer's top level carries them. The legacy output's
// own are all that decide its completion: the answer's top level gathers
// every output's, and an optional output that failed does not fail the
// query.
//
// An ERROR output carries no series and its own status; one that came
// without a status is given one, so that it fails the query as every status
// code does rather than reading as a query that found nothing - a timed-out
// legacy output is not an empty one.
func (named *namedOutputDecoding) legacyAnswer() (*responseStatus, *bool) {
	status, isPartial := named.legacy.Status, named.legacy.IsPartial
	switch named.legacy.State {
	case answerError:
		if status == nil || status.Code == "" {
			status = &responseStatus{Code: answerError}
		}
	case answerPartial:
		if isPartial != nil && !*isPartial {
			partial := true
			isPartial = &partial
		}
	}
	return status, isPartial
}

// finishNamed completes a named-outputs query's answer: its legacy output as
// any query's answer, then every output aligned to the records it
// delivered. An answer that broke the contract, or an output with two points
// at one key, is a query that is not used.
func (session *seriesDecoder) finishNamed(ctx context.Context, status *responseStatus, isPartial *bool, resultTableIDs []string) (execution.ProviderCompletion, error) {
	named := session.named
	refused := ""
	switch {
	case named.contract == "":
		// The older provider answered as if nothing was asked: the top level
		// is the legacy output's.
	case named.contract != execution.NamedOutputsContract || named.malformed || named.legacy == nil:
		refused = execution.ResponseFailureNamedOutputContract
	default:
		status, isPartial = named.legacyAnswer()
	}
	completion, err := session.finish(ctx, status, isPartial, resultTableIDs)
	if err != nil || completion.Completeness == execution.CompletenessUnavailable && refused == "" {
		return completion, err
	}
	reason := execution.ReasonCode(contract.ReasonQueryUnavailable)
	if refused == "" {
		records, err := named.align(ctx, completion.Completeness)
		if err != nil {
			return execution.ProviderCompletion{}, err
		}
		if records != nil && !named.duplicate {
			completion.NamedOutputs = records
			return completion, nil
		}
		refused, reason = execution.ResponseFailureNamedOutputDuplicateKey, execution.ReasonCode(contract.ReasonQueryNamedOutputDuplicateKey)
	}
	// The legacy series were handed on already; the completion keeps their
	// state and delivery only so that delivery is conserved, and every
	// binding of an unavailable completion is UNKNOWN.
	return session.client.responseContractUnavailable(session.attempt, reason, execution.ResponseRouteDetail(refused),
		completion.DataState, completion.Delivery, completion.RouteFacts.ResultTableIDs, completion.Stats), nil
}

// align is every output at every record the legacy output delivered, as
// the platform aligns them (process_named_outputs_data): the legacy output
// at its own value, in the state the query completed in; an output that
// answered at the record's key at the value it answered, in its own state;
// one that did not, in its own state when that is ERROR or UNSUPPORTED and
// MISSING otherwise. An output of an answer under no contract is
// UNSUPPORTED, and so is one the answer left out. Nil when two records of
// the legacy output sit at one key.
//
// The table is held beside the index and the kept points until the answer
// is settled, so it is counted as it is built, record by record, at what it
// is charged at (execution.NamedOutputRecordRetainedBytes), with the keys
// it is checked against: the alignment's peak is counted, and what is left
// reserved once the index and the points go is the table's own.
func (named *namedOutputDecoding) align(ctx context.Context, completeness execution.Completeness) (*execution.NamedOutputRecords, error) {
	legacyState := execution.NamedOutputSuccess
	if completeness == execution.CompletenessPartial {
		legacyState = execution.NamedOutputPartial
	}
	refs := make([]string, 0, len(named.outputs.Outputs))
	answers := make([]*answeredOutput, 0, len(named.outputs.Outputs))
	for _, output := range named.outputs.Outputs {
		refs = append(refs, output.Ref)
		answers = append(answers, named.answered[output.Ref])
	}
	if err := named.hold(ctx, execution.NamedOutputRefsRetainedBytes(refs)); err != nil {
		return nil, err
	}
	records := &execution.NamedOutputRecords{Refs: refs, LegacyRef: named.outputs.LegacyRef,
		Records: make(map[string][]execution.NamedOutputValue, len(named.points))}
	keys := make(map[alignKey]bool, len(named.points))
	for _, point := range named.points {
		if keys[point.key] {
			return nil, nil
		}
		keys[point.key] = true
		values := make([]execution.NamedOutputValue, len(refs))
		for index, ref := range refs {
			answer := answers[index]
			switch {
			case ref == named.outputs.LegacyRef:
				values[index] = execution.NamedOutputValue{Value: point.value, State: legacyState}
			case answer == nil:
				values[index] = execution.NamedOutputValue{State: execution.NamedOutputUnsupported}
			default:
				if value, found := answer.points[point.key]; found {
					values[index] = execution.NamedOutputValue{Value: value, State: answer.state}
				} else if answer.state == execution.NamedOutputError || answer.state == execution.NamedOutputUnsupported {
					values[index] = execution.NamedOutputValue{State: answer.state}
				} else {
					values[index] = execution.NamedOutputValue{State: execution.NamedOutputMissing}
				}
			}
		}
		if err := named.hold(ctx, namedIndexPointBytes+execution.NamedOutputRecordRetainedBytes(point.recordID, values)); err != nil {
			return nil, err
		}
		records.Records[point.recordID] = values
	}
	return records, nil
}
