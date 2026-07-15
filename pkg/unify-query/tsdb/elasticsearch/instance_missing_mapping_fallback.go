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
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	elastic "github.com/olivere/elastic/v7"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	esShardFailureSampleLimit     = 3
	esShardFailureReasonMaxLength = 512
	esMissingMappingReasonPrefix  = "No mapping found for ["
	esMissingMappingReasonSuffix  = "] in order to sort on"
)

type esShardFailureSample struct {
	Shard  int    `json:"shard"`
	Index  string `json:"index"`
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type esMissingMappingFailure struct {
	Index string
	Field string
}

func recordESQueryShards(ctx context.Context, span *trace.Span, qo *queryOption, res *elastic.SearchResult) {
	recordESQueryShardsWithPrefix(ctx, span, qo, res, "")
}

// recordESQueryShardsWithPrefix 将首次查询和 fallback retry 的分片属性分开记录。
// 首次响应可能有分片失败，而 retry 随后成功；如果继续写同一组 key，trace 会把
// 健康的分片计数和首次失败样本混在一起。
func recordESQueryShardsWithPrefix(ctx context.Context, span *trace.Span, qo *queryOption, res *elastic.SearchResult, prefix string) {
	if res == nil {
		return
	}

	span.Set(prefix+"timed_out", res.TimedOut)
	if res.Shards == nil {
		if res.TimedOut {
			span.Set(prefix+"shards_failures_count", 0)
			log.Warnf(ctx, "es query shard abnormal index: %+v, timed_out: %v, shards info is nil", esQueryIndexes(qo), res.TimedOut)
		}
		return
	}

	span.Set(prefix+"shards_total", res.Shards.Total)
	span.Set(prefix+"shards_successful", res.Shards.Successful)
	span.Set(prefix+"shards_failed", res.Shards.Failed)
	span.Set(prefix+"shards_skipped", res.Shards.Skipped)

	if res.Shards.Failed <= 0 && !res.TimedOut {
		return
	}

	failuresCount := countESShardFailures(res.Shards.Failures)
	span.Set(prefix+"shards_failures_count", failuresCount)
	failuresSample := buildESShardFailureSample(res.Shards.Failures)
	failuresSampleJson := marshalESShardFailureSample(failuresSample)
	if len(failuresSample) > 0 {
		span.Set(prefix+"shards_failures_sample", failuresSampleJson)
	}

	log.Warnf(
		ctx,
		"es query shard abnormal index: %+v, timed_out: %v, shards_total: %d, shards_successful: %d, shards_failed: %d, shards_skipped: %d, failures_count: %d, failures_sample: %s",
		esQueryIndexes(qo), res.TimedOut, res.Shards.Total, res.Shards.Successful, res.Shards.Failed, res.Shards.Skipped, failuresCount, failuresSampleJson,
	)
}

// tryFallbackEmptyMissingMappingIndexes 处理按新增字段排序时，只有旧空索引缺
// mapping 导致 ES 查询失败的场景。这个 fallback 刻意保持较窄的适用范围：只重试
// 普通 search 请求，先在原始 alias/pattern 语义下证明失败索引为空，再排除这些
// 失败物理索引重试一次。这个流程依赖失败索引是历史只读索引：空检查和 retry
// 之间如果仍有新文档写入失败索引，retry 排除该索引会带来静默漏查风险。
func (i *Instance) tryFallbackEmptyMissingMappingIndexes(
	ctx context.Context,
	span *trace.Span,
	client *elastic.Client,
	qo *queryOption,
	source *elastic.SearchSource,
	countQuery elastic.Query,
	queryErr error,
	res *elastic.SearchResult,
) (*elastic.SearchResult, []string, bool) {
	if client == nil || qo == nil || qo.query == nil || source == nil || countQuery == nil {
		return nil, nil, false
	}
	if !canFallbackMissingMappingQuery(qo.query) {
		return nil, nil, false
	}

	failures := missingMappingSortFailures(queryErr, res)
	if len(failures) == 0 {
		return nil, nil, false
	}

	field := failures[0].Field
	failedIndexes := dedupeMissingMappingFailureIndexes(failures)
	span.Set("fallback_reason", "missing_mapping_empty_index")
	span.Set("fallback_field", field)
	span.Set("fallback_failed_indexes", failedIndexes)

	indices, err := client.IndexGet(qo.indexes...).Do(ctx)
	if err != nil {
		span.Set("fallback_error", fmt.Sprintf("index_get: %v", err))
		return nil, nil, false
	}

	physicalIndexes := make([]string, 0, len(indices))
	for index := range indices {
		physicalIndexes = append(physicalIndexes, index)
	}
	sort.Strings(physicalIndexes)
	if len(physicalIndexes) == 0 {
		span.Set("fallback_error", "empty_resolved_indexes")
		return nil, nil, false
	}

	remainingPhysicalIndexes := excludeStrings(physicalIndexes, failedIndexes)
	if len(remainingPhysicalIndexes) == 0 {
		// 所有解析出的物理索引都失败时保留原错误。此时没有健康分片结果可保留，
		// 如果重试一个空 target，反而可能掩盖真实的 mapping 问题。
		span.Set("fallback_error", "empty_retry_indexes")
		return nil, nil, false
	}

	// 空检查必须通过原始 alias/pattern target，才能继续保留 alias filter 和
	// search_routing。通过 _index terms 把请求收窄到失败索引，避免在 alias 指向
	// 大量健康历史索引时让 URL 随健康索引数量线性增长。
	// ES 文档：alias filter/search_routing 会影响 alias 查询语义；
	// https://www.elastic.co/guide/en/elasticsearch/reference/7.17/aliases.html
	checkIndexes := append([]string{}, qo.indexes...)
	checkQuery := filterQueryByIndexes(countQuery, failedIndexes)
	matchedDocs, checkErr := searchExactTotalHits(ctx, client, checkIndexes, checkQuery)
	if checkErr != nil {
		span.Set("fallback_error", fmt.Sprintf("empty_check: %v", checkErr))
		return nil, nil, false
	}
	if matchedDocs != 0 {
		span.Set("fallback_error", fmt.Sprintf("non_empty_indexes: count=%d", matchedDocs))
		return nil, nil, false
	}

	// retry 同样保留原始 target，以延续 alias/routing 语义；只排除实际报告缺
	// mapping 的物理索引。ES multi-target 文档说明可以用 "-index" 排除 target，
	// 且 alias 场景应排除具体物理索引；
	// https://www.elastic.co/guide/en/elasticsearch/reference/7.17/multi-index.html
	retryIndexes := appendExcludedIndexExpressions(qo.indexes, failedIndexes)
	span.Set("fallback_retry_indexes", retryIndexes)

	log.Warnf(
		ctx,
		"es missing mapping fallback triggered field: %s, failed_indexes: %+v, retry_indexes: %+v, matched_docs: %d",
		field, failedIndexes, retryIndexes, matchedDocs,
	)

	retryRes, retryErr := client.Search().Index(retryIndexes...).SearchSource(source).Do(ctx)
	if retryErr != nil {
		span.Set("fallback_error", fmt.Sprintf("retry: %v", retryErr))
		return nil, nil, false
	}
	return retryRes, retryIndexes, true
}

// searchExactTotalHits 使用 size:0 search，而不是 CountService。调用方必须先
// 检查超时、分片失败和 total-hits relation，才能把索引当成空索引；CountService
// 只返回 count 值。
func searchExactTotalHits(ctx context.Context, client *elastic.Client, indexes []string, query elastic.Query) (int64, error) {
	res, err := client.Search().
		Index(indexes...).
		Query(query).
		Size(0).
		TrackTotalHits(true).
		Do(ctx)
	if err != nil {
		return 0, err
	}
	if res == nil {
		return 0, fmt.Errorf("empty response")
	}
	if res.TimedOut {
		return 0, fmt.Errorf("timed out")
	}
	if res.Shards == nil {
		return 0, fmt.Errorf("shards info is nil")
	}
	if res.Shards.Failed != 0 {
		return 0, fmt.Errorf(
			"shards failed: total=%d successful=%d failed=%d",
			res.Shards.Total, res.Shards.Successful, res.Shards.Failed,
		)
	}
	if res.Hits == nil || res.Hits.TotalHits == nil {
		return 0, fmt.Errorf("total hits is nil")
	}
	if res.Hits.TotalHits.Relation != "eq" {
		return 0, fmt.Errorf("total hits is not exact: relation=%s value=%d", res.Hits.TotalHits.Relation, res.Hits.TotalHits.Value)
	}
	return res.Hits.TotalHits.Value, nil
}

func filterQueryByIndexes(query elastic.Query, indexes []string) elastic.Query {
	return elastic.NewBoolQuery().Filter(
		query,
		elastic.NewTermsQueryFromStrings("_index", indexes...),
	)
}

// canFallbackMissingMappingQuery 避免重试游标类请求，因为它们的翻页状态绑定在
// 原始分片集合上。
func canFallbackMissingMappingQuery(query *metadata.Query) bool {
	if query == nil {
		return false
	}
	if query.Scroll != "" {
		return false
	}
	if query.ResultTableOption == nil {
		return true
	}
	if query.ResultTableOption.ScrollID != "" {
		return false
	}
	return len(query.ResultTableOption.SearchAfter) == 0
}

// missingMappingSortFailures 统一提取 SearchResult 和 elastic.Error 中的缺
// mapping 分片失败。ES 会根据响应状态，把相同的 failed shard details 放在不同路径。
func missingMappingSortFailures(err error, res *elastic.SearchResult) []esMissingMappingFailure {
	shardFailures, extractedErr := extractESResult(err, res)
	failures := make([]esMissingMappingFailure, 0, len(shardFailures))
	for _, failure := range shardFailures {
		if failure == nil || failure.Index == "" || failure.Reason == nil {
			continue
		}
		failures = appendMissingMappingFailure(failures, failure.Index, failure.Reason)
	}

	var esErr *elastic.Error
	if errors.As(extractedErr, &esErr) && esErr != nil && esErr.Details != nil {
		for _, failedShard := range esErr.Details.FailedShards {
			index, _ := failedShard[IndexField].(string)
			reason, _ := failedShard[ReasonField].(map[string]any)
			failures = appendMissingMappingFailure(failures, index, reason)
		}
	}
	return failures
}

func appendMissingMappingFailure(failures []esMissingMappingFailure, index string, reason map[string]any) []esMissingMappingFailure {
	if index == "" || reason == nil {
		return failures
	}
	reasonMsg, _ := extractReasonAndType(reason, true)
	if reasonMsg == "" {
		reasonMsg, _ = extractReasonAndType(reason, false)
	}
	field, ok := missingMappingSortField(reasonMsg)
	if !ok {
		return failures
	}
	return append(failures, esMissingMappingFailure{
		Index: index,
		Field: field,
	})
}

func missingMappingSortField(reason string) (string, bool) {
	if !strings.HasPrefix(reason, esMissingMappingReasonPrefix) || !strings.HasSuffix(reason, esMissingMappingReasonSuffix) {
		return "", false
	}
	field := strings.TrimSuffix(strings.TrimPrefix(reason, esMissingMappingReasonPrefix), esMissingMappingReasonSuffix)
	if field == "" {
		return "", false
	}
	return field, true
}

func dedupeMissingMappingFailureIndexes(failures []esMissingMappingFailure) []string {
	indexSet := make(map[string]struct{}, len(failures))
	for _, failure := range failures {
		if failure.Index == "" {
			continue
		}
		indexSet[failure.Index] = struct{}{}
	}
	return sortedStringSetKeys(indexSet)
}

func makeStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func excludeStrings(values []string, excluded []string) []string {
	excludedSet := makeStringSet(excluded)
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := excludedSet[value]; ok {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}

// appendExcludedIndexExpressions 依赖 Elasticsearch multi-target 语法：先保留
// 原始 alias 或 pattern，再追加具体的 "-index" 排除项。这样可以在移除已知坏物理
// 索引的同时保留 alias filter/routing。
// 参考：https://www.elastic.co/guide/en/elasticsearch/reference/7.17/multi-index.html
func appendExcludedIndexExpressions(indexes []string, excluded []string) []string {
	result := append([]string{}, indexes...)
	for _, index := range excluded {
		result = append(result, "-"+index)
	}
	return result
}

func sortedStringSetKeys(set map[string]struct{}) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func esQueryIndexes(qo *queryOption) []string {
	if qo == nil {
		return nil
	}
	return qo.indexes
}

func countESShardFailures(failures []*elastic.ShardOperationFailedException) int {
	count := 0
	for _, failure := range failures {
		if failure != nil {
			count++
		}
	}
	return count
}

func buildESShardFailureSample(failures []*elastic.ShardOperationFailedException) []esShardFailureSample {
	samples := make([]esShardFailureSample, 0, esShardFailureSampleLimit)
	for _, failure := range failures {
		if failure == nil {
			continue
		}
		samples = append(samples, esShardFailureSample{
			Shard:  failure.Shard,
			Index:  failure.Index,
			Status: failure.Status,
			Reason: truncateString(marshalESShardFailureReason(failure.Reason), esShardFailureReasonMaxLength),
		})
		if len(samples) >= esShardFailureSampleLimit {
			break
		}
	}
	return samples
}

func marshalESShardFailureReason(reason map[string]any) string {
	if len(reason) == 0 {
		return ""
	}
	reasonJson, err := json.Marshal(reason)
	if err != nil {
		return fmt.Sprintf("%+v", reason)
	}
	return string(reasonJson)
}

func marshalESShardFailureSample(failuresSample []esShardFailureSample) string {
	failuresJson, err := json.Marshal(failuresSample)
	if err != nil {
		return fmt.Sprintf("%+v", failuresSample)
	}
	return string(failuresJson)
}

func truncateString(s string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLength {
		return s
	}
	return string(runes[:maxLength])
}
