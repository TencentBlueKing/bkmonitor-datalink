// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package k8sread

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// An operator's log read asks the API server for what it asked for, up to
// a generous bound: five thousand lines from three days back are asked as
// such, not cut to a thousand lines of one day. One read is one GET, nothing
// next to what the API server serves the cluster.
func TestALogReadAsksForWhatTheOperatorAskedFor(t *testing.T) {
	api := &fakeAPI{t: t, logBody: "2026-09-23T07:59:00Z ok\n"}
	reader := newReader(t, api)
	if _, err := reader.Logs(context.Background(), LogRequest{Pod: selfPod, Lines: 5000, SinceSeconds: 3 * 24 * 3600}); err != nil {
		t.Fatal(err)
	}
	var query url.Values
	for _, path := range api.paths() {
		if strings.Contains(path, "/log?") {
			query, _ = url.ParseQuery(path[strings.Index(path, "?")+1:])
		}
	}
	if query.Get("tailLines") != "5000" || query.Get("sinceSeconds") != "259200" {
		t.Fatalf("the log was asked for as %v, want 5000 lines from 259200 seconds back", query)
	}
}

// The bounds that remain are generous: a read's own size, never a count
// that turns an ordinary investigation away.
func TestTheReadBoundsAreGenerous(t *testing.T) {
	if MaxPods < 500 || MaxEvents < 2000 || MaxLogLines < 100000 || MaxLogBytes < 8<<20 || MaxNamespaces < 64 ||
		MaxLogSinceSeconds < 7*24*3600 || maxLogReadBytes < MaxLogBytes {
		t.Fatalf("bounds pods %d events %d lines %d bytes %d namespaces %d since %d read %d", MaxPods, MaxEvents, MaxLogLines,
			MaxLogBytes, MaxNamespaces, MaxLogSinceSeconds, maxLogReadBytes)
	}
}
