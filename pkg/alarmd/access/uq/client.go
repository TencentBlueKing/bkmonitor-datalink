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
	queryTSPartial    = "QUERY_TS_PARTIAL"
	// spaceTableIDFieldIsNotExists is UQ saying the table or field the query
	// names cannot be routed. It is a statement about the data, not about
	// whether the query ran.
	spaceTableIDFieldIsNotExists = "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"
	// headerSkipSpace asks the provider to route without a space. The
	// provider reads only whether it is non-empty.
	headerSkipSpace = "X-Bk-Scope-Skip-Space"
	skipSpaceValue  = "alarmd"
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
	ErrResponseBytesExceeded error = &responseLimitError{code: "RESPONSE_BYTES_EXCEEDED", class: execution.ResponseFailureLimitResponseBytes}
	ErrSeriesBytesExceeded   error = &responseLimitError{code: "SERIES_BYTES_EXCEEDED", class: execution.ResponseFailureLimitSeriesBytes}
	ErrTotalSeriesExceeded   error = &responseLimitError{code: "TOTAL_SERIES_EXCEEDED", class: execution.ResponseFailureLimitTotalSeries}
	ErrTotalRecordsExceeded  error = &responseLimitError{code: "TOTAL_RECORDS_EXCEEDED", class: execution.ResponseFailureLimitTotalRecords}
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

// markReplayable lets the transport send a query again, once, when the
// pooled connection it picked turns out to have been closed by the server
// before any response began.
//
// unify-query sets no IdleTimeout, so Go closes its idle connections at the
// 3s ReadTimeout, while this client keeps them for 90s; between the server's
// close and the client noticing it, a request can pick the dead connection
// and fail with a bare EOF. Go re-sends such a request by itself only when it
// counts it as replayable, and a POST is not one unless it carries an
// Idempotency-Key or X-Idempotency-Key header (net/http, Request.isReplayable).
// A header with a nil value is never written to the wire, so the mark costs
// nothing and tells the server nothing; the transport re-sends only on a
// reused connection, and only for a failure before any response, so a fresh
// connection refused or a response that arrived is never repeated. A query is
// a read, and sending it twice is safe.
func markReplayable(request *http.Request) {
	request.Header["X-Idempotency-Key"] = nil
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
	return client.execute(callerCtx, ctx, queryIdentity{Spec: attempt.Spec, AttemptNo: attempt.AttemptNo,
		// Ordinary Slot reads negotiate the shared format by default. The
		// same response may still be legacy JSON; other entry points do not
		// negotiate, nor do PromQL and polling Execute queries.
		SharedSchema: attempt.Spec.PlanFacts.PromQL == nil &&
			attempt.Spec.PlanFacts.Normalization.Version != "uq-polling-normalization-v1",
		Budget: queryBudget{StartUnixMilli: attempt.BudgetStartUnixMilli, ReadyAtUnixMilli: attempt.ReadyAtUnixMilli,
			DeadlineUnixMilli: attempt.DeadlineUnixMilli}}, sink, nil)
}

// queryIdentity carries provider-local accounting only. Diagnostic reads do
// not invent a Slot operation or a production recovery permit.
type queryIdentity struct {
	Spec         execution.PhysicalQuerySpec
	AttemptNo    uint32
	SharedSchema bool
	// Budget is the Slot query's, which a failure is timed against; zero
	// for a read that is not a Slot's, which then reports no timing.
	Budget queryBudget
}

// queryBudget is a Slot query's budget as its attempt carries it: where it
// began, when the window could first be read, and the deadline it ends at.
type queryBudget struct{ StartUnixMilli, ReadyAtUnixMilli, DeadlineUnixMilli int64 }

// scopeHeaders are the headers that say whose data a query reads: the
// tenant always, and then either the strategy's space or, for a global
// business Plan, the request to skip the space.
//
// Never both. The provider still applies the filters a space registers for
// a table whenever a space is named, skipped or not - a shared table's
// bk_biz_id filter among them - so a global query that also named its
// space would read the global business's own data and nothing else.
func scopeHeaders(facts execution.QueryPlanFacts) map[string]string {
	if facts.GlobalBusiness {
		return map[string]string{headerTenant: facts.TenantID, headerSkipSpace: skipSpaceValue}
	}
	return map[string]string{headerTenant: facts.TenantID, headerSpace: facts.SpaceScope}
}

// bodySpace is the space_uid the structured request body carries. The
// provider takes the body's space whenever the header names none, so a
// global business Plan leaves it empty as well as the header; the body's
// space alone would scope the query exactly as the header would.
func bodySpace(facts execution.QueryPlanFacts) string {
	if facts.GlobalBusiness {
		return ""
	}
	return facts.SpaceScope
}

func buildWireRequest(spec execution.PhysicalQuerySpec) (string, []byte, error) {
	body, err := buildRequest(spec)
	if err != nil {
		return "", nil, err
	}
	var payload any = body
	path := "/query/ts"
	if query := spec.PlanFacts.PromQL; query != nil {
		path += "/promql"
		// No bk_biz_ids in the body. The scope travels in the space header,
		// which is the only thing the provider resolves it from; bk_biz_ids in
		// a promql body is something else entirely -- the provider appends
		// bk_biz_id IN (ids) to the expression's conditions, and a metric that
		// carries no bk_biz_id label then answers 200 with no series and no
		// status. Custom-reported and bkbase metrics carry none, so every
		// promql strategy over them read as "no data" on every round, and the
		// backend (which pops bk_biz_ids out of the body before sending) saw
		// the data the whole time.
		payload = map[string]any{"promql": query.Expression, "match": query.Match, "start": body.StartTime, "end": body.EndTime, "step": body.Step, "timezone": body.Timezone}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("alarmd access uq: encode request: %w", err)
	}
	return path, encoded, nil
}

func (client *Client) execute(callerCtx, ctx context.Context, attempt queryIdentity, sink execution.ProviderSeriesSink, scanned *DiagnosticScan) (execution.ProviderCompletion, error) {
	path, encoded, err := buildWireRequest(attempt.Spec)
	if err != nil {
		return execution.ProviderCompletion{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if attempt.SharedSchema {
		request.Header.Set("Accept", sharedSchemaAccept)
	}
	request.Header.Set(headerQuerySource, client.querySource)
	for name, value := range scopeHeaders(attempt.Spec.PlanFacts) {
		request.Header.Set(name, value)
	}
	markReplayable(request)
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
		completion := client.unavailableCompletion(attempt, reason, execution.TransportRouteDetail(execution.ClassifyTransportFailure(err)))
		completion.Stats.QueryMillis = uint64(client.now().Sub(started).Milliseconds())
		completion.RouteFacts.Attempts[0].Timing = attemptTiming(attempt.Budget, started, client.now(), 0)
		return completion, nil
	}
	answered := client.now()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// The body is drained and discarded on purpose: UQ error bodies can echo
		// the request (table ids, conditions, dimension values) and must not be
		// parsed for business state or copied into logs. The status code alone
		// is the bounded diagnostic detail. A diagnostic read -- an operator
		// asking why the provider refused this query -- keeps the body's start,
		// sanitized, for its own answer alone.
		if scanned != nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, errorBodyRead))
			scanned.Bytes, scanned.errorExcerpt = uint64(len(body)), providerErrorExcerpt(body)
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, errorBodyRead))
		}
		completion := client.unavailableCompletion(attempt, execution.ReasonCode(contract.ReasonQueryUnavailable), execution.HTTPStatusRouteDetail(response.StatusCode))
		completion.Stats.QueryMillis = uint64(client.now().Sub(started).Milliseconds())
		completion.RouteFacts.Attempts[0].Timing = attemptTiming(attempt.Budget, started, client.now(), 0)
		return completion, nil
	}
	counted := &countingReader{reader: response.Body, now: client.now}
	shared, err := selectSharedDecoder(response.Header.Get("Content-Type"), attempt.SharedSchema)
	if err != nil {
		return execution.ProviderCompletion{}, err
	}
	bounded := &boundedReader{reader: counted, maximum: client.limits.MaxBodyBytes}
	var completion execution.ProviderCompletion
	if shared {
		completion, err = client.decodeShared(ctx, bounded, attempt, sink)
	} else {
		completion, err = client.decodeQuery(ctx, bounded, attempt, sink, scanned)
	}
	if scanned != nil {
		scanned.Bytes = counted.bytes
		scanned.Complete = err == nil
	}
	if err != nil {
		return execution.ProviderCompletion{}, client.bodyFailure(callerCtx, ctx, attempt, err, counted, started, answered)
	}
	completion.Stats.Bytes = counted.bytes
	completion.Stats.QueryMillis = uint64(client.now().Sub(started).Milliseconds())
	completion.Stats.ResponseCodec = "legacy_json"
	if shared {
		completion.Stats.ResponseCodec = "shared_json_v1"
	}
	completion.Stats.SharedSchemaRequested = attempt.SharedSchema
	return completion, nil
}

// bodyFailure names a query whose answer began and whose body did not
// arrive in full: the deadline passed while it was being read or delivered,
// or the connection under it broke. Unnamed, it read as an unclassified
// internal error with no detail, the same as a defect of alarmd's own,
// while the same timeout before the answer began was named and timed.
//
// A deadline is named for whoever held the time when it passed. Inside a
// read begun before it, alarmd was waiting on the backend: the backend's
// timeout, body=timeout. Between reads, or in a read begun after it - which
// only finds out - alarmd was still decoding or delivering what had arrived:
// delivery=timeout, alarmd's own, never the backend's. That one is category
// other, whose code the failure grammar publishes as OTHER whatever it is
// given, so it is given OTHER here: the detail is what names it.
//
// It stays an error. Series already handed on were delivered, and a
// completion cannot describe a body that stopped partway. A failure that
// names itself - a response budget, a sink's own - keeps its name, and a
// caller that gave up keeps its error, as it does before the answer.
func (client *Client) bodyFailure(callerCtx, ctx context.Context, attempt queryIdentity, err error, body *countingReader, started, answered time.Time) error {
	var declared interface{ QueryFailure() (string, string) }
	if callerCtx.Err() != nil || errors.As(err, &declared) {
		return err
	}
	deadline, bounded := ctx.Deadline()
	passed := func(at time.Time) bool { return bounded && !at.Before(deadline) }
	broken := body.failed != nil && errors.Is(err, body.failed)
	category, code := "provider_transport", contract.ReasonQueryTimeout
	var detail string
	switch {
	case broken && passed(body.failedAt):
		detail = execution.BodyRouteDetail(execution.TransportFailureTimeout)
		if passed(body.failedBegan) {
			category, code, detail = "other", "OTHER", execution.DeliveryTimeoutRouteDetail
		}
	case broken:
		class := execution.ClassifyTransportFailure(body.failed)
		if class != execution.TransportFailureTimeout {
			code = contract.ReasonQueryUnavailable
		}
		detail = execution.BodyRouteDetail(class)
	case errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded):
		category, code, detail = "other", "OTHER", execution.DeliveryTimeoutRouteDetail
	default:
		return err
	}
	failed := client.now()
	// What was not spent waiting - for the answer to begin, or inside a read
	// of its body - was alarmd's own decoding and delivery.
	local := failed.Sub(answered) - body.waited
	return &bodyFailureError{err: err, category: category, code: code, detail: detail,
		timing: attemptTiming(attempt.Budget, started, failed, local)}
}

// attemptTiming splits a failed Slot query's budget where its window could
// be read and where its request went out (see execution.AttemptTiming). The
// request's start is taken to the millisecond before splitting, so the
// three parts add up to the budget exactly; elapsed is on the monotonic
// clock, and local is the part of it alarmd spent on the answer itself.
// Nil for a read that is not a Slot's.
func attemptTiming(budget queryBudget, started, failed time.Time, local time.Duration) *execution.AttemptTiming {
	if budget.StartUnixMilli <= 0 || budget.DeadlineUnixMilli <= 0 {
		return nil
	}
	readable := max(budget.StartUnixMilli, budget.ReadyAtUnixMilli)
	sent := started.UnixMilli()
	return &execution.AttemptTiming{
		SettleMillis:    readable - budget.StartUnixMilli,
		StartLateMillis: sent - readable,
		BudgetMillis:    budget.DeadlineUnixMilli - sent,
		ElapsedMillis:   failed.Sub(started).Milliseconds(),
		LocalMillis:     local.Milliseconds(),
	}
}

func (client *Client) unavailableCompletion(attempt queryIdentity, reason execution.ReasonCode, detail string) execution.ProviderCompletion {
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

func providerResultRef(attempt queryIdentity) execution.ProviderResultRef {
	return execution.ProviderResultRef(string(attempt.Spec.Digest) + ":" + strconv.FormatUint(uint64(attempt.AttemptNo), 10))
}

type countingReader struct {
	reader io.Reader
	bytes  uint64

	// The reads are timed: the time spent inside them is the time spent
	// waiting on the backend for the body. failed is the first error a read
	// returned other than the body's end, and failedBegan and failedAt are
	// when that read began and returned.
	now         func() time.Time
	waited      time.Duration
	failed      error
	failedBegan time.Time
	failedAt    time.Time
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
	began := reader.now()
	count, err := reader.reader.Read(buffer)
	returned := reader.now()
	reader.waited += returned.Sub(began)
	reader.bytes += uint64(count)
	if err != nil && err != io.EOF && reader.failed == nil {
		reader.failed, reader.failedBegan, reader.failedAt = err, began, returned
	}
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
		queryConditions, err := mapConditions(source.Conditions)
		if err != nil {
			return request{}, err
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
			Conditions: queryConditions,
			Offset:     source.Offset, OffsetForward: offsetForward,
			KeepColumns: append([]string(nil), source.KeepColumns...), QueryString: source.QueryString,
		})
	}
	return request{TSDBMap: spec.PlanFacts.TSDBMap, QueryList: queries, MetricMerge: spec.PlanFacts.MetricMerge,
		StartTime: strconv.FormatInt(spec.ProviderRange.Start, 10), EndTime: strconv.FormatInt(spec.ProviderRange.End, 10),
		Step: durationString(spec.PlanFacts.StepMillis), SpaceUID: bodySpace(spec.PlanFacts),
		DownSampleRange: string(spec.PlanFacts.DownSampleRange), Timezone: spec.PlanFacts.Timezone,
		NotTimeAlign: spec.PlanFacts.NotTimeAlign}, nil
}

func mapConditions(source execution.QueryConditions) (conditions, error) {
	fields := make([]conditionField, 0, len(source.Fields))
	for _, field := range source.Fields {
		wildcard, err := parseQueryBool("is_wildcard", field.Wildcard)
		if err != nil {
			return conditions{}, err
		}
		prefix, err := parseQueryBool("is_prefix", field.Prefix)
		if err != nil {
			return conditions{}, err
		}
		suffix, err := parseQueryBool("is_suffix", field.Suffix)
		if err != nil {
			return conditions{}, err
		}
		values := make([]string, 0, len(field.Values))
		for _, value := range field.Values {
			text, scalarErr := scalarText(value)
			if scalarErr != nil {
				return conditions{}, scalarErr
			}
			values = append(values, text)
		}
		fields = append(fields, conditionField{Field: field.Field, Operator: field.Operator, Values: values,
			Wildcard: wildcard, Prefix: prefix, Suffix: suffix})
	}
	return conditions{Fields: fields, Connectors: append([]string(nil), source.Connectors...)}, nil
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
	return client.decodeQuery(ctx, reader, queryIdentity{Spec: attempt.Spec, AttemptNo: attempt.AttemptNo}, sink, nil)
}

func (client *Client) decodeQuery(ctx context.Context, reader io.Reader, attempt queryIdentity, sink execution.ProviderSeriesSink, scanned *DiagnosticScan) (completion execution.ProviderCompletion, err error) {
	session := client.newSeriesDecoder(attempt, sink, scanned)
	defer session.limitCompletion(&completion, &err)
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil {
		return completion, err
	}
	if opening != json.Delim('{') {
		return completion, errors.New("alarmd access uq: response must be an object")
	}
	var status *responseStatus
	var isPartial *bool
	var resultTableIDs []string
	session.receivedAt = client.now().Unix()
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return completion, err
		}
		keyToken, err := decoder.Token()
		if err != nil {
			return completion, fmt.Errorf("alarmd access uq: decode field: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return completion, errors.New("alarmd access uq: response field name is invalid")
		}
		switch key {
		case "series":
			start, err := decoder.Token()
			if err != nil || start != json.Delim('[') {
				return completion, errors.New("alarmd access uq: series must be an array")
			}
			for decoder.More() {
				var raw json.RawMessage
				if err := decoder.Decode(&raw); err != nil {
					return completion, fmt.Errorf("alarmd access uq: decode series payload: %w", err)
				}
				if int64(len(raw)) > client.limits.MaxSeriesBytes {
					return completion, ErrSeriesBytesExceeded
				}
				var series responseSeries
				if err := json.Unmarshal(raw, &series); err != nil {
					return completion, fmt.Errorf("alarmd access uq: decode series: %w", err)
				}
				if err := session.accept(ctx, series, uint64(len(raw))); err != nil {
					return completion, err
				}
			}
			if _, err := decoder.Token(); err != nil {
				return completion, err
			}
		case "status":
			if err := decoder.Decode(&status); err != nil {
				return completion, err
			}
			if scanned != nil && status != nil {
				scanned.statusCode = status.Code
			}
		case "is_partial":
			var value bool
			if err := decoder.Decode(&value); err != nil {
				return completion, err
			}
			isPartial = &value
		case "result_table_id":
			if err := decoder.Decode(&resultTableIDs); err != nil {
				return completion, err
			}
		default:
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return completion, err
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return completion, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return completion, fmt.Errorf("alarmd access uq: decode trailing payload: %w", err)
		}
		return completion, fmt.Errorf("alarmd access uq: unexpected trailing token %v", token)
	}
	return session.finish(status, isPartial, resultTableIDs)
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
	attempt queryIdentity,
	reason execution.ReasonCode,
	detail string,
	dataState execution.DataState,
	delivery execution.SeriesDelivery,
	resultTableIDs []string,
	stats execution.ProviderStats,
) execution.ProviderCompletion {
	completion := client.unavailableCompletion(attempt, reason, detail)
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
	if len(source.Columns) == 0 || len(source.Columns) != len(source.Types) || (spec.PlanFacts.Normalization.Version != "uq-polling-normalization-v1" && len(source.GroupKeys) != len(source.GroupValues)) {
		return execution.ProviderSeriesBatch{}, 0, errors.New("alarmd access uq: invalid series schema")
	}
	dimensions := make(map[string]json.RawMessage, len(source.GroupKeys))
	for index, key := range source.GroupKeys {
		key = stripTableSuffix(key)
		if alias, ok := spec.PlanFacts.Normalization.DimensionAliases[key]; ok {
			key = alias
		}
		if _, exists := dimensions[key]; exists {
			return execution.ProviderSeriesBatch{}, 0, errors.New("alarmd access uq: duplicate normalized group key")
		}
		encoded := nullDimension
		if index < len(source.GroupValues) {
			encoded = source.GroupValues[index]
		}
		var scalar any
		if err := json.Unmarshal(encoded, &scalar); err != nil {
			return execution.ProviderSeriesBatch{}, 0, err
		}
		switch scalar.(type) {
		case nil, string, float64, bool:
		default:
			return execution.ProviderSeriesBatch{}, 0, errors.New("alarmd access uq: nonscalar group value")
		}
		dimensions[key] = encoded
	}
	var nullIdentityFields uint64
	identityFields := make([]contract.DimensionFieldV2, 0, len(spec.PlanFacts.Normalization.DatasetContract.IdentityFields))
	names := spec.PlanFacts.Normalization.DatasetContract.IdentityFields
	if spec.PlanFacts.Normalization.DatasetContract.DynamicDimensions {
		names = make([]string, 0, len(dimensions))
		for name := range dimensions {
			names = append(names, name)
		}
	}
	for _, name := range names {
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
	// The identity keeps the canonical encoding of its fields: every record
	// carries the same fields, and the series' delivery digest takes their
	// encoding from here rather than making it again.
	identity, err := contract.EncodeDimensionIdentityV2(spec.PlanFacts.TenantID, spec.PlanFacts.BusinessID, identityFields)
	if err != nil {
		return execution.ProviderSeriesBatch{}, 0, err
	}
	dimensionDigest := identity.Digest
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
		if spec.PlanFacts.NotTimeAlign && !onRequestGrid(spec, sourceTime) {
			return execution.ProviderSeriesBatch{}, 0, errOffRequestGrid
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
	digest, err := contract.DeriveSeriesRecordsDigestV2("alarmd-provider-series-delivery-v1", records, identity)
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

// errOffRequestGrid is a series of an unaligned query - a Plan detected more
// often than it aggregates - with a point that is not where its request's
// buckets are: the storage bucketed on its own grid, the aggregation
// interval's from the epoch, whatever start it was asked for.
var errOffRequestGrid = errors.New("alarmd access uq: unaligned query answered off its request's grid")

// onRequestGrid says whether an unaligned query's point sits where its
// request's buckets start: the accepted range's start plus a whole number of
// data steps.
func onRequestGrid(spec execution.PhysicalQuerySpec, sourceTime int64) bool {
	step := spec.PlanFacts.StepMillis / 1000
	if step <= 0 {
		return true
	}
	return (sourceTime-spec.AcceptedRange.Start)%step == 0
}
