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
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
	"linkd/internal/suppressioncheck"
)

func checkRequest(c suppressioncheck.Command) suppressioncheck.Request {
	raw, _ := json.Marshal([]string{"suppression-request:v1", c.TenantID, c.Kind, c.WindowID, c.OperationID})
	return suppressioncheck.Request{ID: fmt.Sprintf("%x", sha256.Sum256(raw)), Command: c, State: "pending", CreatedAt: time.Now().UTC()}
}

type checkJournal struct {
	row suppressioncheck.Request
	err error
}

func (j *checkJournal) Get(context.Context, string, string, string, string) (suppressioncheck.Stored, error) {
	return suppressioncheck.Stored{Request: j.row, Version: "1"}, j.err
}

func (j *checkJournal) List(_ context.Context, _, _, _, after string, _ int) (suppressioncheck.Page, error) {
	if after != "" {
		return suppressioncheck.Page{}, j.err
	}
	return suppressioncheck.Page{Items: []suppressioncheck.Request{j.row}, Next: j.row.Cursor()}, j.err
}

type submitCheck func(context.Context, suppressioncheck.Command) (suppressioncheck.Request, error)

func (f submitCheck) Request(ctx context.Context, c suppressioncheck.Command) (suppressioncheck.Request, error) {
	return f(ctx, c)
}

func TestSuppressionRequestAPIStrictCommandScopeAndHistoricalReads(t *testing.T) {
	command := suppressioncheck.Command{TenantID: "tenant", Kind: "clip", WindowID: strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), ExpectedEpoch: "first", ExpectedOwner: "", OperationID: "op", OperatorID: "tester", Reason: "核对"}
	journal := &checkJournal{row: checkRequest(command)}
	var resultErr error
	api := &API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}, SuppressionChecks: NewSuppressionChecks(journal, submitCheck(func(_ context.Context, c suppressioncheck.Command) (suppressioncheck.Request, error) {
		return checkRequest(c), resultErr
	}))}
	h := api.Handler()
	base := "/api/v1/policy-runtime/suppression/clip/" + command.WindowID
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
	body := `{"bk_tenant_id":"tenant","expected_epoch":"first","expected_owner_alert_id":"","operation_id":"op","operator_id":"tester","reason":"核对"}`
	accepted := call("POST", base+"/reconcile", body, 202)
	if accepted.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable command")
	}
	for _, bad := range []string{strings.Replace(body, `"expected_owner_alert_id":"",`, "", 1), strings.Replace(body, `"first"`, `""`, 1), strings.Replace(body, `"reason":"核对"`, `"reason":" "`, 1), strings.Replace(body, `"reason":"核对"`, `"force":true`, 1), body + "{}"} {
		call("POST", base+"/reconcile", bad, 400)
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
		response := call("POST", base+"/reconcile", body, test.status)
		if strings.Contains(response.Body.String(), "private") {
			t.Fatal("private error exposed")
		}
	}
	api.SuppressionChecks.slots <- struct{}{}
	api.SuppressionChecks.slots <- struct{}{}
	call("GET", base+"/requests?bk_tenant_id=tenant", "", 429)
	<-api.SuppressionChecks.slots
	<-api.SuppressionChecks.slots
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequestWithContext(t.Context(), http.MethodPost, base+"/reconcile", strings.NewReader(body)))
	if unauth.Code != 401 {
		t.Fatal("unauthenticated command")
	}
}
