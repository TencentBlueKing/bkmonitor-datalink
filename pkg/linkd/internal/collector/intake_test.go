// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"linkd/internal/pluginruntime"
)

type sourceList []SourceBinding

func (s sourceList) Lookup(context.Context, string) ([]SourceBinding, error) { return s, nil }

type receiptFunc func(context.Context, pluginruntime.Request) (pluginruntime.Request, error)

func (f receiptFunc) Accept(ctx context.Context, r pluginruntime.Request) (pluginruntime.Request, error) {
	return f(ctx, r)
}

func TestSourceIntakeScopesSharedIdentifier(t *testing.T) {
	sources := sourceList{
		{Secret: "one", Enabled: true, Request: pluginruntime.Request{TenantID: "tenant-one", SourceID: "source", Plugin: "tcop"}},
		{Secret: "two", Enabled: true, Request: pluginruntime.Request{TenantID: "tenant-two", SourceID: "source", Plugin: "rest_api_push"}},
	}
	saved := 0
	intake := NewSourceIntake(sources, receiptFunc(func(_ context.Context, r pluginruntime.Request) (pluginruntime.Request, error) {
		saved++
		return restoreReceipt(r, "1794657600000-1")
	}))
	if err := intake.Authorize(t.Context(), "shared", "two"); err != nil {
		t.Fatal(err)
	}
	request, _, err := intake.Prepare(t.Context(), "shared", "two", json.RawMessage(`{"bk_tenant_id":"tenant-one","plugin":"tcop"}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.TenantID != "tenant-two" || request.Plugin != "rest_api_push" || request.Mode != "push" || request.ReceivedAt != "2026-11-14T12:00:00Z" {
		t.Fatalf("incorrect binding: %+v", request)
	}
	sources[1].Enabled = false
	if _, _, err = intake.Prepare(t.Context(), "shared", "two", json.RawMessage(`{}`)); !errors.Is(err, ErrSourceUnauthorized) {
		t.Fatalf("disabled source accepted: %v", err)
	}
	if saved != 1 {
		t.Fatal("unauthorized input persisted")
	}
	for _, secret := range []string{"", "wrong"} {
		if err = intake.Authorize(t.Context(), "shared", secret); !errors.Is(err, ErrSourceUnauthorized) {
			t.Fatal(err)
		}
	}
}

func TestReceiptRestore(t *testing.T) {
	for _, id := range []string{"missing", "broken-0"} {
		if _, err := restoreReceipt(pluginruntime.Request{}, id); err == nil {
			t.Fatal("invalid receipt accepted")
		}
	}
}
