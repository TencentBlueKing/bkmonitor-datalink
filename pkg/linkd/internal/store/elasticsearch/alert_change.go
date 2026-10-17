// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"linkd/internal/store"
)

// CompleteAlertChange 只发送完成字段；Bulk update 保留每项原有版本条件。
func (r *Repository) CompleteAlertChange(ctx context.Context, current store.StoredAlert, field store.AlertChangeField) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	version, err := alertSnapshotVersion(current)
	if err != nil {
		return store.StoredAlert{}, err
	}
	next, err := store.CompletedAlertChange(current, field)
	if err != nil {
		return store.StoredAlert{}, err
	}
	body, err := json.Marshal(map[string]any{"doc": map[string]any{string(field): nil, "merge_work": store.HasMergeWork(next)}})
	if err != nil {
		return store.StoredAlert{}, err
	}
	query := url.Values{"if_seq_no": {strconv.FormatInt(version.SeqNo, 10)}, "if_primary_term": {strconv.FormatInt(version.PrimaryTerm, 10)}, "refresh": {"false"}}
	var response indexResponse
	err = r.performJSON(ctx, http.MethodPost, "/"+version.Index+"/_update/"+version.DocumentID, query, body, &response)
	if err != nil {
		if isResponseStatus(err, http.StatusConflict) {
			return store.StoredAlert{}, store.ErrVersionConflict
		}
		if isResponseStatus(err, http.StatusNotFound) {
			return store.StoredAlert{}, store.ErrNotFound
		}
		return store.StoredAlert{}, err
	}
	return storedAlertFromIndexResponse(next, version.DocumentID, response)
}
