// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// Native queries run side by side: a server that answers none until it has
// eight in flight answers all eight. Before, one query at a time reached the
// egress and the rest waited or were refused.
func TestNativeQueriesRunSideBySide(t *testing.T) {
	const together = 8
	var arrived sync.WaitGroup
	arrived.Add(together)
	all := make(chan struct{})
	go func() { arrived.Wait(); close(all) }()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived.Done()
		select {
		case <-all:
		case <-testwait.Expired(t):
			http.Error(w, "the queries did not arrive together", http.StatusGatewayTimeout)
			return
		}
		_, _ = io.WriteString(w, `{"series":[]}`)
	}))
	defer s.Close()
	p, _ := NewWithQuerySource(fixture(s.URL), nil, "alarmd-sre")
	defer p.Close()
	errs := make([]error, together)
	var done sync.WaitGroup
	for i := range errs {
		done.Add(1)
		go func() { defer done.Done(); _, errs[i] = p.Query(context.Background(), request()) }()
	}
	done.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("query %d = %v, want all eight answered together", i, err)
		}
	}
}

// Two failure reads at once both complete.
func TestTwoFailureReadsAtOnceBothComplete(t *testing.T) {
	base := failureNow.Add(-time.Hour).UnixMilli()
	server := &failureServer{rows: func(from int) ([]map[string]any, int) {
		time.Sleep(40 * time.Millisecond) // fixed-wait: the server's per-page answer time, which keeps the two reads in flight together
		page := make([]map[string]any, 0, MaxTraceRows)
		for i := 0; i < MaxTraceRows; i++ {
			page = append(page, span(fmt.Sprintf("t%d", from+i), "web", "/busy", 500, nil, base+int64(from+i), 1, nil))
		}
		return page, 3 * MaxTraceRows
	}}
	p := failureProvider(t, server, false)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answer, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Now: failureNow})
			if err == nil && answer.Scanned != 3*MaxTraceRows {
				err = fmt.Errorf("scanned %d, want %d", answer.Scanned, 3*MaxTraceRows)
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("read %d = %v, want both reads complete", i, err)
		}
	}
}

// A response past the bound the channel set is refused by name and kept
// out of the receipt.
func TestANativeResponsePastItsBoundIsRefused(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"series":"`+strings.Repeat("x", 2048)+`"}`)
	}))
	defer s.Close()
	p, _ := NewWithQuerySource(fixture(s.URL), nil, "alarmd-sre")
	defer p.Close()
	if p.ResponseBytes() != DefaultResponseBytes {
		t.Fatalf("default bound %d, want %d", p.ResponseBytes(), DefaultResponseBytes)
	}
	if _, err := p.Query(context.Background(), request()); err != nil {
		t.Fatalf("under the default bound: %v", err)
	}
	p.SetResponseBytes(1024)
	receipt, err := p.Query(context.Background(), request())
	if code(err) != "uq_response_bytes_exceeded" || receipt.Native != nil {
		t.Fatalf("past the bound: %v, native kept %d bytes", err, len(receipt.Native))
	}
}

// Each receipt keeps its own response: a later query does not write into
// the bytes an earlier receipt holds.
func TestAReceiptKeepsItsOwnResponseAcrossLaterQueries(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"series":[],"page":"page-%d"}`, calls.Add(1))
	}))
	defer s.Close()
	p, _ := NewWithQuerySource(fixture(s.URL), nil, "alarmd-sre")
	defer p.Close()
	first, err := p.Query(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	kept := string(first.Native)
	if _, err := p.Query(context.Background(), request()); err != nil {
		t.Fatal(err)
	}
	if string(first.Native) != kept || !strings.Contains(kept, "page-1") {
		t.Fatalf("first receipt %s after a second query, want it unchanged (%s)", first.Native, kept)
	}
}
