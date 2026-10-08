// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/merge"
	"linkd/internal/policy"
)

func mergeRetryAPIRequest(c merge.RetryCommand) merge.RetryRequest {
	raw, _ := json.Marshal([]string{"merge-request:v1", c.TenantID, c.Kind, c.TargetID, c.OperationID})
	return merge.RetryRequest{ID: fmt.Sprintf("%x", sha256.Sum256(raw)), Command: c, WindowID: strings.Repeat("b", 64), State: "pending", CreatedAt: time.Now().UTC()}
}

type mergeRetryAPIJournal struct {
	row merge.RetryRequest
	err error
}

func (j *mergeRetryAPIJournal) GetRetry(context.Context, string, string, string, string) (merge.StoredRetry, error) {
	return merge.StoredRetry{Request: j.row, Version: "1"}, j.err
}

func (j *mergeRetryAPIJournal) ListRetries(_ context.Context, _, _, _, after string, _ int) (merge.RetryPage, error) {
	if after != "" {
		return merge.RetryPage{}, j.err
	}
	return merge.RetryPage{Items: []merge.RetryRequest{j.row}, Next: j.row.Cursor()}, j.err
}

type submitMergeRetry func(context.Context, merge.RetryCommand) (merge.RetryRequest, error)

func (f submitMergeRetry) Request(ctx context.Context, c merge.RetryCommand) (merge.RetryRequest, error) {
	return f(ctx, c)
}

func TestMergeRetryAPIStrictCommandScopeAndHistoricalReads(t *testing.T) {
	id, err := domain.MergeDecisionID("tenant", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	command := merge.RetryCommand{TenantID: "tenant", Kind: "decisions", TargetID: id, ExpectedToken: strings.Repeat("c", 64), OperationID: "op", OperatorID: "tester", Reason: "核对"}
	journal := &mergeRetryAPIJournal{row: mergeRetryAPIRequest(command)}
	var resultErr error
	api := &API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}, MergeRetries: NewMergeRetries(journal, submitMergeRetry(func(_ context.Context, c merge.RetryCommand) (merge.RetryRequest, error) {
		return mergeRetryAPIRequest(c), resultErr
	}))}
	h := api.Handler()
	base := "/api/v1/policy-runtime/merge/decisions/" + command.TargetID
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		r.Header.Set("Internal-Token", testJWT(t, "admin"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s status %d want %d", path, w.Code, want)
		}
		return w
	}
	body := `{"bk_tenant_id":"tenant","expected_token":"` + command.ExpectedToken + `","operation_id":"op","operator_id":"tester","reason":"核对"}`
	call("GET", base+"/control?bk_tenant_id=tenant", "", 200)
	call("GET", base+"/control?bk_tenant_id=other", "", 503)
	accepted := call("POST", base+"/requests", body, 202)
	if accepted.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable command")
	}
	for _, bad := range []string{strings.Replace(body, `"expected_token":"`+command.ExpectedToken+`",`, "", 1), strings.Replace(body, command.ExpectedToken, "bad-token", 1), strings.Replace(body, `"reason":"核对"`, `"reason":" "`, 1), strings.Replace(body, `"reason":"核对"`, `"force":true`, 1), body + "{}"} {
		call("POST", base+"/requests", bad, 400)
	}
	list := call("GET", base+"/requests?bk_tenant_id=tenant", "", 200)
	var page struct {
		Next string `json:"next"`
	}
	if json.Unmarshal(list.Body.Bytes(), &page) != nil || page.Next == "" {
		t.Fatal("request cursor missing")
	}
	call("GET", base+"/requests?bk_tenant_id=tenant&after="+page.Next, "", 200)
	call("GET", base+"/requests?bk_tenant_id=other&after="+page.Next, "", 400)
	call("GET", base+"/requests/"+journal.row.ID+"?bk_tenant_id=tenant", "", 200) // 不依赖 Redis 查询端口或当前窗口存在。
	call("GET", base+"/requests/"+journal.row.ID+"?bk_tenant_id=other", "", 403)
	for _, tail := range []string{"&limit=5", "&epoch=first", "&after=bad"} {
		call("GET", base+"/requests?bk_tenant_id=tenant"+tail, "", 400)
	}
	for _, test := range []struct {
		err    error
		status int
	}{{scheduler.ErrLockBusy, 429}, {errors.Join(scheduler.ErrLockBusy, errors.New("private lease error")), 500}, {policy.ErrConflict, 409}, {policy.ErrInvalid, 503}} {
		resultErr = test.err
		response := call("POST", base+"/requests", body, test.status)
		if strings.Contains(response.Body.String(), "private") {
			t.Fatal("private error exposed")
		}
	}
	api.MergeRetries.slots <- struct{}{}
	api.MergeRetries.slots <- struct{}{}
	call("GET", base+"/requests?bk_tenant_id=tenant", "", 429)
	<-api.MergeRetries.slots
	<-api.MergeRetries.slots
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequestWithContext(t.Context(), http.MethodPost, base+"/requests", strings.NewReader(body)))
	if unauth.Code != 401 {
		t.Fatal("unauthenticated command")
	}
}

func (j *mergeRetryAPIJournal) ReadControlPoint(context.Context, string, string, string) (merge.ControlPoint, error) {
	c := j.row.Command
	return merge.ControlPoint{TenantID: c.TenantID, Kind: c.Kind, TargetID: c.TargetID, WindowID: j.row.WindowID, Token: c.ExpectedToken, Phase: "capturing", Outcome: "succeeded", UpdatedAt: j.row.CreatedAt}, j.err
}
