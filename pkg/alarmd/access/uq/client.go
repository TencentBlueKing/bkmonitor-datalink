package uq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const (
	headerQuerySource = "Bk-Query-Source"
	headerTenant      = "X-Bk-Tenant-Id"
	headerSpace       = "X-Bk-Scope-Space-Uid"
)

var (
	ErrResponseBytesExceeded = errors.New("alarmd access uq: RESPONSE_BYTES_EXCEEDED")
	ErrSeriesBytesExceeded   = errors.New("alarmd access uq: SERIES_BYTES_EXCEEDED")
	ErrTotalSeriesExceeded   = errors.New("alarmd access uq: TOTAL_SERIES_EXCEEDED")
	ErrTotalRecordsExceeded  = errors.New("alarmd access uq: TOTAL_RECORDS_EXCEEDED")
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
		completion := client.unavailableCompletion(attempt, reason)
		completion.Stats.QueryMillis = uint64(client.now().Sub(started).Milliseconds())
		return completion, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		completion := client.unavailableCompletion(attempt, execution.ReasonCode(contract.ReasonQueryUnavailable))
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

func (client *Client) unavailableCompletion(attempt execution.QueryAttempt, reason execution.ReasonCode) execution.ProviderCompletion {
	return execution.ProviderCompletion{
		Ref:           providerResultRef(attempt),
		PhysicalQuery: attempt.Spec.Digest,
		Completeness:  execution.CompletenessUnavailable,
		DataState:     execution.DataStateUnknown,
		RouteFacts: execution.ProviderRouteFacts{
			ProviderRouteRef: attempt.Spec.PlanFacts.ProviderRouteRef,
			Attempts: []execution.RouteAttemptFact{{
				AttemptNo: attempt.AttemptNo, Endpoint: client.endpoint,
				Result: execution.RouteAttemptFailed, ReasonCode: reason,
			}},
		},
	}
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
	var totalSeries, totalRecords uint64
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
				batch, err := normalizeSeries(attempt.Spec, ref, series, receivedAt)
				if err != nil {
					return execution.ProviderCompletion{}, err
				}
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
	if status != nil && status.Code != "" {
		return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: query status %s", status.Code)
	}
	dataState := execution.DataStateEmpty
	if delivery.Records > 0 {
		dataState = execution.DataStateData
	}
	if isPartial == nil {
		completion := client.unavailableCompletion(attempt, execution.ReasonCode(contract.ReasonQueryUnavailable))
		completion.DataState = dataState
		completion.Delivery = delivery
		completion.RouteFacts.ResultTableIDs = append([]string(nil), resultTableIDs...)
		completion.Stats = execution.ProviderStats{Series: delivery.Series, Records: delivery.Records,
			DecodeMillis: uint64(client.now().Sub(decodeStarted).Milliseconds())}
		return completion, nil
	}
	completeness := execution.CompletenessFull
	if *isPartial {
		completeness = execution.CompletenessPartial
	}
	return execution.ProviderCompletion{Ref: ref, PhysicalQuery: attempt.Spec.Digest,
		Completeness: completeness, DataState: dataState, Delivery: delivery,
		RouteFacts: execution.ProviderRouteFacts{ProviderRouteRef: attempt.Spec.PlanFacts.ProviderRouteRef,
			ResultTableIDs: append([]string(nil), resultTableIDs...), Attempts: []execution.RouteAttemptFact{{AttemptNo: attempt.AttemptNo, Endpoint: client.endpoint, Result: execution.RouteAttemptSucceeded}}},
		Stats: execution.ProviderStats{Series: delivery.Series, Records: delivery.Records, DecodeMillis: uint64(client.now().Sub(decodeStarted).Milliseconds())}}, nil
}

func normalizeSeries(spec execution.PhysicalQuerySpec, ref execution.ProviderResultRef, source responseSeries, receivedAt int64) (execution.ProviderSeriesBatch, error) {
	if len(source.Columns) == 0 || len(source.Columns) != len(source.Types) || len(source.GroupKeys) != len(source.GroupValues) {
		return execution.ProviderSeriesBatch{}, errors.New("alarmd access uq: invalid series schema")
	}
	dimensions := make(map[string]json.RawMessage, len(source.GroupKeys))
	for index, key := range source.GroupKeys {
		key = stripTableSuffix(key)
		encoded, _ := json.Marshal(source.GroupValues[index])
		dimensions[key] = encoded
	}
	identityFields := make([]contract.DimensionFieldV2, 0, len(spec.PlanFacts.Normalization.DatasetContract.IdentityFields))
	for _, name := range spec.PlanFacts.Normalization.DatasetContract.IdentityFields {
		value, ok := dimensions[name]
		if !ok {
			return execution.ProviderSeriesBatch{}, fmt.Errorf("alarmd access uq: identity field %s is missing", name)
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
		return execution.ProviderSeriesBatch{}, err
	}
	records := make([]contract.CanonicalRecordV2, 0, len(source.Values))
	lastTime := int64(-1)
	for _, row := range source.Values {
		if len(row) != len(source.Columns) {
			return execution.ProviderSeriesBatch{}, errors.New("alarmd access uq: row width differs from columns")
		}
		timestamp, value, err := rowFacts(source.Columns, row, spec.PlanFacts.QueryList)
		if err != nil {
			return execution.ProviderSeriesBatch{}, err
		}
		sourceTime, err := spec.PlanFacts.Normalization.NormalizeSourceTime(timestamp)
		if err != nil {
			return execution.ProviderSeriesBatch{}, err
		}
		if sourceTime <= lastTime {
			return execution.ProviderSeriesBatch{}, errors.New("alarmd access uq: source order contract violation")
		}
		lastTime = sourceTime
		if sourceTime < spec.AcceptedRange.Start || sourceTime >= spec.AcceptedRange.End {
			continue
		}
		recordID, err := contract.DeriveRecordIDV2(dimensionDigest, sourceTime)
		if err != nil {
			return execution.ProviderSeriesBatch{}, err
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
		return execution.ProviderSeriesBatch{}, err
	}
	return execution.ProviderSeriesBatch{PhysicalQuery: spec.Digest, CompletionRef: ref, Dataset: dataset,
		Delivery: execution.SeriesDelivery{PhysicalQuery: spec.Digest, QueryRevision: spec.PlanFacts.QueryRevision,
			Series: 1, Records: uint64(len(records)), Digest: digest}}, nil
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
