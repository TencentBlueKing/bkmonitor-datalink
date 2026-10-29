// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package elasticsearchstore

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func TestAlertLogLargeBatchHonorsRequestBytes(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		count, bytes, failAt int
	}{
		{"five_thousand", 5000, 16 << 20, 0},
		{"byte_chunks", 10, 1024, 0},
		{"failed_later_chunk", 10, 1024, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, received := 0, 0
			cfg := DefaultConfig()
			cfg.MaxRequestBytes = tc.bytes
			cfg.MaxDocumentBytes = min(512, tc.bytes)
			r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == tc.failAt {
					return nil, fmt.Errorf("ES unavailable")
				}
				raw, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				if len(raw) > tc.bytes {
					t.Fatal("request exceeds byte budget", len(raw))
				}
				lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
				items := []any{}
				for i := 0; i < len(lines); i += 2 {
					var head map[string]map[string]any
					if err := json.Unmarshal([]byte(lines[i]), &head); err != nil {
						t.Fatal(err)
					}
					items = append(items, map[string]any{"create": map[string]any{"_index": head["create"]["_index"], "_id": head["create"]["_id"], "status": 201}})
					received++
				}
				return jsonResponse(t, map[string]any{"items": items}), nil
			}), mustStaticRouter(t), cfg)
			if err != nil {
				t.Fatal(err)
			}
			logs := make([]domain.AlertLog, tc.count)
			for i := range logs {
				logs[i] = domain.AlertLog{LogID: fmt.Sprintf("log-%d", i), BKTenantID: "tenant", AlertID: "alert", OperatorKind: domain.OperatorKindUser, OperationKind: domain.OperationKindClose, CreatedTime: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC), Params: domain.JSONObject{}}
			}
			results, err := r.AppendAlertLogs(t.Context(), logs)
			if tc.failAt > 0 {
				if err == nil || calls != tc.failAt {
					t.Fatal(calls, err)
				}
				return
			}
			if err != nil || len(results) != tc.count || received != tc.count {
				t.Fatal(len(results), received, err)
			}
			for _, item := range results {
				if item.Err != nil || !item.Result.Created {
					t.Fatal(item)
				}
			}
			if tc.count == store.MaxAlertLogBatchSize && calls != 1 {
				t.Fatal("large batch split by old count limit", calls)
			}
			if tc.bytes == 1024 && calls < 2 {
				t.Fatal("byte budget did not split batch")
			}
		})
	}
}
