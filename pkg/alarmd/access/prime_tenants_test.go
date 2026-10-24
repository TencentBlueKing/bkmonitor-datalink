// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type primingAdmission struct {
	primed []string
	ctxs   []context.Context
}

func (p *primingAdmission) EnrichFor(string, map[string]json.RawMessage) admission.Facts {
	return admission.Facts{}
}
func (p *primingAdmission) Admit(admission.PlanContext, *admission.Facts) (bool, string, string) {
	return true, "", ""
}
func (p *primingAdmission) PrimeFor(ctx context.Context, tenant string) {
	p.primed, p.ctxs = append(p.primed, tenant), append(p.ctxs, ctx)
}

// A query primes the tenant of every Plan it feeds, each once, with the
// query's own context - the deadline its first read of a tenant is waited
// for within - before any of its series is enriched.
func TestAQueryPrimesItsPlansTenantsWithinItsOwnContext(t *testing.T) {
	consumer := func(tenant, strategy string) execution.DataRequirementConsumer {
		return execution.DataRequirementConsumer{Consumer: execution.ConsumerRef{
			Plan: execution.PlanIdentity{TenantID: tenant, BusinessID: "2", StrategyID: strategy}}}
	}
	query := PlannedQuery{Requirements: []execution.DataRequirement{
		{Consumers: []execution.DataRequirementConsumer{consumer("tenant-a", "1"), consumer("tenant-a", "2")}},
		{Consumers: []execution.DataRequirementConsumer{consumer("tenant-b", "3"), consumer("tenant-a", "4")}},
	}}
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "the query's")
	primer := &primingAdmission{}
	primeQueryTenants(ctx, primer, query)
	if !reflect.DeepEqual(primer.primed, []string{"tenant-a", "tenant-b"}) {
		t.Fatalf("primed %v, want each Plan tenant once", primer.primed)
	}
	for _, got := range primer.ctxs {
		if got.Value(key{}) != "the query's" {
			t.Fatal("a tenant was primed with a context other than the query's")
		}
	}
	primeQueryTenants(ctx, &noPrimeAdmission{}, query)
}

type noPrimeAdmission struct{}

func (noPrimeAdmission) EnrichFor(string, map[string]json.RawMessage) admission.Facts {
	return admission.Facts{}
}
func (noPrimeAdmission) Admit(admission.PlanContext, *admission.Facts) (bool, string, string) {
	return true, "", ""
}

// The admission production hands a Source is a chain, and a chain primes:
// without this a query's tenants would silently never be primed.
var _ TenantPrimer = (*admission.Chain)(nil)

// primeOrderFuller notes, for every record it fills, whether the record's
// tenant had been primed first.
type primeOrderFuller struct {
	mu       sync.Mutex
	primed   map[string]bool
	filled   int
	unprimed int
}

func (fuller *primeOrderFuller) Name() string { return "prime_order" }
func (fuller *primeOrderFuller) PrimeFor(_ context.Context, tenant string) {
	fuller.mu.Lock()
	defer fuller.mu.Unlock()
	fuller.primed[tenant] = true
}
func (fuller *primeOrderFuller) Fill(_ map[string]json.RawMessage, facts *admission.Facts) {
	fuller.mu.Lock()
	defer fuller.mu.Unlock()
	fuller.filled++
	if !fuller.primed[facts.TenantID] {
		fuller.unprimed++
	}
}

// Through the Source: a query's series are enriched only after their Plans'
// tenant was primed, so a tenant's first read lands, within the query's
// deadline, before the first record of it is filled.
func TestAQuerysSeriesAreEnrichedAfterTheirTenantIsPrimed(t *testing.T) {
	fuller := &primeOrderFuller{primed: map[string]bool{}}
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, fuller}, nil)
	executeScopedThrough(t, chain, nil, "192.0.2.10", "192.0.2.11")
	if fuller.filled == 0 || fuller.unprimed != 0 {
		t.Fatalf("filled %d records, %d before their tenant was primed; want every one after", fuller.filled, fuller.unprimed)
	}
}
