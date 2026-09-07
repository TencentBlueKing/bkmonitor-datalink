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
	"sort"
	"strconv"
	"strings"
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
)

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
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), querySource: querySource, httpClient: httpClient, limits: limits, now: time.Now}, nil
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
	// When the provider series grain is finer than the dataset identity, series
	// sharing one identity are folded into one batch after the series array
	// closes; every other query keeps delivering one series at a time.
	fold := foldsProviderSeries(attempt.Spec)
	var folder seriesFolder
	deliver := func(normalized normalizedSeries) error {
		batch, err := seriesBatch(attempt.Spec, ref, normalized.records, normalized.bytes)
		if err != nil {
			return err
		}
		if err := sink.ConsumeProviderSeries(ctx, batch); err != nil {
			return err
		}
		delivery, err = execution.AccumulateSeriesDelivery(delivery, batch.Delivery)
		return err
	}
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
				normalized, err := normalizeSeriesRecords(attempt.Spec, series, receivedAt)
				if err != nil {
					return execution.ProviderCompletion{}, err
				}
				nullIdentityFields += normalized.nullIdentityFields
				normalized.bytes = uint64(len(raw))
				if len(normalized.records) == 0 {
					continue
				}
				if fold {
					folder.add(normalized)
					continue
				}
				if err := deliver(normalized); err != nil {
					return execution.ProviderCompletion{}, err
				}
			}
			if _, err := decoder.Token(); err != nil {
				return execution.ProviderCompletion{}, err
			}
			for _, folded := range folder.folded() {
				if err := ctx.Err(); err != nil {
					return execution.ProviderCompletion{}, err
				}
				if err := deliver(folded); err != nil {
					return execution.ProviderCompletion{}, err
				}
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
	if status != nil && status.Code != "" && status.Code != queryTSPartial {
		// A non-partial status code is a deterministic answer for this table and
		// field (for example SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS). Completing it as
		// UNAVAILABLE lets the Slot finish with a Plan gap instead of failing and
		// re-querying UQ on every attempt until the Slot ages out. UQ writes the
		// series array before status, so series decoded before the status token
		// have already reached the sink; the completion keeps their DataState and
		// Delivery only so that delivery conservation holds. The consumer never
		// receives them: every binding of an UNAVAILABLE completion is UNKNOWN.
		return client.responseContractUnavailable(attempt, execution.ResponseStatusRouteDetail(status.Code),
			dataState, delivery, resultTableIDs, stats), nil
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
			ResultTableIDs: append([]string(nil), resultTableIDs...), Attempts: []execution.RouteAttemptFact{{AttemptNo: attempt.AttemptNo, Endpoint: client.endpoint, Result: execution.RouteAttemptSucceeded}}},
		Stats: stats}, nil
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
	normalized, err := normalizeSeriesRecords(spec, source, receivedAt)
	if err != nil {
		return execution.ProviderSeriesBatch{}, 0, err
	}
	batch, err := seriesBatch(spec, ref, normalized.records, 0)
	if err != nil {
		return execution.ProviderSeriesBatch{}, 0, err
	}
	return batch, normalized.nullIdentityFields, nil
}

// normalizedSeries is one UQ series after normalization: its canonical
// records in source order, the SeriesIdentityDigest they share, the number of
// declared identity fields bound to null and the raw payload bytes.
type normalizedSeries struct {
	identity           string
	records            []contract.CanonicalRecordV2
	nullIdentityFields uint64
	bytes              uint64
}

// seriesBatch builds the immutable single-series batch the consumer contract
// requires from canonical records that share one identity and have strictly
// increasing source times.
func seriesBatch(spec execution.PhysicalQuerySpec, ref execution.ProviderResultRef, records []contract.CanonicalRecordV2, bytes uint64) (execution.ProviderSeriesBatch, error) {
	dataset := execution.NewDataset(records)
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-provider-series-delivery-v1", records)
	if err != nil {
		return execution.ProviderSeriesBatch{}, err
	}
	return execution.ProviderSeriesBatch{PhysicalQuery: spec.Digest, CompletionRef: ref, Dataset: dataset,
		Delivery: execution.SeriesDelivery{PhysicalQuery: spec.Digest, QueryRevision: spec.PlanFacts.QueryRevision,
			Series: 1, Records: uint64(len(records)), Bytes: bytes, Digest: digest}}, nil
}

// foldsProviderSeries reports whether the provider series grain is finer than
// the dataset identity: a group-by dimension of the query is not an identity
// field, so several UQ series can share one SeriesIdentityDigest. The ProcPort
// plan is the production case: the query groups by the five dynamic port
// dimensions (bind_ip, listen, nonlisten, not_accurate_listen, protocol) that
// the identity contract excludes, exactly as Python drops them from the record
// id, so one process with two port rows arrives as two UQ series that the
// consumer must receive as one series. Delivering both would violate the
// one-batch-per-series contract (duplicate streamed named input).
func foldsProviderSeries(spec execution.PhysicalQuerySpec) bool {
	identity := make(map[string]struct{}, len(spec.PlanFacts.Normalization.DatasetContract.IdentityFields))
	for _, name := range spec.PlanFacts.Normalization.DatasetContract.IdentityFields {
		identity[name] = struct{}{}
	}
	for _, query := range spec.PlanFacts.QueryList {
		for _, dimension := range query.Dimensions {
			if _, ok := identity[stripTableSuffix(dimension)]; !ok {
				return true
			}
		}
	}
	return false
}

// seriesFolder groups normalized series by SeriesIdentityDigest in first
// appearance order and folds each group into one series. Records that share a
// source time collapse to one record, the record of the later series in
// response order: both carry the same record id (identity digest plus source
// time), which is the key Python de-duplicates records by, and the consumer
// contract admits one record per id.
type seriesFolder struct {
	order  []string
	groups map[string]*normalizedSeries
}

func (folder *seriesFolder) add(series normalizedSeries) {
	if folder.groups == nil {
		folder.groups = make(map[string]*normalizedSeries)
	}
	if group, found := folder.groups[series.identity]; found {
		group.records = foldRecords(group.records, series.records)
		group.bytes += series.bytes
		return
	}
	copied := series
	folder.groups[series.identity] = &copied
	folder.order = append(folder.order, series.identity)
}

func (folder *seriesFolder) folded() []normalizedSeries {
	result := make([]normalizedSeries, 0, len(folder.order))
	for _, identity := range folder.order {
		result = append(result, *folder.groups[identity])
	}
	return result
}

// foldRecords merges next into current: one record per source time, the
// record from next winning, ordered by source time.
func foldRecords(current, next []contract.CanonicalRecordV2) []contract.CanonicalRecordV2 {
	byTime := make(map[int64]int, len(current)+len(next))
	for index, record := range current {
		byTime[record.SourceTime] = index
	}
	for _, record := range next {
		if index, found := byTime[record.SourceTime]; found {
			current[index] = record
			continue
		}
		byTime[record.SourceTime] = len(current)
		current = append(current, record)
	}
	sort.SliceStable(current, func(left, right int) bool { return current[left].SourceTime < current[right].SourceTime })
	return current
}

func normalizeSeriesRecords(spec execution.PhysicalQuerySpec, source responseSeries, receivedAt int64) (normalizedSeries, error) {
	if len(source.Columns) == 0 || len(source.Columns) != len(source.Types) || len(source.GroupKeys) != len(source.GroupValues) {
		return normalizedSeries{}, errors.New("alarmd access uq: invalid series schema")
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
		return normalizedSeries{}, err
	}
	records := make([]contract.CanonicalRecordV2, 0, len(source.Values))
	lastTime := int64(-1)
	for _, row := range source.Values {
		if len(row) != len(source.Columns) {
			return normalizedSeries{}, errors.New("alarmd access uq: row width differs from columns")
		}
		timestamp, value, err := rowFacts(source.Columns, row, spec.PlanFacts.QueryList)
		if err != nil {
			return normalizedSeries{}, err
		}
		sourceTime, err := spec.PlanFacts.Normalization.NormalizeSourceTime(timestamp)
		if err != nil {
			return normalizedSeries{}, err
		}
		if sourceTime <= lastTime {
			return normalizedSeries{}, errors.New("alarmd access uq: source order contract violation")
		}
		lastTime = sourceTime
		if sourceTime < spec.AcceptedRange.Start || sourceTime >= spec.AcceptedRange.End {
			continue
		}
		recordID, err := contract.DeriveRecordIDV2(dimensionDigest, sourceTime)
		if err != nil {
			return normalizedSeries{}, err
		}
		records = append(records, contract.CanonicalRecordV2{RecordID: recordID, SourceTime: sourceTime,
			BusinessID:        spec.PlanFacts.BusinessID,
			DimensionIdentity: contract.DimensionIdentityV2{Fields: identityFields, Digest: dimensionDigest},
			Values:            map[string]json.RawMessage{spec.PlanFacts.Normalization.CanonicalValueField: value},
			Dimensions:        dimensions, ReceivedTime: receivedAt})
	}
	return normalizedSeries{identity: dimensionDigest, records: records, nullIdentityFields: nullIdentityFields}, nil
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
