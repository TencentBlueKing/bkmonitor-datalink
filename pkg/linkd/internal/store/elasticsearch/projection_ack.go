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
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// AcknowledgeProjection 仅点读身份和水位，条件更新投影元数据；业务载荷不会经过 Go。
// 版本冲突及归档搬迁重新定位，旧快照不能覆盖并发发生的业务变化。
func (r *Repository) AcknowledgeProjection(ctx context.Context, ack store.ProjectionAcknowledgment) error {
	if err := ack.Validate(); err != nil {
		return err
	}
	route, err := r.router.AlertRoute(ctx, ack.AlertID)
	if err != nil {
		return err
	}
	route, err = normalizeRoute(route, r.config.MaxReadTargets)
	if err != nil {
		return err
	}
	for range 4 {
		var current struct {
			Index  string `json:"_index"`
			ID     string `json:"_id"`
			Seq    int64  `json:"_seq_no"`
			Term   int64  `json:"_primary_term"`
			Found  bool   `json:"found"`
			Source struct {
				TenantID   string                 `json:"bk_tenant_id"`
				AlertID    string                 `json:"alert_id"`
				Revision   int64                  `json:"revision"`
				Projection domain.AlertProjection `json:"projection"`
			} `json:"_source"`
		}
		found := false
		for _, target := range route.ReadTargets {
			query := url.Values{"realtime": {"true"}, "_source_includes": {"bk_tenant_id,alert_id,revision,projection"}}
			err = r.performJSON(ctx, http.MethodGet, "/"+target+"/_doc/"+documentID(ack.TenantID, ack.AlertID), query, nil, &current)
			if isResponseStatus(err, http.StatusNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			found = true
			break
		}
		if !found {
			return store.ErrNotFound
		}
		if !current.Found || current.Source.TenantID != ack.TenantID || current.Source.AlertID != ack.AlertID || current.ID != documentID(ack.TenantID, ack.AlertID) || current.Term < 1 || current.Seq < 0 || current.Index == "" {
			return store.ErrInvalidArgument
		}
		if err := current.Source.Projection.Validate(current.Source.Revision); err != nil {
			return fmt.Errorf("invalid projection watermarks: %w", err)
		}
		next, changed, err := current.Source.Projection.Acknowledge(ack.TargetID, ack.SourceVersion, ack.Revision, ack.At)
		if err != nil || !changed {
			return err
		}
		// 水位与索引标记同次提交，更新不推进业务 revision，也不等待搜索刷新。
		body, err := marshalRequest(map[string]any{"doc": map[string]any{"projection": next, "projection_work": next.Pending()}})
		if err != nil {
			return err
		}
		query := url.Values{"if_seq_no": {strconv.FormatInt(current.Seq, 10)}, "if_primary_term": {strconv.FormatInt(current.Term, 10)}, "refresh": {"false"}}
		var response indexResponse
		err = r.performJSON(ctx, http.MethodPost, "/"+current.Index+"/_update/"+current.ID, query, body, &response)
		if isResponseStatus(err, http.StatusConflict) || isResponseStatus(err, http.StatusNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if response.Index != current.Index || response.ID != current.ID || response.PrimaryTerm < 1 || response.SeqNo < current.Seq {
			return store.ErrInvalidArgument
		}
		return nil
	}
	return store.ErrVersionConflict
}

var _ store.ProjectionAcknowledger = (*Repository)(nil)
