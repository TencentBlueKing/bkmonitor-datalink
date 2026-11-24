// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestCompleteAlertChangeOnlyPatchesIntentAndKeepsCAS(t *testing.T) {
	for _, field := range []store.AlertChangeField{store.PolicyChangeField, store.MergeChangeField} {
		t.Run(string(field), func(t *testing.T) {
			a := storetest.Alert("tenant", "a", "opening", "fp", "warning")
			a.ExtraData = domain.JSONObject{"untouched": json.RawMessage(`{"value":null}`)}
			version, _ := encodeVersion(versionPayload{Index: "linkd-test-alerts", DocumentID: alertDocumentID(a), SeqNo: 4, PrimaryTerm: 2})
			current := store.StoredAlert{Alert: a, Version: version}
			seq, requests := int64(4), 0
			repo, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.Method != http.MethodPost || req.URL.Query().Get("if_primary_term") != "2" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
				}
				if req.URL.Query().Get("if_seq_no") != strconv.FormatInt(seq, 10) {
					return managerJSONResponse(409, `{"error":{"type":"version_conflict_engine_exception"}}`), nil
				}
				raw, _ := io.ReadAll(req.Body)
				var body struct {
					Doc map[string]json.RawMessage `json:"doc"`
				}
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatal(err)
				}
				if len(body.Doc) != 2 || string(body.Doc[string(field)]) != "null" || string(body.Doc["merge_work"]) != "false" {
					t.Fatalf("unexpected patch: %s", raw)
				}
				seq++
				return managerJSONResponse(200, fmt.Sprintf(`{"_index":"linkd-test-alerts","_id":%q,"_seq_no":%d,"_primary_term":2}`, alertDocumentID(a), seq)), nil
			}), mustStaticRouter(t), DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			updated, err := repo.CompleteAlertChange(t.Context(), current, field)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(updated.Alert, current.Alert) || updated.Version == current.Version {
				t.Fatal("completion changed business state or lost token")
			}
			if _, err := repo.CompleteAlertChange(t.Context(), current, field); !errors.Is(err, store.ErrVersionConflict) {
				t.Fatal("old completion accepted", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := repo.CompleteAlertChange(ctx, updated, field); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if requests != 2 {
				t.Fatalf("completion performed extra reads: %d", requests)
			}
		})
	}
}
