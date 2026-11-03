package uq

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// DiagnosticTimeout is the least a diagnostic query is given; a caller that
// knows the query's own budget gives it that instead (Query's budget).
const (
	DiagnosticTimeout        = 3 * time.Second
	DiagnosticMaxOutputBytes = 1 << 20
)

// DiagnosticLimits are the production query's own decode bounds: a re-query
// reads what the Slot read, however large, and keeps only the selected
// series, so only the output is bounded apart (DiagnosticMaxOutputBytes,
// the selection). Output selection never changes the provider request or
// its upstream query cost.
func DiagnosticLimits() Limits {
	return DefaultLimits()
}

type DiagnosticPreview struct {
	Path          string            `json:"path"`
	Body          json.RawMessage   `json:"body"`
	Headers       map[string]string `json:"headers"`
	RequestDigest string            `json:"request_digest"`
}

type DiagnosticSelection struct {
	SeriesDigest string `json:"series_digest,omitempty"`
	// Offset is how many matching series to pass over before returning
	// any: the next page of a re-query whose earlier page was full.
	Offset    int `json:"offset,omitempty"`
	MaxSeries int `json:"max_series,omitempty"`
	// MaxPoints bounds all returned series together, not each series.
	MaxPoints int `json:"max_points,omitempty"`
}

type DiagnosticScan struct {
	Bytes   uint64 `json:"bytes"`
	Series  uint64 `json:"series"`
	Records uint64 `json:"records"`
	// Complete means the entire provider envelope, including its tail status,
	// was decoded. It does not mean the provider declared complete evidence.
	Complete   bool `json:"complete"`
	statusCode string
	// errorExcerpt is the start of the body of an answer that was not 200,
	// sanitized (providerErrorExcerpt): kept for the diagnostic read alone.
	errorExcerpt string
}

type DiagnosticPoint struct {
	RecordID   string          `json:"record_id"`
	SourceTime int64           `json:"source_time"`
	Value      json.RawMessage `json:"value"`
}

type DiagnosticSeries struct {
	SeriesDigest string                     `json:"series_digest"`
	Dimensions   map[string]json.RawMessage `json:"dimensions"`
	Points       []DiagnosticPoint          `json:"points"`
}

// DiagnosticCompletion projects the final production-normalized completion.
// Endpoint URLs and raw upstream error messages never leave this boundary,
// with one exception: ErrorExcerpt, the start of the body of an answer that
// was not 200, sanitized (providerErrorExcerpt), for an operator asking why
// the provider refused a query. Only this read keeps it: detection drains
// and drops such a body, and it reaches no log or published snapshot.
type DiagnosticCompletion struct {
	Completeness   execution.Completeness `json:"completeness"`
	DataState      execution.DataState    `json:"data_state"`
	Status         *DiagnosticStatus      `json:"status,omitempty"`
	ResultTableIDs []string               `json:"result_table_ids"`
	RouteDetails   []string               `json:"route_details"`
	ErrorExcerpt   string                 `json:"error_excerpt,omitempty"`
}

type DiagnosticStatus struct {
	Code    string `json:"code"`
	Allowed bool   `json:"allowed"`
}

type DiagnosticResult struct {
	RequestDigest  string                `json:"request_digest"`
	Series         []DiagnosticSeries    `json:"series"`
	Completion     *DiagnosticCompletion `json:"completion,omitempty"`
	Scan           DiagnosticScan        `json:"scan"`
	MatchedSeries  uint64                `json:"matched_series"`
	ReturnedPoints int                   `json:"returned_points"`
	// NextOffset is the offset of the next page when matching series were
	// left after this one, zero when this page was the last.
	NextOffset  int      `json:"next_offset,omitempty"`
	Truncated   bool     `json:"truncated"`
	Complete    bool     `json:"complete"`
	Limitations []string `json:"limitations"`
}

// DiagnosticError contains only a fixed safe code. No endpoint, expression,
// response body or transport error string is copied to its text.
type DiagnosticError struct {
	Code string `json:"code"`
}

func (e *DiagnosticError) Error() string { return "alarmd diagnostic query: " + e.Code }

// DiagnosticClient re-runs a Slot's query for an operator. Re-queries run
// side by side with no count of their own: they are a handful next to the
// business queries UQ serves.
type DiagnosticClient struct {
	client *Client
}

// NewDiagnosticClient accepts a trusted deployment endpoint and HTTP transport,
// never request-supplied URLs or headers. Redirects are disabled and no query retry is performed.
func NewDiagnosticClient(endpoint, querySource string, httpClient *http.Client) (*DiagnosticClient, error) {
	return newDiagnosticClient(endpoint, querySource, httpClient, DiagnosticLimits())
}

func newDiagnosticClient(endpoint, querySource string, httpClient *http.Client, limits Limits) (*DiagnosticClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || httpClient == nil {
		return nil, &DiagnosticError{Code: "diagnostic_config_invalid"}
	}
	bounded := *httpClient
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client, err := NewClientWithLimits(endpoint, querySource, &bounded, limits)
	if err != nil {
		return nil, &DiagnosticError{Code: "diagnostic_config_invalid"}
	}
	return &DiagnosticClient{client: client}, nil
}

func (client *DiagnosticClient) Preview(spec execution.PhysicalQuerySpec) (DiagnosticPreview, error) {
	if client == nil || client.client == nil {
		return DiagnosticPreview{}, &DiagnosticError{Code: "diagnostic_config_invalid"}
	}
	path, body, err := buildWireRequest(spec)
	if err != nil {
		return DiagnosticPreview{}, &DiagnosticError{Code: "query_spec_invalid"}
	}
	headers := scopeHeaders(spec.PlanFacts)
	headers["Content-Type"], headers[headerQuerySource] = "application/json", client.client.querySource
	// The digest covers the exact provider path/body and semantic headers, not
	// the current endpoint, secrets or output selection. The preview preserves
	// business conditions and expressions so operators can inspect the query.
	encoded, err := json.Marshal(struct {
		Path    string
		Body    json.RawMessage
		Headers map[string]string
	}{path, body, headers})
	if err != nil {
		return DiagnosticPreview{}, &DiagnosticError{Code: "query_spec_invalid"}
	}
	digest := sha256.Sum256(encoded)
	return DiagnosticPreview{Path: path, Body: body, Headers: headers, RequestDigest: hex.EncodeToString(digest[:])}, nil
}

// Query re-runs spec and returns the selected series. budget is how long the
// query may take - the Slot's own query budget, so a slow Slot can be read
// again - at least DiagnosticTimeout.
func (client *DiagnosticClient) Query(ctx context.Context, spec execution.PhysicalQuerySpec, selection DiagnosticSelection, budget time.Duration) (DiagnosticResult, error) {
	result := DiagnosticResult{Series: []DiagnosticSeries{}, Limitations: []string{}}
	ctx, cancel := context.WithTimeout(ctx, max(DiagnosticTimeout, budget))
	defer cancel()
	if err := ctx.Err(); err != nil {
		return result, diagnosticError(err)
	}
	preview, err := client.Preview(spec)
	if err != nil {
		return result, err
	}
	result.RequestDigest = preview.RequestDigest
	if selection.MaxSeries == 0 {
		selection.MaxSeries = 5
	}
	if selection.MaxPoints == 0 {
		selection.MaxPoints = 100
	}
	if selection.MaxSeries < 1 || selection.MaxSeries > 10 || selection.MaxPoints < 1 || selection.MaxPoints > 500 || selection.Offset < 0 {
		return result, &DiagnosticError{Code: "query_selection_invalid"}
	}
	if selection.SeriesDigest != "" {
		decoded, err := hex.DecodeString(selection.SeriesDigest)
		if err != nil || len(decoded) != 32 {
			return result, &DiagnosticError{Code: "query_selection_invalid"}
		}
	}
	if err := ctx.Err(); err != nil {
		return result, diagnosticError(err)
	}
	sink := &diagnosticSink{selection: selection, result: &result, valueField: spec.PlanFacts.Normalization.CanonicalValueField}
	// Only provider-local identity is needed for decoding. This is not a
	// production QueryAttempt: no Slot runner, operation or permit is created.
	completion, err := client.client.execute(ctx, ctx, queryIdentity{Spec: spec, AttemptNo: 1}, sink, &result.Scan)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		safe := diagnosticError(err)
		result.Limitations = append(result.Limitations, safe.Code)
		return result, safe
	}
	final := &DiagnosticCompletion{Completeness: completion.Completeness, DataState: completion.DataState,
		ResultTableIDs: []string{}, RouteDetails: []string{}}
	// Table identifiers come from the response too. Reserve a small metadata
	// budget beside the selected-series budget rather than returning an
	// unbounded metadata array after otherwise bounding points and dimensions.
	metadataBytes := 0
	for _, id := range completion.RouteFacts.ResultTableIDs {
		encoded, _ := json.Marshal(id)
		metadataBytes += len(encoded) + 1
		if metadataBytes > 16<<10 {
			result.Truncated = true
			result.Limitations = append(result.Limitations, "provider_metadata_truncated")
			break
		}
		final.ResultTableIDs = append(final.ResultTableIDs, id)
	}
	if result.Scan.statusCode != "" {
		code := strings.ToUpper(strings.TrimPrefix(execution.ResponseStatusRouteDetail(result.Scan.statusCode), "response=status_"))
		final.Status = &DiagnosticStatus{Code: code, Allowed: completion.RouteFacts.Status != nil && completion.RouteFacts.Status.Allowed}
	}
	for _, attempt := range completion.RouteFacts.Attempts {
		if attempt.Detail != "" {
			final.RouteDetails = append(final.RouteDetails, attempt.Detail)
		}
	}
	final.ErrorExcerpt = result.Scan.errorExcerpt
	result.Completion = final
	if completion.Completeness != execution.CompletenessFull {
		result.Limitations = append(result.Limitations, "provider_"+strings.ToLower(string(completion.Completeness)))
	}
	result.Complete = completion.Completeness == execution.CompletenessFull && !result.Truncated
	return result, nil
}

func diagnosticError(err error) *DiagnosticError {
	code := "provider_response_invalid"
	switch {
	case errors.Is(err, context.Canceled):
		code = "query_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		code = "query_timeout"
	case errors.Is(err, ErrResponseBytesExceeded):
		code = "response_bytes_exceeded"
	case errors.Is(err, ErrSeriesBytesExceeded):
		code = "series_bytes_exceeded"
	case errors.Is(err, ErrTotalSeriesExceeded):
		code = "total_series_exceeded"
	case errors.Is(err, ErrTotalRecordsExceeded):
		code = "total_records_exceeded"
	}
	return &DiagnosticError{Code: code}
}

type diagnosticSink struct {
	selection   DiagnosticSelection
	result      *DiagnosticResult
	valueField  string
	outputBytes int
}

func (sink *diagnosticSink) truncate() {
	if !sink.result.Truncated {
		sink.result.Limitations = append(sink.result.Limitations, "output_truncated")
	}
	sink.result.Truncated = true
}

func (sink *diagnosticSink) ConsumeProviderSeries(ctx context.Context, batch execution.ProviderSeriesBatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	first, found := batch.Dataset.Record(0)
	if !found {
		return nil
	}
	digest := first.DimensionIdentityDigest()
	if sink.selection.SeriesDigest != "" && sink.selection.SeriesDigest != digest {
		return nil
	}
	sink.result.MatchedSeries++
	if int(sink.result.MatchedSeries) <= sink.selection.Offset {
		return nil
	}
	if len(sink.result.Series) >= sink.selection.MaxSeries || sink.result.ReturnedPoints >= sink.selection.MaxPoints {
		// A page is full: what is left is the next page, not lost.
		if sink.result.NextOffset == 0 {
			sink.result.NextOffset = sink.selection.Offset + len(sink.result.Series)
		}
		sink.truncate()
		return nil
	}
	series := DiagnosticSeries{SeriesDigest: digest, Dimensions: first.Dimensions(), Points: []DiagnosticPoint{}}
	header, _ := json.Marshal(series)
	if sink.outputBytes+len(header) > DiagnosticMaxOutputBytes {
		sink.truncate()
		return nil
	}
	sink.outputBytes += len(header)
	for index := 0; index < batch.Dataset.Len(); index++ {
		if sink.result.ReturnedPoints >= sink.selection.MaxPoints {
			sink.truncate()
			break
		}
		record, _ := batch.Dataset.Record(index)
		value, found := record.Value(sink.valueField)
		if !found {
			return errors.New("missing canonical value")
		}
		point := DiagnosticPoint{RecordID: record.RecordID(), SourceTime: record.SourceTime(), Value: value}
		encoded, _ := json.Marshal(point)
		if sink.outputBytes+len(encoded)+1 > DiagnosticMaxOutputBytes {
			sink.truncate()
			break
		}
		sink.outputBytes += len(encoded) + 1
		series.Points = append(series.Points, point)
		sink.result.ReturnedPoints++
	}
	sink.result.Series = append(sink.result.Series, series)
	return nil
}
