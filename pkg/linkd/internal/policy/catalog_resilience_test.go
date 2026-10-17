// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"testing/synctest"
)

func TestCatalogKeepsUsableSnapshotAndRefreshesPublications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service := NewService(newTestDocuments())
		request := testRequest()
		mustApply(t, service, request)
		reader := &catalogReader{service: service}
		catalog := NewCatalog(reader)
		if _, err := catalog.Freeze(t.Context(), "tenant"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = catalog.Run(ctx) }()
		synctest.Wait()
		reader.mu.Lock()
		reader.fail = true
		reader.mu.Unlock()
		time.Sleep(20 * time.Second)
		synctest.Wait()
		cached, err := catalog.Freeze(t.Context(), "tenant")
		if err != nil || len(cached) != 1 || cached[0].Release.Version != 1 {
			t.Fatal("short outage discarded valid catalog", cached, err)
		}
		request.ExpectedVersion, request.OperationID = 1, "published-during-outage"
		request.Spec = testSpec("second")
		mustApply(t, service, request)
		reader.mu.Lock()
		reader.fail = false
		reader.mu.Unlock()
		time.Sleep(6 * time.Second)
		synctest.Wait()
		published, err := catalog.Freeze(t.Context(), "tenant")
		if err != nil || published[0].Release.Version != 2 {
			t.Fatal("publication waited for cache expiry", err)
		}
		reader.mu.Lock()
		reader.fail = true
		reader.mu.Unlock()
		time.Sleep(61 * time.Second)
		synctest.Wait()
		if _, err := catalog.Freeze(t.Context(), "tenant"); !errors.Is(err, ErrUnavailable) {
			t.Fatal("expired catalog remained usable", err)
		}
		cancel()
		synctest.Wait()
	})
}
