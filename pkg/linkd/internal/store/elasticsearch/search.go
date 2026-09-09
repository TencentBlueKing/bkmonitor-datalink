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
	"time"
)

type getResponse struct {
	Index       string          `json:"_index"`
	ID          string          `json:"_id"`
	SeqNo       int64           `json:"_seq_no"`
	PrimaryTerm int64           `json:"_primary_term"`
	Found       bool            `json:"found"`
	Source      json.RawMessage `json:"_source"`
}

func (response getResponse) hit() searchHit {
	return searchHit{
		Index:       response.Index,
		ID:          response.ID,
		SeqNo:       response.SeqNo,
		PrimaryTerm: response.PrimaryTerm,
		Source:      response.Source,
	}
}

type multiSearchResponse struct {
	Responses []struct {
		searchCompleteness
		Status int             `json:"status"`
		Error  json.RawMessage `json:"error"`
		Hits   struct {
			Hits []searchHit `json:"hits"`
		} `json:"hits"`
	} `json:"responses"`
}

type pointInTimeResponse struct {
	searchCompleteness
	ID string `json:"id"`
}

func (r *Repository) searchWithPIT(
	ctx context.Context,
	targets []string,
	pitID string,
	body map[string]any,
) (searchResponse, string, error) {
	opened := false
	if pitID == "" {
		var err error
		pitID, err = r.openPIT(ctx, targets)
		if err != nil {
			return searchResponse{}, "", err
		}
		opened = true
	}
	body["pit"] = map[string]any{
		"id":         pitID,
		"keep_alive": formatKeepAlive(r.config.PITKeepAlive),
	}
	encoded, err := marshalRequest(body)
	if err != nil {
		return searchResponse{}, "", err
	}
	var response searchResponse
	if err := r.performJSON(ctx, http.MethodPost, "/_search", nil, encoded, &response); err != nil {
		if opened {
			// 原搜索错误优先返回；关闭失败时 PIT 仍会按 keep_alive 自动过期。
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = r.closePIT(cleanupContext, pitID)
			cancel()
		}
		return searchResponse{}, "", err
	}
	if response.PITID != "" {
		pitID = response.PITID
	}
	// ES 7.12+ 在 PIT 查询中隐式追加 _shard_doc；显式排序已用实体身份和
	// _index 形成全序，游标只保留显式项，使 7.10 与新版使用同一协议。
	if sorts, ok := body["sort"].([]any); ok {
		for i := range response.Hits.Hits {
			hit := &response.Hits.Hits[i]
			if len(hit.Sort) == len(sorts)+1 {
				hit.Sort = hit.Sort[:len(sorts)]
			}
		}
	}
	return response, pitID, nil
}

func (r *Repository) openPIT(ctx context.Context, targets []string) (string, error) {
	query := url.Values{
		"keep_alive":         []string{formatKeepAlive(r.config.PITKeepAlive)},
		"ignore_unavailable": []string{"true"},
	}
	var response pointInTimeResponse
	if err := r.performJSON(
		ctx,
		http.MethodPost,
		"/"+joinTargets(targets)+"/_pit",
		query,
		nil,
		&response,
	); err != nil {
		// 部分失败响应仍可能分配了 PIT，拒绝结果时也要释放成功分片上的上下文。
		if response.ID != "" {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = r.closePIT(cleanup, response.ID)
			cancel()
		}
		return "", fmt.Errorf("open elasticsearch point in time: %w", err)
	}
	if response.ID == "" {
		return "", fmt.Errorf("elasticsearch point in time response has empty ID")
	}
	return response.ID, nil
}

func (r *Repository) closePIT(ctx context.Context, pitID string) error {
	body, err := marshalRequest(map[string]any{"id": pitID})
	if err != nil {
		return err
	}
	err = r.performJSON(ctx, http.MethodDelete, "/_pit", nil, body, nil)
	if isResponseStatus(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("close elasticsearch point in time: %w", err)
	}
	return nil
}

func formatKeepAlive(duration time.Duration) string {
	return fmt.Sprintf("%dms", duration.Milliseconds())
}

func (r *Repository) multiSearch(
	ctx context.Context,
	headers []map[string]any,
	bodies []map[string]any,
) ([][]searchHit, error) {
	if len(headers) != len(bodies) {
		return nil, fmt.Errorf("elasticsearch multi-search header/body count mismatch")
	}
	var request bytes.Buffer
	for index := range headers {
		header, err := json.Marshal(headers[index])
		if err != nil {
			return nil, fmt.Errorf("encode elasticsearch multi-search header: %w", err)
		}
		body, err := json.Marshal(bodies[index])
		if err != nil {
			return nil, fmt.Errorf("encode elasticsearch multi-search body: %w", err)
		}
		request.Write(header)
		request.WriteByte('\n')
		request.Write(body)
		request.WriteByte('\n')
	}
	var response multiSearchResponse
	if err := r.performNDJSON(ctx, http.MethodPost, "/_msearch", nil, request.Bytes(), &response); err != nil {
		return nil, err
	}
	if len(response.Responses) != len(headers) {
		return nil, fmt.Errorf(
			"elasticsearch multi-search returned %d responses for %d requests",
			len(response.Responses),
			len(headers),
		)
	}
	results := make([][]searchHit, len(response.Responses))
	for index, item := range response.Responses {
		if len(item.Error) != 0 && string(item.Error) != "null" {
			return nil, decodeResponseError(item.Status, []byte(`{"error":`+string(item.Error)+`}`))
		}
		if err := item.checkComplete(); err != nil {
			return nil, fmt.Errorf("elasticsearch multi-search item %d: %w", index, err)
		}
		results[index] = item.Hits.Hits
	}
	return results, nil
}

// searchCompleteness 阻止 HTTP 200 的超时或部分失败被当成完整查询结果。
// 扫描恢复和幂等查询若接受部分结果，可能漏处理事件或误判对象不存在。
type searchCompleteness struct {
	TimedOut bool `json:"timed_out"`
	Shards   struct {
		Failed int `json:"failed"`
	} `json:"_shards"`
}

func (s searchCompleteness) checkComplete() error {
	if s.TimedOut || s.Shards.Failed > 0 {
		return fmt.Errorf("elasticsearch incomplete search: timed_out=%t failed_shards=%d", s.TimedOut, s.Shards.Failed)
	}
	return nil
}
