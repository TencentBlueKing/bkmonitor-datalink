// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"net/http"
	"testing"
)

// overview.get is the overview route, answered as it is.
func TestOverviewGetReadsTheOverviewRoute(t *testing.T) {
	var asked string
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		asked = request.URL.Path
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"generated_at":"2026-10-13T12:00:00Z","max_age_seconds":15,"verdict":{"state":"ok","line":"ok"}}`))
	})
	for _, op := range NativeOperations(handler) {
		if op.ID != "overview.get" {
			continue
		}
		out := op.Run(context.Background(), Params{})
		if asked != "/api/overview" || out.Value == nil || out.Error != nil {
			t.Fatalf("overview.get asked %q and answered %+v", asked, out)
		}
		return
	}
	t.Fatal("overview.get is not a channel operation")
}
