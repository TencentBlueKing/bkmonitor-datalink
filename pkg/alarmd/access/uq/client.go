package uq

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const (
	headerQuerySource = "Bk-Query-Source"
	headerTenant      = "X-Bk-Tenant-Id"
	headerSpace       = "X-Bk-Scope-Space-Uid"
	queryTSPartial    = "QUERY_TS_PARTIAL"
	// spaceTableIDFieldIsNotExists is UQ saying the table or field the query
	// names cannot be routed. It is a statement about the data, not about
	// whether the query ran.
	spaceTableIDFieldIsNotExists = "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"
)

// dataExistenceStatusCodes are the status codes that describe the data rather
// than the health of the query, and only for those may series that arrived
// alongside the code still be used.
//
// The distinction matters because UQ answers an expression, not a table. An
// expression with a fallback - `(1 - (a + b) / c) * 100 or vector(100)`, which
// is how a success-rate strategy says "no failures means 100%" - resolves to a
// series even when none of its sub-queries route anywhere, and UQ reports both:
// the constant series, and the code saying the tables were not found. Both are
// true. Treating the code as the whole answer threw away a series the strategy
// was defined to produce, and two strategies went ~34 hours without a single
// evaluation while Python evaluated them normally every cycle.
//
// The list is deliberately one entry. Everything not on it stays UNAVAILABLE,
// which is the direction that fails visibly, and a new code has to be reviewed
// in rather than default in:
//
//   - SPACE_IS_NOT_EXISTS is also an existence statement, but for a whole
//     space, and it is raised when the space router has no entry - which a
//     router that failed to load also produces. Wrong here silences every
//     strategy in the space.
//   - EXCEEDS_MAXIMUM_LIMIT / EXCEEDS_MAXIMUM_SLIMIT mean data exists and was
//     cut short. That is the opposite of empty.
//   - STORAGE_TIMEOUT / STORAGE_ERROR / QUERY_RAW_ERROR are the query failing.
//   - SPACE_TABLE_ID_FIELD_MISSING_FALLBACK is annotated in UQ as metadata
//     possibly being stale, so it is transient by construction. It is also
//     only ever logged, never set as a status, so it cannot reach here at all -
//     but it is the one that would look most like a member of this list.
//   - TABLE_ID_PROXY_IS_NOT_EXISTS is declared in UQ and never assigned.
//
// What puts the one entry on the list is not the source reading: it is a replay
// of the two strategies' own compiled queries against the deployed UQ, which
// answered with exactly this code beside a usable fallback series. The source
// reading (pkg/unify-query/metadata/const.go and the assignment sites in
// query/structured/space.go, at bkmonitor-datalink master rather than the
// deployed tag) supports only the exclusions above, where being wrong means
// keeping today's behaviour.
var dataExistenceStatusCodes = map[string]struct{}{
	spaceTableIDFieldIsNotExists: {},
}

var (
	ErrResponseBytesExceeded error = &responseLimitError{code: "RESPONSE_BYTES_EXCEEDED"}
	ErrSeriesBytesExceeded   error = &responseLimitError{code: "SERIES_BYTES_EXCEEDED"}
	ErrTotalSeriesExceeded   error = &responseLimitError{code: "TOTAL_SERIES_EXCEEDED"}
	ErrTotalRecordsExceeded  error = &responseLimitError{code: "TOTAL_RECORDS_EXCEEDED"}
)

type Limits struct {
	MaxBodyBytes   int64
	MaxSeriesBytes int64
	MaxSeries      uint64
	MaxRecords     uint64
}

func DefaultLimits() Limits {
	return Limits{MaxBodyBytes: 64 << 20, MaxSeriesBytes: 8 << 20, MaxSeries: 100_000, MaxRecords: 1_000_000}
}

func (limits Limits) validate() error {
	if limits.MaxBodyBytes <= 0 || limits.MaxSeriesBytes <= 0 || limits.MaxSeries == 0 || limits.MaxRecords == 0 || limits.MaxSeriesBytes > limits.MaxBodyBytes {
		return errors.New("alarmd access uq: positive ordered response limits are required")
	}
	return nil
}

type Client struct {
	endpoint    string
	httpClient  *http.Client
	querySource string
	limits      Limits
	now         func() time.Time
	// rangeRetries counts connections that failed before a response began and
	// had to be re-dialed. See doRangeRequest.
	rangeRetries atomic.Uint64
}

func NewClient(endpoint, querySource string, httpClient *http.Client) (*Client, error) {
	return NewClientWithLimits(endpoint, querySource, httpClient, DefaultLimits())
}

func NewClientWithLimits(endpoint, querySource string, httpClient *http.Client, limits Limits) (*Client, error) {
	if endpoint == "" || querySource == "" || httpClient == nil {
		return nil, errors.New("alarmd access uq: endpoint, query source and HTTP client are required")
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), querySource: querySource,
		httpClient: httpClient, limits: limits, now: time.Now}, nil
}

func (client *Client) Execute(ctx context.Context, attempt execution.QueryAttempt, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	if client == nil || sink == nil {
		return execution.ProviderCompletion{}, errors.New("alarmd access uq: initialized client and sink are required")
	}
	if err := attempt.Validate(); err != nil {
		return execution.ProviderCompletion{}, err
	}
	callerCtx := ctx
	deadline := time.UnixMilli(attempt.DeadlineUnixMilli)
	if current, ok := ctx.Deadline(); !ok || deadline.Before(current) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	body, err := buildRequest(attempt.Spec)
	if err != nil {
		return execution.ProviderCompletion{}, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+"/query/ts", bytes.NewReader(encoded))
	if err != nil {
		return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(headerQuerySource, client.querySource)
	request.Header.Set(headerTenant, attempt.Spec.PlanFacts.TenantID)
	request.Header.Set(headerSpace, attempt.Spec.PlanFacts.SpaceScope)
	started := client.now()
	response, err := client.httpClient.Do(request)
	if err != nil {
		if callerCtx.Err() != nil {
			return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: execute request: %w", callerCtx.Err())
		}
		reason := execution.ReasonCode(contract.ReasonQueryUnavailable)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			reason = execution.ReasonCode(contract.ReasonQueryTimeout)
		}
		completion := client.unavailableCompletion(attempt, reason, execution.TransportRouteDetail(classifyTransportFailure(err)))
		completion.Stats.QueryMillis = uint64(client.now().Sub(started).Milliseconds())
		return completion, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// The body is drained and discarded on purpose: UQ error bodies can echo
		// the request (table ids, conditions, dimension values) and must not be
		// parsed for business state or copied into logs. The status code alone
		// is the bounded diagnostic detail.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		completion := client.unavailableCompletion(attempt, execution.ReasonCode(contract.ReasonQueryUnavailable), execution.HTTPStatusRouteDetail(response.StatusCode))
		completion.Stats.QueryMillis = uint64(client.now().Sub(started).Milliseconds())
		return completion, nil
	}
	counted := &countingReader{reader: &boundedReader{reader: response.Body, maximum: client.limits.MaxBodyBytes}}
	completion, err := client.decode(ctx, counted, attempt, sink)
	if err != nil {
		return execution.ProviderCompletion{}, err
	}
	completion.Stats.Bytes = counted.bytes
	completion.Stats.QueryMillis = uint64(client.now().Sub(started).Milliseconds())
	return completion, nil
}

func (client *Client) unavailableCompletion(attempt execution.QueryAttempt, reason execution.ReasonCode, detail string) execution.ProviderCompletion {
	return execution.ProviderCompletion{
		Ref:           providerResultRef(attempt),
		PhysicalQuery: attempt.Spec.Digest,
		Completeness:  execution.CompletenessUnavailable,
		DataState:     execution.DataStateUnknown,
		RouteFacts: execution.ProviderRouteFacts{
			ProviderRouteRef: attempt.Spec.PlanFacts.ProviderRouteRef,
			Attempts: []execution.RouteAttemptFact{{
				AttemptNo: attempt.AttemptNo, Endpoint: client.endpoint,
				Result: execution.RouteAttemptFailed, ReasonCode: reason, Detail: detail,
			}},
		},
	}
}

// classifyTransportFailure maps an http.Client.Do error onto the bounded
// transport failure enum. It inspects error types only and never copies the
// error text, which may embed the endpoint URL.
func classifyTransportFailure(err error) string {
	if err == nil {
		return execution.TransportFailureOther
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return execution.TransportFailureTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return execution.TransportFailureDNS
	}
	if isTLSFailure(err) {
		return execution.TransportFailureTLS
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return execution.TransportFailureConnectionRefused
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return execution.TransportFailureConnectionReset
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return execution.TransportFailureEOF
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return execution.TransportFailureTimeout
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return execution.TransportFailureConnectionRefused
	}
	return execution.TransportFailureOther
}

func isTLSFailure(err error) bool {
	var recordHeader tls.RecordHeaderError
	var alert tls.AlertError
	var certificate *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificateInvalid x509.CertificateInvalidError
	return errors.As(err, &recordHeader) || errors.As(err, &alert) || errors.As(err, &certificate) ||
		errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &certificateInvalid)
}

func providerResultRef(attempt execution.QueryAttempt) execution.ProviderResultRef {
	return execution.ProviderResultRef(string(attempt.Spec.Digest) + ":" + strconv.FormatUint(uint64(attempt.AttemptNo), 10))
}

type countingReader struct {
	reader io.Reader
	bytes  uint64
}

type boundedReader struct {
	reader  io.Reader
	maximum int64
	read    int64
}

func (reader *boundedReader) Read(buffer []byte) (int, error) {
	remaining := reader.maximum - reader.read
	if remaining < 0 {
		return 0, ErrResponseBytesExceeded
	}
	if int64(len(buffer)) > remaining+1 {
		buffer = buffer[:remaining+1]
	}
	count, err := reader.reader.Read(buffer)
	if reader.read+int64(count) > reader.maximum {
		reader.read += int64(count)
		return 0, ErrResponseBytesExceeded
	}
	reader.read += int64(count)
	return count, err
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	reader.bytes += uint64(count)
	return count, err
}

func buildRequest(spec execution.PhysicalQuerySpec) (request, error) {
	if err := spec.Validate(); err != nil {
		return request{}, err
	}
	queries := make([]queryClause, 0, len(spec.PlanFacts.QueryList))
	for _, source := range spec.PlanFacts.QueryList {
		functions, err := mapFunctions(source.Functions)
		if err != nil {
			return request{}, err
		}
		timeAggregation, err := mapTimeAggregation(source.TimeAggregation)
		if err != nil {
			return request{}, err
		}
		fields := make([]conditionField, 0, len(source.Conditions.Fields))
		for _, field := range source.Conditions.Fields {
			wildcard, err := parseQueryBool("is_wildcard", field.Wildcard)
			if err != nil {
				return request{}, err
			}
			prefix, err := parseQueryBool("is_prefix", field.Prefix)
			if err != nil {
				return request{}, err
			}
			suffix, err := parseQueryBool("is_suffix", field.Suffix)
			if err != nil {
				return request{}, err
			}
			values := make([]string, 0, len(field.Values))
			for _, value := range field.Values {
				text, scalarErr := scalarText(value)
				if scalarErr != nil {
					return request{}, scalarErr
				}
				values = append(values, text)
			}
			fields = append(fields, conditionField{Field: field.Field, Operator: field.Operator, Values: values,
				Wildcard: wildcard, Prefix: prefix, Suffix: suffix})
		}
		offsetForward, err := parseQueryBool("offset_forward", source.OffsetForward)
		if err != nil {
			return request{}, err
		}
		queries = append(queries, queryClause{
			DataSource: source.DataSource, TableID: source.TableID, FieldName: source.FieldName,
			Driver: source.Driver, TimeField: source.TimeField, IsRegexp: source.IsRegexp,
			ReferenceName: source.ReferenceName, Functions: functions, TimeAggregation: timeAggregation,
			Dimensions: append([]string(nil), source.Dimensions...),
			Conditions: conditions{Fields: fields, Connectors: append([]string(nil), source.Conditions.Connectors...)},
			Offset:     source.Offset, OffsetForward: offsetForward,
			KeepColumns: append([]string(nil), source.KeepColumns...), QueryString: source.QueryString,
		})
	}
	return request{QueryList: queries, MetricMerge: spec.PlanFacts.MetricMerge,
		StartTime: strconv.FormatInt(spec.ProviderRange.Start, 10), EndTime: strconv.FormatInt(spec.ProviderRange.End, 10),
		Step: durationString(spec.PlanFacts.StepMillis), SpaceUID: spec.PlanFacts.SpaceScope,
		DownSampleRange: string(spec.PlanFacts.DownSampleRange), Timezone: spec.PlanFacts.Timezone,
		NotTimeAlign: spec.PlanFacts.NotTimeAlign}, nil
}

func parseQueryBool(field, value string) (bool, error) {
	switch value {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("alarmd access uq: %s must be true or false", field)
	}
}

func mapFunctions(source []execution.QueryFunction) ([]queryFunction, error) {
	result := make([]queryFunction, 0, len(source))
	for _, item := range source {
		mapped, err := mapFunction(item)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapTimeAggregation(source execution.QueryFunction) (timeAggregation, error) {
	if source.Method == "" {
		return timeAggregation{}, nil
	}
	arguments := make([]any, 0, len(source.Arguments))
	for _, argument := range source.Arguments {
		value, err := scalarValue(argument)
		if err != nil {
			return timeAggregation{}, err
		}
		arguments = append(arguments, value)
	}
	position := source.Position
	return timeAggregation{Function: source.Method, Window: source.Window, Position: &position,
		VArgsList: arguments, Subquery: source.Subquery, Step: source.Step}, nil
}

func mapFunction(source execution.QueryFunction) (queryFunction, error) {
	arguments := make([]any, 0, len(source.Arguments))
	for _, argument := range source.Arguments {
		value, err := scalarValue(argument)
		if err != nil {
			return queryFunction{}, err
		}
		arguments = append(arguments, value)
	}
	return queryFunction{Method: source.Method, Field: source.Field, Without: source.Without,
		Dimensions: append([]string(nil), source.Dimensions...), Position: source.Position,
		VArgsList: arguments, Window: source.Window, Subquery: source.Subquery, Step: source.Step}, nil
}

func scalarValue(value execution.QueryScalar) (any, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	switch value.Kind {
	case execution.QueryScalarString:
		return value.StringValue, nil
	case execution.QueryScalarNumber:
		return json.Number(value.NumberValue), nil
	case execution.QueryScalarBoolean:
		return value.BoolValue, nil
	default:
		return nil, errors.New("alarmd access uq: unsupported query scalar")
	}
}

func scalarText(value execution.QueryScalar) (string, error) {
	mapped, err := scalarValue(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(mapped), nil
}

func durationString(milliseconds int64) string {
	if milliseconds%1000 == 0 {
		return strconv.FormatInt(milliseconds/1000, 10) + "s"
	}
	return strconv.FormatInt(milliseconds, 10) + "ms"
}

func (client *Client) decode(ctx context.Context, reader io.Reader, attempt execution.QueryAttempt, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	decodeStarted := client.now()
	opening, err := decoder.Token()
	if err != nil {
		return execution.ProviderCompletion{}, err
	}
	if opening != json.Delim('{') {
		return execution.ProviderCompletion{}, errors.New("alarmd access uq: response must be an object")
	}
	ref := providerResultRef(attempt)
	var delivery execution.SeriesDelivery
	var status *responseStatus
	var isPartial *bool
	var resultTableIDs []string
	var totalSeries, totalRecords, nullIdentityFields uint64
	receivedAt := client.now().Unix()
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return execution.ProviderCompletion{}, err
		}
		keyToken, err := decoder.Token()
		if err != nil {
			return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: decode field: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return execution.ProviderCompletion{}, errors.New("alarmd access uq: response field name is invalid")
		}
		switch key {
		case "series":
			start, err := decoder.Token()
			if err != nil || start != json.Delim('[') {
				return execution.ProviderCompletion{}, errors.New("alarmd access uq: series must be an array")
			}
			for decoder.More() {
				var raw json.RawMessage
				if err := decoder.Decode(&raw); err != nil {
					return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: decode series payload: %w", err)
				}
				if int64(len(raw)) > client.limits.MaxSeriesBytes {
					return execution.ProviderCompletion{}, ErrSeriesBytesExceeded
				}
				var series responseSeries
				if err := json.Unmarshal(raw, &series); err != nil {
					return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: decode series: %w", err)
				}
				totalSeries++
				if totalSeries > client.limits.MaxSeries {
					return execution.ProviderCompletion{}, ErrTotalSeriesExceeded
				}
				if uint64(len(series.Values)) > client.limits.MaxRecords-totalRecords {
					return execution.ProviderCompletion{}, ErrTotalRecordsExceeded
				}
				totalRecords += uint64(len(series.Values))
				batch, nullFields, err := normalizeSeries(attempt.Spec, ref, series, receivedAt)
				if err != nil {
					return execution.ProviderCompletion{}, err
				}
				nullIdentityFields += nullFields
				batch.Delivery.Bytes = uint64(len(raw))
				if batch.Dataset.Len() == 0 {
					continue
				}
				if err := sink.ConsumeProviderSeries(ctx, batch); err != nil {
					return execution.ProviderCompletion{}, err
				}
				delivery, err = execution.AccumulateSeriesDelivery(delivery, batch.Delivery)
				if err != nil {
					return execution.ProviderCompletion{}, err
				}
			}
			if _, err := decoder.Token(); err != nil {
				return execution.ProviderCompletion{}, err
			}
		case "status":
			if err := decoder.Decode(&status); err != nil {
				return execution.ProviderCompletion{}, err
			}
		case "is_partial":
			var value bool
			if err := decoder.Decode(&value); err != nil {
				return execution.ProviderCompletion{}, err
			}
			isPartial = &value
		case "result_table_id":
			if err := decoder.Decode(&resultTableIDs); err != nil {
				return execution.ProviderCompletion{}, err
			}
		default:
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return execution.ProviderCompletion{}, err
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return execution.ProviderCompletion{}, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: decode trailing payload: %w", err)
		}
		return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: unexpected trailing token %v", token)
	}
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
			unavailable := client.responseContractUnavailable(attempt, execution.ResponseStatusRouteDetail(status.Code),
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
	if isPartial == nil {
		return client.responseContractUnavailable(attempt, execution.ResponseRouteDetail(execution.ResponseFailureIsPartialMissing),
			dataState, delivery, resultTableIDs, stats), nil
	}
	completeness := execution.CompletenessFull
	if *isPartial || status != nil && status.Code == queryTSPartial {
		completeness = execution.CompletenessPartial
	}
	return execution.ProviderCompletion{Ref: ref, PhysicalQuery: attempt.Spec.Digest,
		Completeness: completeness, DataState: dataState, Delivery: delivery,
		RouteFacts: execution.ProviderRouteFacts{ProviderRouteRef: attempt.Spec.PlanFacts.ProviderRouteRef,
			ResultTableIDs: append([]string(nil), resultTableIDs...), Status: passthroughStatus,
			Attempts: []execution.RouteAttemptFact{{AttemptNo: attempt.AttemptNo,
				Endpoint: client.endpoint, Result: execution.RouteAttemptSucceeded, Detail: passthroughDetail}}},
		Stats: stats}, nil
}

// usableDespiteStatus reports whether a response carrying code should still be
// read for the series it delivered.
//
// Both halves are required. Without a delivered series there is nothing to
// keep and the answer really is "this does not exist", which UNAVAILABLE
// already states correctly - so a query that returned nothing behaves exactly
// as before. Without the code check, "some sub-queries failed but one
// succeeded" would be read the same way as "the expression answered by
// itself", and those need opposite handling.
//
// What keeps those two apart is an ordering in UQ, not the codes being
// mutually exclusive: SetStatus holds one slot and the last writer wins
// (metadata/status.go), the routing statuses are written while the query is
// being built, and the multi-route partial status is written after the fan-out
// finishes (tsdb/prometheus/querier.go - it even keeps the earlier message and
// replaces only the code). So a response that really did lose a route reports
// QUERY_TS_PARTIAL as its final code and never reaches this list, while an
// existence code surviving as the final code means no route reported a partial
// failure and the series came from the expression itself.
//
// is_partial is a second, independent guard: it comes back from the storage
// instance rather than from the status slot, and a true value still completes
// the query as PARTIAL further down whatever the code says.
func usableDespiteStatus(code string, delivery execution.SeriesDelivery) bool {
	if delivery.Series == 0 {
		return false
	}
	_, known := dataExistenceStatusCodes[code]
	return known
}

// responseContractUnavailable completes a decoded 200 response that violated
// the wire contract (missing is_partial) or reported a deterministic backend
// status as UNAVAILABLE with a bounded detail. DataState and Delivery describe
// series already streamed to the sink so the completion conserves them.
func (client *Client) responseContractUnavailable(
	attempt execution.QueryAttempt,
	detail string,
	dataState execution.DataState,
	delivery execution.SeriesDelivery,
	resultTableIDs []string,
	stats execution.ProviderStats,
) execution.ProviderCompletion {
	completion := client.unavailableCompletion(attempt, execution.ReasonCode(contract.ReasonQueryUnavailable), detail)
	completion.DataState = dataState
	completion.Delivery = delivery
	completion.RouteFacts.ResultTableIDs = append([]string(nil), resultTableIDs...)
	completion.Stats = stats
	return completion
}

// nullDimension is the JSON value bound to a declared identity dimension that
// the provider series does not carry.
var nullDimension = json.RawMessage("null")

// normalizeSeries converts one UQ series into an immutable canonical batch. It
// also returns how many declared identity fields were absent from the series
// group keys and were bound to null.
//
// Python (alarm_backends/service/access/data/records.py, dimension extraction
// in both the module-level and the record-level helper) does
// dimensions[field] = raw_data.get(field): an absent dimension becomes None,
// the record continues and the dimensions md5 includes that None. Mirroring
// it here means a series that lacks the field and a series that carries an
// explicit null for it have the same identity digest, exactly as in Python
// where None is the value in both cases. Series that carry the field keep
// their previous identity unchanged.
func normalizeSeries(spec execution.PhysicalQuerySpec, ref execution.ProviderResultRef, source responseSeries, receivedAt int64) (execution.ProviderSeriesBatch, uint64, error) {
	if len(source.Columns) == 0 || len(source.Columns) != len(source.Types) || len(source.GroupKeys) != len(source.GroupValues) {
		return execution.ProviderSeriesBatch{}, 0, errors.New("alarmd access uq: invalid series schema")
	}
	dimensions := make(map[string]json.RawMessage, len(source.GroupKeys))
	for index, key := range source.GroupKeys {
		key = stripTableSuffix(key)
		encoded, _ := json.Marshal(source.GroupValues[index])
		dimensions[key] = encoded
	}
	var nullIdentityFields uint64
	identityFields := make([]contract.DimensionFieldV2, 0, len(spec.PlanFacts.Normalization.DatasetContract.IdentityFields))
	for _, name := range spec.PlanFacts.Normalization.DatasetContract.IdentityFields {
		value, ok := dimensions[name]
		if !ok {
			value = nullDimension
			dimensions[name] = value
			nullIdentityFields++
		}
		identityFields = append(identityFields, contract.DimensionFieldV2{Name: name, Value: value})
	}
	// Canonical identity requires deterministic field order, independent of UQ column order.
	for left := 0; left < len(identityFields); left++ {
		for right := left + 1; right < len(identityFields); right++ {
			if identityFields[right].Name < identityFields[left].Name {
				identityFields[left], identityFields[right] = identityFields[right], identityFields[left]
			}
		}
	}
	dimensionDigest, err := contract.DeriveDimensionIdentityDigestV2(spec.PlanFacts.TenantID, spec.PlanFacts.BusinessID, identityFields)
	if err != nil {
		return execution.ProviderSeriesBatch{}, 0, err
	}
	records := make([]contract.CanonicalRecordV2, 0, len(source.Values))
	lastTime := int64(-1)
	for _, row := range source.Values {
		if len(row) != len(source.Columns) {
			return execution.ProviderSeriesBatch{}, 0, errors.New("alarmd access uq: row width differs from columns")
		}
		timestamp, value, err := rowFacts(source.Columns, row, spec.PlanFacts.QueryList)
		if err != nil {
			return execution.ProviderSeriesBatch{}, 0, err
		}
		sourceTime, err := spec.PlanFacts.Normalization.NormalizeSourceTime(timestamp)
		if err != nil {
			return execution.ProviderSeriesBatch{}, 0, err
		}
		if sourceTime <= lastTime {
			return execution.ProviderSeriesBatch{}, 0, errors.New("alarmd access uq: source order contract violation")
		}
		lastTime = sourceTime
		if sourceTime < spec.AcceptedRange.Start || sourceTime >= spec.AcceptedRange.End {
			continue
		}
		recordID, err := contract.DeriveRecordIDV2(dimensionDigest, sourceTime)
		if err != nil {
			return execution.ProviderSeriesBatch{}, 0, err
		}
		records = append(records, contract.CanonicalRecordV2{RecordID: recordID, SourceTime: sourceTime,
			BusinessID:        spec.PlanFacts.BusinessID,
			DimensionIdentity: contract.DimensionIdentityV2{Fields: identityFields, Digest: dimensionDigest},
			Values:            map[string]json.RawMessage{spec.PlanFacts.Normalization.CanonicalValueField: value},
			Dimensions:        dimensions, ReceivedTime: receivedAt})
	}
	dataset := execution.NewDataset(records)
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-provider-series-delivery-v1", records)
	if err != nil {
		return execution.ProviderSeriesBatch{}, 0, err
	}
	return execution.ProviderSeriesBatch{PhysicalQuery: spec.Digest, CompletionRef: ref, Dataset: dataset,
		Delivery: execution.SeriesDelivery{PhysicalQuery: spec.Digest, QueryRevision: spec.PlanFacts.QueryRevision,
			Series: 1, Records: uint64(len(records)), Digest: digest}}, nullIdentityFields, nil
}

func rowFacts(columns []string, row []json.RawMessage, queries []execution.QueryClause) (int64, json.RawMessage, error) {
	timeIndex, valueIndex := -1, -1
	for index, name := range columns {
		switch name {
		case "_time", "_time_":
			timeIndex = index
		case "_result", "_result_", "_value", "_value_":
			if valueIndex < 0 {
				valueIndex = index
			}
		}
	}
	if valueIndex < 0 {
		for _, query := range queries {
			for index, name := range columns {
				if name == query.ReferenceName {
					valueIndex = index
					break
				}
			}
			if valueIndex >= 0 {
				break
			}
		}
	}
	if timeIndex < 0 || valueIndex < 0 {
		return 0, nil, errors.New("alarmd access uq: time or result column is missing")
	}
	var timestamp json.Number
	if err := json.Unmarshal(row[timeIndex], &timestamp); err != nil {
		return 0, nil, errors.New("alarmd access uq: invalid source time")
	}
	timeValue, err := timestamp.Int64()
	if err != nil {
		return 0, nil, errors.New("alarmd access uq: non-integer source time")
	}
	canonicalValue, err := contract.CanonicalJSONV2(row[valueIndex])
	if err != nil {
		return 0, nil, err
	}
	return timeValue, json.RawMessage(canonicalValue), nil
}

func stripTableSuffix(value string) string {
	index := strings.LastIndex(value, "_table")
	if index < 0 || index+6 == len(value) {
		return value
	}
	for _, char := range value[index+6:] {
		if char < '0' || char > '9' {
			return value
		}
	}
	return value[:index]
}
