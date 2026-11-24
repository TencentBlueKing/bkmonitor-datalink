// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package queryservice

import (
	"context"
	"errors"
	"sync"
	"testing"

	"linkd/internal/onemodel"
)

type pagesStub struct {
	search func(context.Context, string, onemodel.PageQuery) (onemodel.Page, error)
}

func (p pagesStub) Search(ctx context.Context, t string, q onemodel.PageQuery) (onemodel.Page, error) {
	return p.search(ctx, t, q)
}

func (p pagesStub) Close(context.Context, string, string) error { return nil }

type readerStub struct {
	related func(context.Context, string, []onemodel.Instance, string, string, onemodel.Query) ([]onemodel.Instance, error)
}

func (r readerStub) Search(context.Context, string, onemodel.Query) ([]onemodel.Instance, error) {
	return nil, nil
}

func (r readerStub) Related(ctx context.Context, t string, roots []onemodel.Instance, relation, direction string, q onemodel.Query) ([]onemodel.Instance, error) {
	return r.related(ctx, t, roots, relation, direction, q)
}

func status(t *testing.T, err error, want int) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != want {
		t.Fatalf("error=%v want=%d", err, want)
	}
}

func TestMissingAndConcurrentCancellation(t *testing.T) {
	_, err := New(nil, nil).Search(t.Context(), SearchRequest{})
	status(t, err, 503)
	entered := make(chan struct{}, 4)
	service := New(pagesStub{search: func(ctx context.Context, _ string, _ onemodel.PageQuery) (onemodel.Page, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return onemodel.Page{}, ctx.Err()
	}}, readerStub{})
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wg.Go(func() { _, err := service.Search(ctx, SearchRequest{}); results <- err })
	}
	for range 4 {
		<-entered
	}
	_, err = service.Search(t.Context(), SearchRequest{})
	status(t, err, 429)
	cancel()
	wg.Wait()
	close(results)
	for err := range results {
		status(t, err, 504)
	}
	if len(service.slots) != 0 {
		t.Fatal("query capacity leaked")
	}
}

func TestRelatedValidatesRootsAndPreservesLimits(t *testing.T) {
	calls := 0
	reader := readerStub{related: func(_ context.Context, tenant string, roots []onemodel.Instance, relation, direction string, q onemodel.Query) ([]onemodel.Instance, error) {
		calls++
		if tenant != "t" || roots[0].TenantID != "t" || roots[0].InstanceID != "1" || relation != "belongs" || direction != "both" || q.Limit != 1024 {
			t.Fatal("wrong relation contract")
		}
		return nil, onemodel.ErrResultLimit
	}}
	service := New(pagesStub{}, reader)
	request := RelatedRequest{Tenant: "t", Roots: []Root{{ModelID: "host", InstanceID: "1"}}, Relation: "belongs", Direction: "both", Query: onemodel.Query{ModelID: "biz"}}
	_, err := service.Related(t.Context(), request)
	status(t, err, 422)
	request.Roots[0].InstanceID = ""
	_, err = service.Related(t.Context(), request)
	status(t, err, 400)
	request.Roots = []Root{{ModelID: "host", InstanceID: "1"}}
	request.Direction = "invalid"
	_, err = service.Related(t.Context(), request)
	status(t, err, 400)
	if calls != 1 {
		t.Fatal("invalid relation queried backend")
	}
}

func TestQueryErrorsDoNotExposeBackendDetails(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{{errors.New("password=private"), 502}, {onemodel.ErrInvalidCursor, 400}, {onemodel.ErrCursorExpired, 410}, {context.DeadlineExceeded, 504}} {
		service := New(pagesStub{search: func(context.Context, string, onemodel.PageQuery) (onemodel.Page, error) {
			return onemodel.Page{}, tc.err
		}}, readerStub{})
		_, err := service.Search(t.Context(), SearchRequest{})
		status(t, err, tc.want)
		if err.Error() == "password=private" {
			t.Fatal("backend detail leaked")
		}
	}
}
