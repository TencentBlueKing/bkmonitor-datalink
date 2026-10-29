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
	// framed is set on an answer read a frame at a time, which hands on a
	// frame's series before it reads the next (handOn): only its series are
	// made canonical side by side. An answer read as one document hands each
	// on before it reads on, so a series that came is delivered even when
	// the rest does not. pending is the series accepted and not yet handed
	// on, oldest first; failed is the first error handing one on met, after
	// which none is.
	framed  bool
	pending []normalizing
	failed  error
	// named reads the answer of a query that asked for named outputs; nil
	// for every other query.
	named *namedOutputDecoding
}

func (client *Client) newSeriesDecoder(attempt queryIdentity, sink execution.ProviderSeriesSink, scanned *DiagnosticScan) *seriesDecoder {
	return &seriesDecoder{client: client, attempt: attempt, sink: sink, scanned: scanned,
		ref: providerResultRef(attempt), cut: newTermsCutCounter(attempt.Spec), decodeStarted: client.now(),
		named: newNamedOutputDecoding(attempt)}
}

func (session *seriesDecoder) accept(ctx context.Context, series responseSeries, encodedBytes uint64) error {
	if session.failed != nil {
		return session.failed
	}
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
	session.pending = append(session.pending, normalizing{series: series, encodedBytes: encodedBytes})
	if session.framed {
		return nil
	}
	return session.handOn(ctx)
}

// handOn makes the series accepted canonical - side by side on the workers
// free (normalizeSideBySide) - and hands them on here, one after another in
// the order they came, as when each was handed on as it came: the sink sees
// the same series in the same order, and the delivery digest chains them
// the same way. The first error one meets ends the answer's deliveries: the
// series after it are not handed on.
func (session *seriesDecoder) handOn(ctx context.Context) error {
	if session.failed != nil || len(session.pending) == 0 {
		return session.failed
	}
	normalizeSideBySide(session.pending, session.attempt.Spec, session.ref, session.receivedAt)
	for index := range session.pending {
		if session.failed = session.deliver(ctx, &session.pending[index]); session.failed != nil {
			break
		}
	}
	clear(session.pending)
	session.pending = session.pending[:0]
	return session.failed
}

// deliver hands one canonical series on.
func (session *seriesDecoder) deliver(ctx context.Context, job *normalizing) error {
	batch, err := job.batch, job.err
	if errors.Is(err, errOffRequestGrid) {
		session.offGrid = true
		return nil
	}
	if err != nil {
		return err
	}
	session.nullIdentityFields += job.nullFields
	if batch.Dataset.Len() == 0 {
		return nil
	}
	batch.Delivery.Bytes = job.encodedBytes
	if err := session.sink.ConsumeProviderSeries(ctx, batch); err != nil {
		return err
	}
	session.delivery, err = execution.AccumulateSeriesDelivery(session.delivery, batch.Delivery)
	if err == nil && session.named != nil {
		err = session.named.noteDelivered(ctx, job.series, batch.Dataset, session.attempt.Spec.PlanFacts.Normalization.CanonicalValueField)
	}
	return err
}

// settle hands on what an answer that ended early had accepted, as each
// series was handed on as it came before it ended, and keeps the earlier
// error: a series accepted before the one that ended the answer failed
// first.
func (session *seriesDecoder) settle(ctx context.Context, err *error) {
	if len(session.pending) == 0 {
		return
	}
	if failed := session.handOn(ctx); failed != nil {
		*err = failed
	}
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

// finish completes the answer, and leaves the provider's own message, when
// its status carried one, on the attempt for the client to keep with the
// request it answered (statusExcerpt).
func (session *seriesDecoder) finish(ctx context.Context, status *responseStatus, isPartial *bool, resultTableIDs []string) (execution.ProviderCompletion, error) {
	completion, err := session.complete(ctx, status, isPartial, resultTableIDs)
	if excerpt := statusExcerpt(status); err == nil && excerpt != "" {
		for index := range completion.RouteFacts.Attempts {
			completion.RouteFacts.Attempts[index].Request = &execution.RequestFacts{Excerpt: excerpt}
		}
	}
	return completion, err
}

// statusExcerpt is a body status's message as an operator may read it,
// sanitized and bounded as a refusal's body is (providerErrorExcerpt); empty
// for no status or no message.
func statusExcerpt(status *responseStatus) string {
	if status == nil || status.Message == "" {
		return ""
	}
	return providerErrorExcerpt([]byte(status.Message))
}

func (session *seriesDecoder) complete(ctx context.Context, status *responseStatus, isPartial *bool, resultTableIDs []string) (execution.ProviderCompletion, error) {
	if err := session.handOn(ctx); err != nil {
		return execution.ProviderCompletion{}, err
	}
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
