// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ManagerConfig 定义时间桶管理操作的预创建范围和资源上限。
type ManagerConfig struct {
	PrecreatePastBuckets   int
	PrecreateFutureBuckets int
	MaxBucketsPerEntity    int
}

// Manager 提供 Elasticsearch Schema 与 Active 资源对账、时间桶维护和终态 Alert 归档的单轮管理操作。
// 执行周期和任务监督由控制面决定，Manager 自身不启动后台 goroutine。
type Manager struct {
	repository *Repository
	router     *BucketRouter
	config     ManagerConfig
	now        func() time.Time
}

// NewManager 创建索引管理器；构造过程不访问 Elasticsearch。
func NewManager(repository *Repository, router *BucketRouter, config ManagerConfig) (*Manager, error) {
	return newManager(repository, router, config, time.Now)
}

func newManager(
	repository *Repository,
	router *BucketRouter,
	config ManagerConfig,
	now func() time.Time,
) (*Manager, error) {
	if repository == nil || router == nil {
		return nil, fmt.Errorf("create elasticsearch manager: repository and router are required")
	}
	if config.PrecreatePastBuckets < 0 || config.PrecreateFutureBuckets < 1 ||
		config.MaxBucketsPerEntity < 2 {
		return nil, fmt.Errorf("create elasticsearch manager: invalid resource limits")
	}
	if now == nil {
		return nil, fmt.Errorf("create elasticsearch manager: clock is required")
	}
	return &Manager{repository: repository, router: router, config: config, now: now}, nil
}

// ReconcileSchemaAndActive 幂等收敛模板、Active Alert 索引和静态 alias。
// 时间桶由 ReconcileBuckets 独立维护，不属于本操作的成功条件。
func (m *Manager) ReconcileSchemaAndActive(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("reconcile elasticsearch resources: context must not be nil")
	}
	if err := m.repository.EnsureSchema(ctx, m.router.SchemaConfig()); err != nil {
		return fmt.Errorf("reconcile elasticsearch schema: %w", err)
	}
	if err := m.ensureActiveAlert(ctx); err != nil {
		return err
	}
	return nil
}

// ReconcileBuckets 幂等维护当前预创建窗口内的时间桶及其 alias。
// 模板必须先由 ReconcileSchemaAndActive 准备；控制面启动时按该顺序完成首次对账。
func (m *Manager) ReconcileBuckets(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("reconcile elasticsearch buckets: context must not be nil")
	}
	now := m.now().Round(0).UTC()
	if err := m.ensureWindow(ctx, now); err != nil {
		return err
	}
	return nil
}

// PrepareRange 显式创建覆盖闭区间的 Event、AlertHistory 和 AlertLog 桶，供历史回放前使用。
func (m *Manager) PrepareRange(ctx context.Context, from, to time.Time) error {
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return fmt.Errorf("prepare elasticsearch buckets: valid from/to are required")
	}
	from, to = from.UTC(), to.UTC()
	for _, family := range m.bucketFamilies() {
		for start := bucketStart(from, family.days); !start.After(to); start = start.AddDate(0, 0, family.days) {
			if err := m.ensureBucket(ctx, family, start); err != nil {
				return err
			}
		}
	}
	return nil
}

// VerifyReady 验证数据面启动所需的模板、Active alias 和当前时间桶已经存在。
func (m *Manager) VerifyReady(ctx context.Context) error {
	for _, alias := range []string{
		m.router.alertReadAlias(), m.router.activeAlertAlias(), m.router.activeAlertWriteAlias(),
		m.router.eventReadAlias(), m.router.alertHistoryReadAlias(), m.router.alertLogReadAlias(),
	} {
		indices, err := m.aliasIndices(ctx, alias)
		if err != nil {
			return fmt.Errorf("verify elasticsearch alias %q: %w", alias, err)
		}
		if len(indices) == 0 {
			return fmt.Errorf("elasticsearch alias %q is missing; start linkd run control-plane or linkd run all-in-one first", alias)
		}
	}
	now := m.now()
	for alias, expectedIndex := range map[string]string{
		m.router.activeAlertWriteAlias():     m.router.activeAlertIndex(),
		m.router.eventWriteAlias(now):        m.router.eventIndex(now),
		m.router.alertHistoryWriteAlias(now): m.router.alertHistoryIndex(now),
		m.router.alertLogWriteAlias(now):     m.router.alertLogIndex(now),
	} {
		members, err := m.aliasMembers(ctx, alias)
		if err != nil {
			return fmt.Errorf("verify elasticsearch write alias %q: %w", alias, err)
		}
		if len(members) != 1 || !members[expectedIndex] {
			return fmt.Errorf("elasticsearch write alias %q must point to %q with is_write_index=true", alias, expectedIndex)
		}
	}
	return nil
}

type bucketFamily struct {
	entity     string
	role       string
	days       int
	readAlias  string
	index      func(time.Time) string
	writeAlias func(time.Time) string
}

func (m *Manager) bucketFamilies() []bucketFamily {
	return []bucketFamily{
		{entity: entityEvent, role: "bucket", days: m.router.eventBucketDays, readAlias: m.router.eventReadAlias(), index: m.router.eventIndex, writeAlias: m.router.eventWriteAlias},
		{entity: entityAlertHistory, role: "history", days: m.router.alertBucketDays, readAlias: m.router.alertHistoryReadAlias(), index: m.router.alertHistoryIndex, writeAlias: m.router.alertHistoryWriteAlias},
		{entity: entityAlertLog, role: "bucket", days: m.router.alertLogBucketDays, readAlias: m.router.alertLogReadAlias(), index: m.router.alertLogIndex, writeAlias: m.router.alertLogWriteAlias},
	}
}

func (m *Manager) ensureWindow(ctx context.Context, now time.Time) error {
	for _, family := range m.bucketFamilies() {
		current := bucketStart(now, family.days)
		for offset := -m.config.PrecreatePastBuckets; offset <= m.config.PrecreateFutureBuckets; offset++ {
			start := current.AddDate(0, 0, offset*family.days)
			if err := m.ensureBucket(ctx, family, start); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) ensureActiveAlert(ctx context.Context) error {
	metadata := schemaMetadata{
		ManagedBy: managedByLinkd, Entity: entityAlert, Role: "active", SchemaVersion: currentSchemaVersion,
	}
	index := m.router.activeAlertIndex()
	if err := m.ensureManagedIndex(ctx, index, metadata); err != nil {
		return fmt.Errorf("ensure active alert index: %w", err)
	}
	if err := m.ensureAlias(ctx, m.router.alertReadAlias(), index, false, false); err != nil {
		return err
	}
	if err := m.ensureAlias(ctx, m.router.activeAlertAlias(), index, false, true); err != nil {
		return err
	}
	return m.ensureAlias(ctx, m.router.activeAlertWriteAlias(), index, true, true)
}

func (m *Manager) ensureBucket(ctx context.Context, family bucketFamily, start time.Time) error {
	start = bucketStart(start, family.days)
	metadata := schemaMetadata{
		ManagedBy: managedByLinkd, Entity: family.entity, Role: family.role,
		SchemaVersion: currentSchemaVersion, BucketDays: family.days,
		BucketStart: start.Format(time.RFC3339), BucketEnd: start.AddDate(0, 0, family.days).Format(time.RFC3339),
	}
	index := family.index(start)
	if err := m.ensureManagedIndex(ctx, index, metadata); err != nil {
		return fmt.Errorf("ensure %s bucket %q: %w", family.entity, index, err)
	}
	indices, err := m.aliasIndices(ctx, family.readAlias)
	if err != nil {
		return err
	}
	if !containsString(indices, index) && len(indices) >= m.config.MaxBucketsPerEntity {
		return fmt.Errorf("elasticsearch alias %q reached max bucket count %d", family.readAlias, m.config.MaxBucketsPerEntity)
	}
	if err := m.ensureAlias(ctx, family.readAlias, index, false, false); err != nil {
		return err
	}
	if family.entity == entityAlertHistory {
		if err := m.ensureAlias(ctx, m.router.alertReadAlias(), index, false, false); err != nil {
			return err
		}
	}
	return m.ensureAlias(ctx, family.writeAlias(start), index, true, true)
}

func (m *Manager) ensureManagedIndex(ctx context.Context, index string, metadata schemaMetadata) error {
	body, err := marshalRequest(map[string]any{"mappings": map[string]any{"_meta": metadata}})
	if err != nil {
		return err
	}
	err = m.repository.performJSON(ctx, http.MethodPut, "/"+index, nil, body, nil)
	if err != nil {
		var responseErr *responseError
		if !errors.As(err, &responseErr) || responseErr.Type != "resource_already_exists_exception" {
			return fmt.Errorf("ensure elasticsearch index %q: %w", index, err)
		}
	}
	return m.verifyManagedIndex(ctx, index, metadata)
}

func (m *Manager) verifyManagedIndex(ctx context.Context, index string, expected schemaMetadata) error {
	var response map[string]struct {
		Mappings struct {
			Metadata schemaMetadata `json:"_meta"`
		} `json:"mappings"`
	}
	if err := m.repository.performJSON(ctx, http.MethodGet, "/"+index+"/_mapping", nil, nil, &response); err != nil {
		return err
	}
	item, ok := response[index]
	if !ok || len(response) != 1 || item.Mappings.Metadata != expected {
		return fmt.Errorf("elasticsearch index %q has incompatible managed metadata; stop Linkd, delete managed indices and aliases, then restart linkd run control-plane or linkd run all-in-one", index)
	}
	return nil
}

func (m *Manager) ensureAlias(ctx context.Context, alias, index string, write, exclusive bool) error {
	indices, err := m.aliasIndices(ctx, alias)
	if err != nil {
		return err
	}
	actions := make([]any, 0, len(indices)+1)
	if exclusive {
		for _, existing := range indices {
			if existing != index {
				actions = append(actions, map[string]any{"remove": map[string]any{"index": existing, "alias": alias}})
			}
		}
	}
	add := map[string]any{"index": index, "alias": alias}
	if write {
		add["is_write_index"] = true
	}
	actions = append(actions, map[string]any{"add": add})
	body, err := marshalRequest(map[string]any{"actions": actions})
	if err != nil {
		return err
	}
	if err := m.repository.performJSON(ctx, http.MethodPost, "/_aliases", nil, body, nil); err != nil {
		return fmt.Errorf("ensure elasticsearch alias %q: %w", alias, err)
	}
	return nil
}

func (m *Manager) aliasIndices(ctx context.Context, alias string) ([]string, error) {
	members, err := m.aliasMembers(ctx, alias)
	if err != nil {
		return nil, err
	}
	indices := make([]string, 0, len(members))
	for index := range members {
		indices = append(indices, index)
	}
	sort.Strings(indices)
	return indices, nil
}

func (m *Manager) aliasMembers(ctx context.Context, alias string) (map[string]bool, error) {
	var response map[string]struct {
		Aliases map[string]struct {
			IsWriteIndex bool `json:"is_write_index"`
		} `json:"aliases"`
	}
	err := m.repository.performJSON(
		ctx, http.MethodGet, "/_alias/"+url.PathEscape(alias),
		url.Values{"expand_wildcards": []string{"open"}}, nil, &response,
	)
	if isResponseStatus(err, http.StatusNotFound) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	members := make(map[string]bool, len(response))
	for index, item := range response {
		if properties, ok := item.Aliases[alias]; ok {
			members[index] = properties.IsWriteIndex
		}
	}
	return members, nil
}

// ArchiveTerminalAlerts 扫描一批 Active 索引中的终态 Alert，并用有界 Worker 并发归档。
// 本操作只消费 Bucket Manager 已准备的 History write alias，不隐式创建或修复时间桶。
func (m *Manager) ArchiveTerminalAlerts(
	ctx context.Context,
	request ArchiveBatchRequest,
) (ArchiveBatchResult, error) {
	if ctx == nil {
		return ArchiveBatchResult{}, fmt.Errorf("archive terminal alerts: context must not be nil")
	}
	if request.Limit < 1 || request.Limit > 10000 {
		return ArchiveBatchResult{}, fmt.Errorf("archive terminal alerts: limit must be between 1 and 10000")
	}
	if request.WorkerCount < 1 || request.WorkerCount > 64 || request.WorkerCount > request.Limit {
		return ArchiveBatchResult{}, fmt.Errorf("archive terminal alerts: worker count must be between 1 and min(64, limit)")
	}
	searchBody := map[string]any{
		"size":                request.Limit,
		"track_total_hits":    false,
		"seq_no_primary_term": true,
		"query": map[string]any{"bool": map[string]any{"must_not": []any{
			map[string]any{"term": map[string]any{"status": domain.AlertStatusActive}},
		}}},
		"sort": []any{map[string]any{"alert_id": map[string]any{"order": "asc"}}},
	}
	if request.AfterAlertID != "" {
		searchBody["search_after"] = []string{request.AfterAlertID}
	}
	body, err := marshalRequest(searchBody)
	if err != nil {
		return ArchiveBatchResult{}, err
	}
	var response searchResponse
	query := url.Values{"ignore_unavailable": []string{"true"}}
	if err := m.repository.performJSON(
		ctx, http.MethodPost, "/"+m.router.activeAlertAlias()+"/_search", query, body, &response,
	); err != nil {
		return ArchiveBatchResult{}, fmt.Errorf("scan terminal active alerts: %w", err)
	}
	result := ArchiveBatchResult{Scanned: len(response.Hits.Hits)}
	terminals := make([]store.StoredAlert, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		if len(hit.Sort) != 1 {
			return ArchiveBatchResult{}, fmt.Errorf("active alert terminal scan returned invalid sort values")
		}
		terminal, err := decodeAlertHit(hit)
		if err != nil {
			result.addFailure(ArchiveFailure{
				Index: hit.Index, DocumentID: hit.ID, Stage: "decode", Code: "decode_failed", Reason: err.Error(),
			})
			continue
		}
		if !terminal.Alert.Status.Terminal() {
			result.addFailure(ArchiveFailure{
				Index: hit.Index, DocumentID: hit.ID, Stage: "decode", Code: "non_terminal_document",
				Reason: fmt.Sprintf("active alert terminal scan returned non-terminal alert %q", terminal.Alert.AlertID),
			})
			continue
		}
		terminals = append(terminals, terminal)
	}
	for _, item := range m.archiveTerminalAlertsWithWorkers(ctx, terminals, request.WorkerCount) {
		if item.archived {
			result.Archived++
			continue
		}
		reason := "archive worker returned neither success nor error"
		if item.err != nil {
			reason = item.err.Error()
		}
		result.addFailure(ArchiveFailure{
			Index: item.index, DocumentID: item.documentID, Stage: item.stage,
			Code: item.stage + "_failed", Reason: reason,
		})
	}
	result.Failed = result.Scanned - result.Archived
	if result.Scanned == request.Limit {
		if err := json.Unmarshal(response.Hits.Hits[len(response.Hits.Hits)-1].Sort[0], &result.NextCursor); err != nil {
			return ArchiveBatchResult{}, fmt.Errorf("decode active alert archive cursor: %w", err)
		}
		if result.NextCursor == "" {
			return ArchiveBatchResult{}, fmt.Errorf("decode active alert archive cursor: empty alert_id")
		}
	}
	return result, nil
}

func (m *Manager) archiveTerminalAlertsWithWorkers(
	ctx context.Context,
	terminals []store.StoredAlert,
	workerCount int,
) []archiveItemResult {
	if len(terminals) == 0 {
		return nil
	}
	workerCount = min(workerCount, len(terminals))
	chunkSize := min((len(terminals)+workerCount-1)/workerCount, store.MaxBatchSize)
	jobCount := (len(terminals) + chunkSize - 1) / chunkSize
	jobs := make(chan []store.StoredAlert, jobCount)
	results := make(chan []archiveItemResult, jobCount)
	for start := 0; start < len(terminals); start += chunkSize {
		end := min(start+chunkSize, len(terminals))
		jobs <- terminals[start:end]
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for items := range jobs {
				results <- m.repository.archiveTerminalAlertsBulk(ctx, items)
			}
		}()
	}
	workers.Wait()
	close(results)

	merged := make([]archiveItemResult, 0, len(terminals))
	for items := range results {
		merged = append(merged, items...)
	}
	return merged
}

func (r *ArchiveBatchResult) addFailure(failure ArchiveFailure) {
	r.Failed++
	if len(r.FailureItems) < maxArchiveFailureSamples {
		r.FailureItems = append(r.FailureItems, failure)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
