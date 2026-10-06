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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"

	"linkd/internal/store"
)

const maxArchiveFailureSamples = 10

// ArchiveBatchRequest 定义一次有界终态 Alert 扫描及并发归档请求。
// AfterAlertID 是上一批最后一个已扫描的 alert_id；失败项也会推进游标，避免毒数据阻塞本轮后续对象。
type ArchiveBatchRequest struct {
	// Limit 是本次 ES 搜索最多返回的终态 Alert 数。
	Limit int
	// WorkerCount 是本批最多并发执行的 Bulk Worker 数。
	WorkerCount int
	// AfterAlertID 是同一轮稳定扫描的上一页游标。
	AfterAlertID string
}

// ArchiveFailure 是可安全写入日志的有限失败摘要，不包含 Alert payload。
type ArchiveFailure struct {
	// Index 和 DocumentID 标识失败的 Active 物理副本。
	Index      string
	DocumentID string
	// Stage 和 Code 是低基数失败分类，Reason 只提供不含 payload 的错误上下文。
	Stage  string
	Code   string
	Reason string
}

// ArchiveBatchResult 描述一次扫描批次的逐项收敛结果。
type ArchiveBatchResult struct {
	// Scanned 是本批从 Active 索引读取的终态 Alert 数。
	Scanned int
	// Archived 是已确认写入 History 且完成 Active 条件删除的数量。
	Archived int
	// Failed 是保留在 Active 等待后续重试的数量。
	Failed int
	// NextCursor 非空表示同一轮扫描还有后续页。
	NextCursor string
	// FailureItems 只保留有限样本，不承载完整 Alert payload。
	FailureItems []ArchiveFailure
}

type archiveItemResult struct {
	index      string
	documentID string
	archived   bool
	stage      string
	err        error
}

type preparedArchiveItem struct {
	terminal       store.StoredAlert
	activeVersion  versionPayload
	historyTarget  string
	historyID      string
	requireAlias   bool
	body           []byte
	createMetadata []byte
}

type bulkDeleteResponse struct {
	Items []struct {
		Delete struct {
			Index  string `json:"_index"`
			ID     string `json:"_id"`
			Status int    `json:"status"`
			Error  *struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"error"`
		} `json:"delete"`
	} `json:"items"`
}

type archiveMultiGetResponse struct {
	Docs []struct {
		Index       string          `json:"_index"`
		ID          string          `json:"_id"`
		SeqNo       int64           `json:"_seq_no"`
		PrimaryTerm int64           `json:"_primary_term"`
		Found       bool            `json:"found"`
		Source      json.RawMessage `json:"_source"`
		Error       json.RawMessage `json:"error"`
	} `json:"docs"`
}

// archiveTerminalAlertsBulk 先批量 create History，再只批量删除已确认存在于 History 的 Active 副本。
// 返回值与输入位置一一对应；单项失败不会阻止同一批中的其他 Alert 收敛。
func (r *Repository) archiveTerminalAlertsBulk(
	ctx context.Context,
	terminals []store.StoredAlert,
) []archiveItemResult {
	results := make([]archiveItemResult, len(terminals))
	prepared := make([]*preparedArchiveItem, len(terminals))
	groups := map[bool][]int{false: {}, true: {}}
	for index, terminal := range terminals {
		results[index].documentID = alertDocumentID(terminal.Alert)
		if version, ok := decodeVersion(terminal.Version); ok {
			results[index].index = version.Index
		}
		item, err := r.prepareArchiveItem(ctx, terminal)
		if err != nil {
			results[index].stage, results[index].err = "prepare", err
			continue
		}
		prepared[index] = &item
		results[index].index = item.activeVersion.Index
		results[index].documentID = item.activeVersion.DocumentID
		groups[item.requireAlias] = append(groups[item.requireAlias], index)
	}

	for _, requireAlias := range []bool{false, true} {
		for _, indices := range r.splitArchiveCreateChunks(prepared, groups[requireAlias]) {
			r.archivePreparedChunk(ctx, prepared, indices, requireAlias, results)
		}
	}
	return results
}

func (r *Repository) prepareArchiveItem(
	ctx context.Context,
	terminal store.StoredAlert,
) (preparedArchiveItem, error) {
	if err := contextError(ctx); err != nil {
		return preparedArchiveItem{}, err
	}
	if !terminal.Alert.Status.Terminal() {
		return preparedArchiveItem{}, fmt.Errorf("archive alert: alert is not terminal")
	}
	activeVersion, ok := decodeVersion(terminal.Version)
	if !ok {
		return preparedArchiveItem{}, fmt.Errorf("archive alert: active version is invalid")
	}
	route, err := r.router.AlertHistoryRoute(ctx, terminal.Alert.AlertID)
	if err != nil {
		return preparedArchiveItem{}, fmt.Errorf("route alert history: %w", err)
	}
	route, err = normalizeRoute(route, r.config.MaxReadTargets)
	if err != nil {
		return preparedArchiveItem{}, err
	}
	body, err := encodeAlertDocument(terminal.Alert)
	if err != nil {
		return preparedArchiveItem{}, err
	}
	if len(body) > r.config.MaxDocumentBytes {
		return preparedArchiveItem{}, fmt.Errorf("alert document exceeds %d bytes", r.config.MaxDocumentBytes)
	}
	historyID := alertDocumentID(terminal.Alert)
	metadata, err := json.Marshal(map[string]any{"create": map[string]string{
		"_index": route.WriteTarget,
		"_id":    historyID,
	}})
	if err != nil {
		return preparedArchiveItem{}, fmt.Errorf("encode alert archive bulk metadata: %w", err)
	}
	if len(metadata)+len(body)+2 > r.config.MaxRequestBytes {
		return preparedArchiveItem{}, fmt.Errorf("alert archive item exceeds %d request bytes", r.config.MaxRequestBytes)
	}
	return preparedArchiveItem{
		terminal: terminal, activeVersion: activeVersion, historyTarget: route.WriteTarget,
		historyID: historyID, requireAlias: route.RequireAlias, body: body, createMetadata: metadata,
	}, nil
}

func (r *Repository) splitArchiveCreateChunks(
	prepared []*preparedArchiveItem,
	indices []int,
) [][]int {
	chunks := make([][]int, 0, (len(indices)+store.MaxBatchSize-1)/store.MaxBatchSize)
	current := make([]int, 0, min(len(indices), store.MaxBatchSize))
	currentBytes := 0
	for _, index := range indices {
		item := prepared[index]
		itemBytes := len(item.createMetadata) + len(item.body) + 2
		if len(current) > 0 && (len(current) == store.MaxBatchSize || currentBytes+itemBytes > r.config.MaxRequestBytes) {
			chunks = append(chunks, current)
			current = make([]int, 0, min(len(indices), store.MaxBatchSize))
			currentBytes = 0
		}
		current = append(current, index)
		currentBytes += itemBytes
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

func (r *Repository) archivePreparedChunk(
	ctx context.Context,
	prepared []*preparedArchiveItem,
	indices []int,
	requireAlias bool,
	results []archiveItemResult,
) {
	var body bytes.Buffer
	for _, index := range indices {
		item := prepared[index]
		body.Write(item.createMetadata)
		body.WriteByte('\n')
		body.Write(item.body)
		body.WriteByte('\n')
	}
	query := url.Values{"refresh": []string{"wait_for"}}
	if requireAlias {
		query.Set("require_alias", "true")
	}
	var response bulkCreateResponse
	if err := r.performNDJSON(ctx, http.MethodPost, "/_bulk", query, body.Bytes(), &response); err != nil {
		setArchiveChunkError(indices, results, "history_create", fmt.Errorf("bulk create alert history: %w", err))
		return
	}
	if len(response.Items) != len(indices) {
		setArchiveChunkError(indices, results, "history_create", fmt.Errorf(
			"elasticsearch alert history bulk returned %d items for %d alerts",
			len(response.Items), len(indices),
		))
		return
	}

	eligible := make([]int, 0, len(indices))
	conflicts := make([]int, 0)
	for responseIndex, resultIndex := range indices {
		item := prepared[resultIndex]
		created := response.Items[responseIndex].Create
		switch created.Status {
		case http.StatusCreated:
			_, err := storedAlertFromIndexResponse(item.terminal.Alert, item.historyID, indexResponse{
				Index: created.Index, ID: created.ID, SeqNo: created.SeqNo, PrimaryTerm: created.PrimaryTerm,
			})
			if err != nil {
				results[resultIndex].stage, results[resultIndex].err = "history_create", err
				continue
			}
			eligible = append(eligible, resultIndex)
		case http.StatusConflict:
			conflicts = append(conflicts, resultIndex)
		default:
			results[resultIndex].stage = "history_create"
			results[resultIndex].err = archiveBulkItemError(
				"create alert history", item.terminal.Alert.AlertID, created.Status,
				created.Error,
			)
		}
	}
	eligible = append(eligible, r.verifyArchiveConflicts(ctx, prepared, conflicts, results)...)
	if len(eligible) != 0 {
		r.deleteArchivedActiveAlerts(ctx, prepared, eligible, results)
	}
}

func (r *Repository) verifyArchiveConflicts(
	ctx context.Context,
	prepared []*preparedArchiveItem,
	indices []int,
	results []archiveItemResult,
) []int {
	if len(indices) == 0 {
		return nil
	}
	documents := make([]map[string]string, 0, len(indices))
	for _, index := range indices {
		item := prepared[index]
		documents = append(documents, map[string]string{"_index": item.historyTarget, "_id": item.historyID})
	}
	body, err := marshalRequest(map[string]any{"docs": documents})
	if err != nil {
		setArchiveChunkError(indices, results, "history_verify", err)
		return nil
	}
	var response archiveMultiGetResponse
	if err := r.performJSON(ctx, http.MethodPost, "/_mget", nil, body, &response); err != nil {
		setArchiveChunkError(indices, results, "history_verify", fmt.Errorf("bulk read duplicate alert history: %w", err))
		return nil
	}
	if len(response.Docs) != len(indices) {
		setArchiveChunkError(indices, results, "history_verify", fmt.Errorf(
			"elasticsearch alert history mget returned %d items for %d alerts",
			len(response.Docs), len(indices),
		))
		return nil
	}
	verified := make([]int, 0, len(indices))
	for responseIndex, resultIndex := range indices {
		item := prepared[resultIndex]
		document := response.Docs[responseIndex]
		if len(document.Error) != 0 && string(document.Error) != "null" {
			results[resultIndex].stage = "history_verify"
			results[resultIndex].err = fmt.Errorf("read duplicate alert history returned an item error")
			continue
		}
		if !document.Found {
			results[resultIndex].stage = "history_verify"
			results[resultIndex].err = fmt.Errorf("duplicate alert history is missing")
			continue
		}
		stored, err := decodeAlertHit(searchHit{
			Index: document.Index, ID: document.ID, SeqNo: document.SeqNo,
			PrimaryTerm: document.PrimaryTerm, Source: document.Source,
		})
		if err != nil {
			results[resultIndex].stage, results[resultIndex].err = "history_verify", err
			continue
		}
		if !reflect.DeepEqual(stored.Alert, item.terminal.Alert) {
			results[resultIndex].stage = "history_verify"
			results[resultIndex].err = fmt.Errorf("%w: alert history contains different content", store.ErrIdentityConflict)
			continue
		}
		verified = append(verified, resultIndex)
	}
	return verified
}

func (r *Repository) deleteArchivedActiveAlerts(
	ctx context.Context,
	prepared []*preparedArchiveItem,
	indices []int,
	results []archiveItemResult,
) {
	var body bytes.Buffer
	deleteIndices := make([]int, 0, len(indices))
	for _, index := range indices {
		version := prepared[index].activeVersion
		metadata, err := json.Marshal(map[string]any{"delete": map[string]any{
			"_index": version.Index, "_id": version.DocumentID,
			"if_seq_no": version.SeqNo, "if_primary_term": version.PrimaryTerm,
		}})
		if err != nil {
			results[index].stage, results[index].err = "active_delete", err
			continue
		}
		body.Write(metadata)
		body.WriteByte('\n')
		deleteIndices = append(deleteIndices, index)
	}
	if len(deleteIndices) == 0 {
		return
	}
	query := url.Values{"refresh": []string{"wait_for"}}
	var response bulkDeleteResponse
	if err := r.performNDJSON(ctx, http.MethodPost, "/_bulk", query, body.Bytes(), &response); err != nil {
		setArchiveChunkError(deleteIndices, results, "active_delete", fmt.Errorf("bulk delete archived active alerts: %w", err))
		return
	}
	if len(response.Items) != len(deleteIndices) {
		setArchiveChunkError(deleteIndices, results, "active_delete", fmt.Errorf(
			"elasticsearch active alert delete bulk returned %d items for %d alerts",
			len(response.Items), len(deleteIndices),
		))
		return
	}
	for responseIndex, resultIndex := range deleteIndices {
		item := prepared[resultIndex]
		deleted := response.Items[responseIndex].Delete
		if deleted.Status == http.StatusOK || deleted.Status == http.StatusNotFound {
			if (deleted.ID != "" && deleted.ID != item.activeVersion.DocumentID) ||
				(deleted.Index != "" && deleted.Index != item.activeVersion.Index) {
				results[resultIndex].stage = "active_delete"
				results[resultIndex].err = fmt.Errorf("elasticsearch active delete returned an unexpected identity")
				continue
			}
			results[resultIndex].archived = true
			continue
		}
		results[resultIndex].stage = "active_delete"
		results[resultIndex].err = archiveBulkItemError(
			"delete archived active alert", item.terminal.Alert.AlertID, deleted.Status,
			deleted.Error,
		)
	}
}

func setArchiveChunkError(indices []int, results []archiveItemResult, stage string, err error) {
	for _, index := range indices {
		if results[index].err == nil {
			results[index].stage = stage
			results[index].err = err
		}
	}
}

func archiveBulkItemError(
	operation, alertID string,
	status int,
	detail *struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	},
) error {
	if detail == nil {
		return fmt.Errorf("elasticsearch bulk %s %q returned status %d", operation, alertID, status)
	}
	return fmt.Errorf(
		"elasticsearch bulk %s %q returned status %d: %s",
		operation, alertID, status, detail.Type,
	)
}
