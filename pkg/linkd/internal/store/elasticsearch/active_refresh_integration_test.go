// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package elasticsearchstore

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"linkd/internal/store/storetest"
)

func TestElasticsearchRefreshActiveAlertsMakesUnrefreshedMainSearchable(t *testing.T) {
	endpoint := os.Getenv(elasticsearchIntegrationURLEnv)
	if endpoint == "" {
		t.Skipf("set %s to run isolated active refresh integration", elasticsearchIntegrationURLEnv)
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport := endpointTransport{baseURL: base, client: &http.Client{Timeout: 15 * time.Second}, apiKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY")}
	prefix := "linkd-active-refresh-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	router, err := NewStaticRouter(prefix)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	schema := router.SchemaConfig()
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if err := repository.performJSON(cleanup, http.MethodDelete, "/"+router.alertIndex, nil, nil, nil); err != nil {
			t.Error(err)
		}
		for _, spec := range schema.Templates() {
			_ = repository.performJSON(cleanup, http.MethodDelete, "/_index_template/"+spec.Name, nil, nil, nil)
		}
	})
	if err := repository.EnsureSchema(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if err := repository.EnsureIndex(ctx, router.alertIndex, entityAlert); err != nil {
		t.Fatal(err)
	}
	settings, err := marshalRequest(map[string]any{"index": map[string]any{"refresh_interval": "-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.performJSON(ctx, http.MethodPut, "/"+router.alertIndex+"/_settings", nil, settings, nil); err != nil {
		t.Fatal(err)
	}
	alert := storetest.Alert("tenant", "unrefreshed-main", "event", "fingerprint", "warning")
	if _, err := repository.CreateAlertAfterActiveLookup(ctx, alert); err != nil {
		t.Fatal(err)
	}
	before, err := repository.ListActiveAlerts(ctx, "tenant", "", 32)
	if err != nil || len(before.Alerts) != 0 {
		t.Fatal("new main unexpectedly searchable before explicit refresh", before, err)
	}
	if err := repository.RefreshActiveAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := repository.ListActiveAlerts(ctx, "tenant", "", 32)
	if err != nil || len(after.Alerts) != 1 || after.Alerts[0].Alert.AlertID != alert.AlertID {
		t.Fatal("ready scan missed unrefreshed main", after, err)
	}
}
