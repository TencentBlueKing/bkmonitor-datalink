// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A CMDB index going stale is one WARN line naming the tenant, the
// transition, the published age, the bound in force and the gap it was
// derived from; coming back is another.
func TestACMDBIndexTransitionIsOneWarnLine(t *testing.T) {
	var out bytes.Buffer
	line := cmdbIndexTransitionLine(observability.New("worker", &out))
	line(cmdbcache.Transition{Tenant: "tenant-a", From: "", To: cmdbcache.IndexStale,
		PublishedAge: 15*time.Minute + 17*time.Second, Bound: 15 * time.Minute, MaxGap: 5 * time.Minute})
	line(cmdbcache.Transition{Tenant: "tenant-a", From: cmdbcache.IndexStale, To: "",
		PublishedAge: 30 * time.Second, Bound: 33 * time.Minute, MaxGap: 16*time.Minute + 32*time.Second})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want one per transition:\n%s", len(lines), out.String())
	}
	for _, want := range []string{`"cmdb_index_transition"`, `"bk_tenant_id":"tenant-a"`, `"from":"usable"`, `"to":"index_stale"`,
		`"published_age_seconds":917`, `"stale_bound_seconds":900`, `"publish_max_gap_seconds":300`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the line into stale lacks %s: %s", want, lines[0])
		}
	}
	if !strings.Contains(lines[0], `"WARN"`) || !strings.Contains(lines[0], `"result":"degraded"`) ||
		!strings.Contains(lines[1], `"from":"index_stale"`) || !strings.Contains(lines[1], `"to":"usable"`) {
		t.Errorf("lines %q, want WARN into stale and a line out of it", lines)
	}
	if !strings.Contains(lines[1], `"INFO"`) || !strings.Contains(lines[1], `"result":"success"`) {
		t.Errorf("the line back into usable is %s, want INFO with result success", lines[1])
	}
}

// The first load at start goes into usable from never loaded: INFO, not a
// warning on every start. Any transition out of usable - into stale, into
// empty, or back to never loaded - stays a WARN.
func TestACMDBIndexTransitionWarnsOnlyOutOfUsable(t *testing.T) {
	for _, test := range []struct {
		from, to string
		warn     bool
	}{
		{from: cmdbcache.IndexNeverLoaded, to: "", warn: false},
		{from: cmdbcache.IndexEmpty, to: "", warn: false},
		{from: "", to: cmdbcache.IndexStale, warn: true},
		{from: "", to: cmdbcache.IndexEmpty, warn: true},
		{from: "", to: cmdbcache.IndexNeverLoaded, warn: true},
	} {
		var out bytes.Buffer
		cmdbIndexTransitionLine(observability.New("worker", &out))(cmdbcache.Transition{Tenant: "tenant-a", From: test.from, To: test.to})
		warned := strings.Contains(out.String(), `"WARN"`) && strings.Contains(out.String(), `"result":"degraded"`)
		informed := strings.Contains(out.String(), `"INFO"`) && strings.Contains(out.String(), `"result":"success"`)
		if warned != test.warn || informed == test.warn {
			t.Errorf("%q to %q: %s", test.from, test.to, out.String())
		}
	}
}

// A writer flapping across the bound writes the same transition at most once
// per ten minutes per tenant; the next line written says how many were held
// back. Another tenant, or the other direction, is its own line.
func TestAFlappingTransitionIsLimitedAndCountsWhatItHeldBack(t *testing.T) {
	var out bytes.Buffer
	now := time.Unix(1_800_000_000, 0)
	line := cmdbIndexTransitionLineAt(observability.New("worker", &out), func() time.Time { return now })
	into := cmdbcache.Transition{Tenant: "tenant-a", From: "", To: cmdbcache.IndexStale}
	for minute := 0; minute < 10; minute++ {
		line(into)
		now = now.Add(time.Minute)
	}
	line(cmdbcache.Transition{Tenant: "tenant-b", From: "", To: cmdbcache.IndexStale})
	line(into)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want the first, tenant-b's, and one ten minutes on:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"suppressed":0`) || !strings.Contains(lines[1], `"bk_tenant_id":"tenant-b"`) ||
		!strings.Contains(lines[2], `"suppressed":9`) {
		t.Fatalf("lines %q, want suppressed 0, tenant-b, then suppressed 9", lines)
	}
}
