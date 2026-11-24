// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package assembly

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/enrich"
	"linkd/internal/store/storetest"
)

func TestRouterUsesCurrentSourceForAllIntakeVersions(t *testing.T) {
	source := config.EventSource{EventSourceID: "host", Version: 2, RelatedTenantID: "tenant"}
	router, err := NewRouter([]config.EventSource{source}, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int64{1, 2, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			event := storetest.Event("tenant", "event", "fingerprint", "warning")
			event.EventSourceID, event.EventSourceVersion = source.EventSourceID, version
			result, err := router.Enrich(t.Context(), enrich.Input{Event: event})
			if err != nil || result.SourceVersion != source.Version || result.ConfigDigest == "" || len(result.Data.Evaluations) != 1 {
				t.Fatalf("current task source not used: %+v %v", result, err)
			}
			if event.EventSourceVersion != version {
				t.Fatal("intake version was overwritten")
			}
			event.BKTenantID = "foreign"
			if _, err := router.Enrich(t.Context(), enrich.Input{Event: event}); err == nil {
				t.Fatal("foreign tenant used task resources")
			}
		})
	}
}

type currentSourceBlockingEngine struct{ started chan<- struct{} }

func (e currentSourceBlockingEngine) Enrich(ctx context.Context, _ enrich.Input) (enrich.Result, error) {
	e.started <- struct{}{}
	<-ctx.Done()
	return enrich.Result{}, ctx.Err()
}

func TestRouterSharesCurrentEngineAndCancelsConcurrentOldEvents(t *testing.T) {
	const workers = 16
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	router, err := NewRouter([]config.EventSource{{EventSourceID: "host", Version: 2, RelatedTenantID: "tenant"}}, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, workers)
	router.routes["host"] = currentSourceBlockingEngine{started: started}
	event := storetest.Event("tenant", "event", "fingerprint", "warning")
	event.EventSourceID, event.EventSourceVersion = "host", 1
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			result, err := router.Enrich(ctx, enrich.Input{Event: event})
			if !errors.Is(err, context.Canceled) || result.SourceVersion != 2 {
				t.Errorf("current engine cancellation: %+v %v", result, err)
			}
		})
	}
	for range workers {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("old intake versions did not share available workers")
		}
	}
	cancel()
	wg.Wait()
}
