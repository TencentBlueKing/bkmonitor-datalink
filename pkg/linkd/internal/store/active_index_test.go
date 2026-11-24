// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package store_test

import (
	"testing"

	"linkd/internal/store"
)

func TestActiveIndexQueryBatchRequiresTenant(t *testing.T) {
	q := store.ActiveIndexQuery{Sources: []string{"source"}, PageSize: 2000, MaxBytes: 4096, Fingerprints: []string{"a", "b"}}
	if q.Validate() == nil {
		t.Fatal("unscoped member lookup accepted")
	}
	q.BKTenantID = "tenant"
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
}
