// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	rawBatchContentType            = "application/x-ndjson"
	rawBatchTraceQueryBodyMaxBytes = 16 * 1024
)

// RawBatchMember keeps the stable request ordinal together with the one
// prepared-query pointer that owns the member's formatter and decoder state.
type RawBatchMember struct {
	Ordinal  int
	Prepared *PreparedRawQuery
}

// RawBatch is one sequentially packed _msearch request. Its wire body is kept
// private so callers cannot separate it from the member order used to decode
// the response.
type RawBatch struct {
	members []RawBatchMember
	ndjson  string
}

// Members returns the stable member sequence without copying prepared queries.
func (b *RawBatch) Members() []RawBatchMember {
	if b == nil {
		return nil
	}
	return append([]RawBatchMember(nil), b.members...)
}

// MemberCount returns the number of independent searches in the request.
func (b *RawBatch) MemberCount() int {
	if b == nil {
		return 0
	}
	return len(b.members)
}

// BodyBytes returns the exact encoded NDJSON size used for body budgeting.
func (b *RawBatch) BodyBytes() int {
	if b == nil {
		return 0
	}
	return len([]byte(b.ndjson))
}

// RawBatchOversizedMember identifies a prepared member that cannot fit in an
// otherwise empty batch. The caller can execute its pointer through the
// prepared single-query path without repeating alias or mapping preparation.
type RawBatchOversizedMember struct {
	Member    RawBatchMember
	BodyBytes int
}

// RawBatchMemberResult is one ordinal-preserving child result. Child errors
// leave Rows, Size, Total and Option empty without affecting sibling results.
type RawBatchMemberResult struct {
	Ordinal           int
	Rows              []map[string]any
	Size              int64
	Total             int64
	Option            *metadata.ResultTableOption
	FallbackAttempted bool
	FallbackSucceeded bool
	Err               error
}

// RawBatchTransportError denotes a failure of the whole _msearch exchange or
// response envelope. Its rendered text intentionally excludes endpoint,
// indexes, headers, query values and Elasticsearch reasons.
type RawBatchTransportError struct {
	Status int
	Kind   string
}

func (e *RawBatchTransportError) Error() string {
	if e == nil {
		return "elasticsearch raw batch transport failed"
	}
	switch e.Kind {
	case "member_already_consumed":
		return "elasticsearch raw batch member already consumed"
	case "response_count_mismatch":
		return "elasticsearch raw batch response count mismatch"
	case "nil_batch":
		return "elasticsearch raw batch is empty"
	case "incomplete_member":
		return "elasticsearch raw batch member is incomplete"
	case "mixed_connection":
		return "elasticsearch raw batch contains mixed connections"
	}
	if e.Status > 0 {
		return fmt.Sprintf("elasticsearch raw batch transport failed with status %d", e.Status)
	}
	return "elasticsearch raw batch transport failed"
}

// RawBatchChildError is a member-scoped Elasticsearch error. Only the status
// and a constrained error type are rendered, so raw ES reasons cannot expose
// query values or index names.
type RawBatchChildError struct {
	Status int
	Type   string
}

func (e *RawBatchChildError) Error() string {
	if e == nil {
		return "elasticsearch raw batch child failed"
	}
	if e.Status > 0 {
		return fmt.Sprintf(
			"elasticsearch raw batch child failed with status %d (type=%s)",
			e.Status,
			e.Type,
		)
	}
	return fmt.Sprintf("elasticsearch raw batch child failed (type=%s)", e.Type)
}

// PreparedRawQueryFingerprint hashes only the final serialized search body.
// Index targets stay member-local in the NDJSON header and do not affect this
// body-equivalence key.
func PreparedRawQueryFingerprint(prepared *PreparedRawQuery) (string, error) {
	if prepared == nil || prepared.body == "" {
		return "", fmt.Errorf("prepared raw query body is empty")
	}
	sum := sha256.Sum256([]byte(prepared.body))
	return hex.EncodeToString(sum[:]), nil
}

type rawBatchHeader struct {
	Index []string `json:"index"`
}

func encodeRawBatchMember(member RawBatchMember) (string, error) {
	if member.Prepared == nil || member.Prepared.queryOption == nil {
		return "", fmt.Errorf("prepared raw query is incomplete")
	}
	if member.Prepared.body == "" {
		return "", fmt.Errorf("prepared raw query body is empty")
	}
	header, err := json.Marshal(rawBatchHeader{
		Index: append([]string(nil), member.Prepared.queryOption.indexes...),
	})
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	builder.Grow(len(header) + len(member.Prepared.body) + 2)
	builder.Write(header)
	builder.WriteByte('\n')
	builder.WriteString(member.Prepared.body)
	builder.WriteByte('\n')
	return builder.String(), nil
}

// PackRawBatchMembers uses deterministic first-fit sequential packing. Every
// member is emitted exactly once, either in a batch or as an oversized marker.
func PackRawBatchMembers(
	members []RawBatchMember,
	maxMembers int,
	maxBodyBytes int,
) (batches []*RawBatch, oversized []RawBatchOversizedMember, err error) {
	if maxMembers <= 0 {
		return nil, nil, fmt.Errorf("raw batch max members must be positive")
	}
	if maxBodyBytes <= 0 {
		return nil, nil, fmt.Errorf("raw batch max body bytes must be positive")
	}

	currentMembers := make([]RawBatchMember, 0, min(maxMembers, len(members)))
	var currentBody strings.Builder
	flush := func() {
		if len(currentMembers) == 0 {
			return
		}
		batches = append(batches, &RawBatch{
			members: append([]RawBatchMember(nil), currentMembers...),
			ndjson:  currentBody.String(),
		})
		currentMembers = currentMembers[:0]
		currentBody.Reset()
	}

	for _, member := range members {
		encoded, encodeErr := encodeRawBatchMember(member)
		if encodeErr != nil {
			return nil, nil, encodeErr
		}
		encodedBytes := len([]byte(encoded))
		if encodedBytes > maxBodyBytes {
			flush()
			oversized = append(oversized, RawBatchOversizedMember{
				Member:    member,
				BodyBytes: encodedBytes,
			})
			continue
		}

		if len(currentMembers) > 0 &&
			(len(currentMembers)+1 > maxMembers || currentBody.Len()+encodedBytes > maxBodyBytes) {
			flush()
		}
		currentMembers = append(currentMembers, member)
		currentBody.WriteString(encoded)
	}
	flush()

	return batches, oversized, nil
}

// ExecuteRawBatch claims each prepared member exactly once, sends one fixed
// GET /_msearch request, then decodes child responses by ordinal. Whole-batch
// failures are returned once and are never expanded into member _search calls.
func (i *Instance) ExecuteRawBatch(
	ctx context.Context,
	batch *RawBatch,
	maxConcurrentSearches int,
) (results []RawBatchMemberResult, err error) {
	ctx, span := trace.NewSpan(ctx, "elasticsearch-query-raw-batch")
	startedAt := time.Now()
	wireAttempted := false
	wireEvent := metric.QueryRawESBatchEventWireTransportFailure
	defer func() {
		if wireAttempted {
			if wireEvent != "" {
				metric.QueryRawESBatchEventInc(ctx, wireEvent)
			}
			metric.QueryRawESBatchDurationObserve(
				ctx,
				metric.QueryRawESBatchDurationExecute,
				time.Since(startedAt),
			)
		}
		span.End(&err)
	}()

	if batch == nil || len(batch.members) == 0 || batch.ndjson == "" {
		return nil, &RawBatchTransportError{Kind: "nil_batch"}
	}
	span.Set("es_batch_member_count", batch.MemberCount())
	span.Set("es_batch_body_bytes", batch.BodyBytes())
	span.Set("es_batch_max_concurrent_searches", maxConcurrentSearches)

	connect, err := i.validateRawBatchMembers(ctx, batch.members)
	if err != nil {
		return nil, err
	}
	recordRawBatchRequestTrace(span, batch)
	claimedMembers := make([]*PreparedRawQuery, 0, len(batch.members))
	for _, member := range batch.members {
		if !member.Prepared.claimed.CompareAndSwap(false, true) {
			for _, claimed := range claimedMembers {
				claimed.claimed.Store(false)
			}
			return nil, &RawBatchTransportError{Kind: "member_already_consumed"}
		}
		claimedMembers = append(claimedMembers, member.Prepared)
	}

	client, err := i.getClient(ctx, connect)
	if err != nil {
		return nil, newRawBatchTransportError("client", 0, err)
	}
	defer client.Stop()

	params := make(url.Values)
	if maxConcurrentSearches > 0 {
		params.Set("max_concurrent_searches", strconv.Itoa(maxConcurrentSearches))
	}
	wireAttempted = true
	metric.QueryRawESBatchMemberCountObserve(ctx, batch.MemberCount())
	metric.QueryRawESBatchBodyBytesObserve(ctx, batch.BodyBytes())
	response, err := client.PerformRequest(ctx, elastic.PerformRequestOptions{
		Method:      http.MethodGet,
		Path:        "/_msearch",
		Params:      params,
		Body:        batch.ndjson,
		ContentType: rawBatchContentType,
	})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		return nil, newRawBatchTransportError("request", status, err)
	}
	if response == nil {
		return nil, newRawBatchTransportError("empty_response", 0, nil)
	}

	var multiSearchResult elastic.MultiSearchResult
	if err := json.Unmarshal(response.Body, &multiSearchResult); err != nil {
		return nil, newRawBatchTransportError("decode", response.StatusCode, err)
	}
	if len(multiSearchResult.Responses) != len(batch.members) {
		return nil, &RawBatchTransportError{
			Status: response.StatusCode,
			Kind:   "response_count_mismatch",
		}
	}
	recordRawBatchResponseTrace(span, batch.members, multiSearchResult.Responses)

	results = make([]RawBatchMemberResult, len(batch.members))
	childErrors := 0
	fallbackAttempted := 0
	fallbackRecovered := 0
	for index, member := range batch.members {
		results[index] = i.executeRawBatchMember(
			ctx,
			client,
			member,
			multiSearchResult.Responses[index],
		)
		if results[index].Err != nil {
			childErrors++
		}
		if results[index].FallbackAttempted {
			fallbackAttempted++
			if results[index].FallbackSucceeded {
				fallbackRecovered++
			}
		}
	}

	metric.QueryRawESBatchEventAdd(
		ctx,
		metric.QueryRawESBatchEventWireChildSuccess,
		len(results)-childErrors,
	)
	if childErrors > 0 {
		metric.QueryRawESBatchEventAdd(ctx, metric.QueryRawESBatchEventWireChildFailure, childErrors)
		span.Set("es_batch_item_errors", childErrors)
		if childErrors < len(results) {
			wireEvent = metric.QueryRawESBatchEventWirePartial
		} else {
			wireEvent = ""
		}
	} else {
		wireEvent = metric.QueryRawESBatchEventWireSuccess
	}
	if fallbackAttempted > 0 {
		fallbackFailed := fallbackAttempted - fallbackRecovered
		metric.QueryRawESBatchEventAdd(
			ctx,
			metric.QueryRawESBatchEventFallbackAttempted,
			fallbackAttempted,
		)
		metric.QueryRawESBatchEventAdd(
			ctx,
			metric.QueryRawESBatchEventFallbackRecovered,
			fallbackRecovered,
		)
		metric.QueryRawESBatchEventAdd(
			ctx,
			metric.QueryRawESBatchEventFallbackFailed,
			fallbackFailed,
		)
		span.Set("es_batch_fallback_reason", "missing_mapping_empty_index")
		span.Set("es_batch_fallback_attempted", fallbackAttempted)
		span.Set("es_batch_fallback_recovered", fallbackRecovered)
		span.Set("es_batch_fallback_failed", fallbackFailed)
	}

	return results, nil
}

func recordRawBatchRequestTrace(span *trace.Span, batch *RawBatch) {
	members := batch.members
	ordinals := make([]int64, len(members))
	tableIDs := make([]string, len(members))
	indexCounts := make([]int64, len(members))
	sharedQueryBody := members[0].Prepared.body
	for index, member := range members {
		ordinals[index] = int64(member.Ordinal)
		tableIDs[index] = member.Prepared.query.TableID
		indexCounts[index] = int64(len(member.Prepared.queryOption.indexes))
		if member.Prepared.body != sharedQueryBody {
			sharedQueryBody = ""
		}
	}

	span.Set("es_batch_member_ordinals", ordinals)
	span.Set("es_batch_member_table_ids", tableIDs)
	span.Set("es_batch_member_index_counts", indexCounts)
	span.Set("es_batch_shared_query_body", sharedQueryBody != "")
	if sharedQueryBody == "" {
		return
	}
	span.Set("query-body-size", len([]byte(sharedQueryBody)))
	queryBody, truncated := truncateRawBatchTraceQueryBody(sharedQueryBody)
	span.Set("query-body-truncated", truncated)
	span.Set("query-body", queryBody)
}

func truncateRawBatchTraceQueryBody(body string) (string, bool) {
	if len(body) <= rawBatchTraceQueryBodyMaxBytes {
		return body, false
	}
	end := rawBatchTraceQueryBodyMaxBytes
	for end > 0 && !utf8.ValidString(body[:end]) {
		end--
	}
	return body[:end], true
}

func recordRawBatchResponseTrace(
	span *trace.Span,
	members []RawBatchMember,
	children []*elastic.SearchResult,
) {
	tookMillis := make([]int64, len(children))
	statuses := make([]int64, len(children))
	totalHits := make([]int64, len(children))
	shardsTotal := make([]int64, len(children))
	shardsSuccessful := make([]int64, len(children))
	shardsFailed := make([]int64, len(children))
	errorTypes := make([]string, len(children))
	timedOutOrdinals := make([]int64, 0)

	for index, child := range children {
		errorTypes[index] = "none"
		if child == nil {
			errorTypes[index] = "nil_response"
			continue
		}
		tookMillis[index] = child.TookInMillis
		statuses[index] = int64(child.Status)
		totalHits[index] = child.TotalHits()
		if child.TimedOut {
			timedOutOrdinals = append(timedOutOrdinals, int64(members[index].Ordinal))
		}
		if child.Shards != nil {
			shardsTotal[index] = int64(child.Shards.Total)
			shardsSuccessful[index] = int64(child.Shards.Successful)
			shardsFailed[index] = int64(child.Shards.Failed)
		}
		var childError *RawBatchChildError
		if errors.As(rawBatchChildError(child), &childError) {
			errorTypes[index] = childError.Type
		}
	}

	span.Set("es_batch_child_took_millis", tookMillis)
	span.Set("es_batch_child_timed_out_ordinals", timedOutOrdinals)
	span.Set("es_batch_child_statuses", statuses)
	span.Set("es_batch_child_total_hits", totalHits)
	span.Set("es_batch_child_shards_total", shardsTotal)
	span.Set("es_batch_child_shards_successful", shardsSuccessful)
	span.Set("es_batch_child_shards_failed", shardsFailed)
	span.Set("es_batch_child_error_types", errorTypes)
}

func (i *Instance) executeRawBatchMember(
	ctx context.Context,
	client *elastic.Client,
	member RawBatchMember,
	child *elastic.SearchResult,
) (result RawBatchMemberResult) {
	result.Ordinal = member.Ordinal
	defer func() {
		if recover() != nil {
			result.Rows = nil
			result.Size = 0
			result.Total = 0
			result.Option = nil
			result.FallbackSucceeded = false
			result.Err = &RawBatchChildError{Type: "decode_panic"}
		}
	}()

	if child == nil {
		result.Err = &RawBatchChildError{Type: "nil_response"}
		return result
	}
	fallbackRetried := false
	if fallbackChild, attempted, retried := i.tryFallbackEmptyMissingMappingIndexesForBatch(
		ctx,
		client,
		member.Prepared,
		child,
	); attempted {
		result.FallbackAttempted = true
		fallbackRetried = retried
		if retried {
			child = fallbackChild
		}
	}
	if childErr := rawBatchChildError(child); childErr != nil {
		result.Err = childErr
		return result
	}
	result.FallbackSucceeded = result.FallbackAttempted && fallbackRetried

	rowCapacity := 0
	if child.Hits != nil {
		rowCapacity = len(child.Hits.Hits)
	}
	dataCh := make(chan map[string]any, rowCapacity)
	size, total, option, decodeErr := decodePreparedRawResult(member.Prepared, child, dataCh)
	close(dataCh)
	if decodeErr != nil {
		result.Err = &RawBatchChildError{Type: "decode_error"}
		return result
	}
	rows := make([]map[string]any, 0, rowCapacity)
	for row := range dataCh {
		rows = append(rows, row)
	}
	result.Rows = rows
	result.Size = size
	result.Total = total
	result.Option = option
	return result
}

func (i *Instance) validateRawBatchMembers(ctx context.Context, members []RawBatchMember) (Connect, error) {
	if len(members) == 0 {
		return Connect{}, &RawBatchTransportError{Kind: "nil_batch"}
	}
	first := members[0].Prepared
	if !rawBatchPreparedComplete(first) {
		return Connect{}, &RawBatchTransportError{Kind: "incomplete_member"}
	}
	connect := first.queryOption.conn
	connectionKey := first.connectionKey
	if connectionKey != i.RawBatchConnectionKey(ctx) {
		return Connect{}, &RawBatchTransportError{Kind: "mixed_connection"}
	}
	for _, member := range members[1:] {
		if !rawBatchPreparedComplete(member.Prepared) {
			return Connect{}, &RawBatchTransportError{Kind: "incomplete_member"}
		}
		if member.Prepared.queryOption.conn != connect ||
			member.Prepared.connectionKey != connectionKey {
			return Connect{}, &RawBatchTransportError{Kind: "mixed_connection"}
		}
	}
	return connect, nil
}

func rawBatchPreparedComplete(prepared *PreparedRawQuery) bool {
	return prepared != nil &&
		prepared.query != nil &&
		prepared.queryOption != nil &&
		prepared.fact != nil &&
		prepared.source != nil &&
		prepared.body != ""
}

func rawBatchChildError(result *elastic.SearchResult) error {
	if result == nil {
		return &RawBatchChildError{Type: "nil_response"}
	}
	if result.Error != nil {
		return &RawBatchChildError{
			Status: result.Status,
			Type:   sanitizeRawBatchErrorType(result.Error.Type),
		}
	}
	if result.Status >= http.StatusBadRequest {
		return &RawBatchChildError{
			Status: result.Status,
			Type:   "http_status",
		}
	}
	if result.Shards != nil && (result.Shards.Failed > 0 || len(result.Shards.Failures) > 0) {
		return &RawBatchChildError{
			Status: result.Status,
			Type:   "shard_failure",
		}
	}
	return nil
}

func sanitizeRawBatchErrorType(value string) string {
	switch value {
	case "authorization_exception",
		"circuit_breaking_exception",
		"illegal_argument_exception",
		"index_not_found_exception",
		"parsing_exception",
		"query_shard_exception",
		"resource_not_found_exception",
		"search_context_missing_exception",
		"search_phase_execution_exception",
		"security_exception",
		"timeout_exception",
		"too_many_requests_exception",
		"x_content_parse_exception":
		return value
	default:
		return "unknown"
	}
}

func newRawBatchTransportError(kind string, status int, cause error) *RawBatchTransportError {
	if status == 0 {
		var elasticErr *elastic.Error
		if errors.As(cause, &elasticErr) {
			status = elasticErr.Status
		}
	}
	return &RawBatchTransportError{
		Status: status,
		Kind:   kind,
	}
}
