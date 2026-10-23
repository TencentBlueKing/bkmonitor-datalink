package uq

import (
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// seriesDecoder is the shared business path of both wire formats. Framing is
// separate; response budgets, immutable normalization and completion are not.
type seriesDecoder struct {
	client                                        *Client
	attempt                                       queryIdentity
	sink                                          execution.ProviderSeriesSink
	scanned                                       *DiagnosticScan
	ref                                           execution.ProviderResultRef
	delivery                                      execution.SeriesDelivery
	totalSeries, totalRecords, nullIdentityFields uint64
	cut                                           *termsCutCounter
	offGrid                                       bool
	receivedAt                                    int64
	decodeStarted                                 time.Time
}

func (client *Client) newSeriesDecoder(attempt queryIdentity, sink execution.ProviderSeriesSink, scanned *DiagnosticScan) *seriesDecoder {
	return &seriesDecoder{client: client, attempt: attempt, sink: sink, scanned: scanned,
		ref: providerResultRef(attempt), cut: newTermsCutCounter(attempt.Spec), decodeStarted: client.now()}
}

func (session *seriesDecoder) accept(ctx context.Context, series responseSeries, encodedBytes uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if encodedBytes > uint64(session.client.limits.MaxSeriesBytes) {
		return ErrSeriesBytesExceeded
	}
	session.totalSeries++
	session.cut.add(series)
	if session.scanned != nil {
		session.scanned.Series = session.totalSeries
		session.scanned.Records += uint64(len(series.Values))
	}
	if session.totalSeries > session.client.limits.MaxSeries {
		return ErrTotalSeriesExceeded
	}
	if uint64(len(series.Values)) > session.client.limits.MaxRecords-session.totalRecords {
		return ErrTotalRecordsExceeded
	}
	session.totalRecords += uint64(len(series.Values))
	batch, nullFields, err := normalizeSeries(session.attempt.Spec, session.ref, series, session.receivedAt)
	if errors.Is(err, errOffRequestGrid) {
		session.offGrid = true
		return nil
	}
	if err != nil {
		return err
	}
	session.nullIdentityFields += nullFields
	if batch.Dataset.Len() == 0 {
		return nil
	}
	batch.Delivery.Bytes = encodedBytes
	if err := session.sink.ConsumeProviderSeries(ctx, batch); err != nil {
		return err
	}
	session.delivery, err = execution.AccumulateSeriesDelivery(session.delivery, batch.Delivery)
	return err
}

// limitCompletion preserves the existing deterministic response-limit outcome,
// including accounting for everything already passed to the sink.
func (session *seriesDecoder) limitCompletion(completion *execution.ProviderCompletion, err *error) {
	var limit *responseLimitError
	if *err == nil || session.scanned != nil || !errors.As(*err, &limit) {
		return
	}
	dataState := execution.DataStateEmpty
	if session.delivery.Records > 0 {
		dataState = execution.DataStateData
	}
	*completion = session.client.responseContractUnavailable(session.attempt, execution.ReasonCode(contract.ReasonQueryUnavailable),
		execution.ResponseRouteDetail(limit.class), dataState, session.delivery, nil,
		execution.ProviderStats{Series: session.delivery.Series, Records: session.delivery.Records})
	*err = nil
}

func (session *seriesDecoder) finish(status *responseStatus, isPartial *bool, resultTableIDs []string) (execution.ProviderCompletion, error) {
	client, attempt, ref := session.client, session.attempt, session.ref
	delivery, nullIdentityFields := session.delivery, session.nullIdentityFields
	decodeStarted, offGrid, cut := session.decodeStarted, session.offGrid, session.cut
	dataState := execution.DataStateEmpty
	if delivery.Records > 0 {
		dataState = execution.DataStateData
	}
	stats := execution.ProviderStats{Series: delivery.Series, Records: delivery.Records, NullIdentityFields: nullIdentityFields,
		DecodeMillis: uint64(client.now().Sub(decodeStarted).Milliseconds())}
	passthroughDetail := ""
	var passthroughStatus *execution.ProviderStatusFact
	if status != nil && status.Code != "" && status.Code != queryTSPartial {
		if !usableDespiteStatus(status.Code, delivery) {
			// A non-partial status code is a deterministic answer for this table
			// and field. Completing it as UNAVAILABLE lets the Slot finish with a
			// Plan gap instead of failing and re-querying UQ on every attempt
			// until the Slot ages out. UQ writes the series array before status,
			// so series decoded before the status token have already reached the
			// sink; the completion keeps their DataState and Delivery only so
			// that delivery conservation holds. The consumer never receives them:
			// every binding of an UNAVAILABLE completion is UNKNOWN.
			// A table or field that does not route in the space is named for
			// what it is: the strategy's data is not where it points, which no
			// retry and no backend recovery changes.
			reason := execution.ReasonCode(contract.ReasonQueryUnavailable)
			if _, targetMissing := dataExistenceStatusCodes[status.Code]; targetMissing {
				reason = execution.ReasonCode(contract.ReasonQueryTargetMissing)
			}
			unavailable := client.responseContractUnavailable(attempt, reason, execution.ResponseStatusRouteDetail(status.Code),
				dataState, delivery, resultTableIDs, stats)
			unavailable.RouteFacts.Status = &execution.ProviderStatusFact{Code: status.Code}
			return unavailable, nil
		}
		// The code is kept on the succeeded attempt because this is now the only
		// place it exists. Before, a code always produced an UNAVAILABLE
		// completion, so it was visible by making the Slot fail loudly; letting
		// the series through removes that, and nothing in alarmd counts UQ status
		// codes. Dropping it here would turn the failure this fixes into a silent
		// one: a result table that is genuinely renamed would route nowhere, the
		// fallback would answer 100, and the strategy would report itself healthy
		// forever with nothing to look at. A loud wrong answer is discoverable -
		// this whole defect was found because 34 hours of nothing was
		// conspicuous.
		passthroughDetail = execution.ResponseStatusRouteDetail(status.Code)
		passthroughStatus = &execution.ProviderStatusFact{Code: status.Code, Allowed: true}
	}
	if offGrid {
		// No point of it is used: a bucket starting anywhere but where the
		// window starts covers part of the window and part of another, and
		// reads as a detection at the wrong time. The strategy's step cannot
		// be read from this table's storage.
		return client.responseContractUnavailable(attempt, execution.ReasonCode(contract.ReasonDetectIntervalStorageNotSliding),
			execution.ResponseRouteDetail(execution.ResponseFailureOffRequestGrid), dataState, delivery, resultTableIDs, stats), nil
	}
	if isPartial == nil {
		return client.responseContractUnavailable(attempt, execution.ReasonCode(contract.ReasonQueryUnavailable),
			execution.ResponseRouteDetail(execution.ResponseFailureIsPartialMissing), dataState, delivery, resultTableIDs, stats), nil
	}
	completeness := execution.CompletenessFull
	if *isPartial || status != nil && status.Code == queryTSPartial {
		completeness = execution.CompletenessPartial
	}
	var truncation *execution.ProviderTruncationFact
	if dimension, suspected := cut.suspected(); suspected {
		truncation = &execution.ProviderTruncationFact{Kind: execution.TruncationTermsCut, Dimension: dimension, Cap: esTermsCap,
			SourceSemantics: cut.source}
	}
	return execution.ProviderCompletion{Ref: ref, PhysicalQuery: attempt.Spec.Digest,
		Completeness: completeness, DataState: dataState, Delivery: delivery,
		RouteFacts: execution.ProviderRouteFacts{ProviderRouteRef: attempt.Spec.PlanFacts.ProviderRouteRef,
			ResultTableIDs: append([]string(nil), resultTableIDs...), Status: passthroughStatus, Truncation: truncation,
			Attempts: []execution.RouteAttemptFact{{AttemptNo: attempt.AttemptNo,
				Endpoint: client.endpoint, Result: execution.RouteAttemptSucceeded, Detail: passthroughDetail}}},
		Stats: stats}, nil
}
