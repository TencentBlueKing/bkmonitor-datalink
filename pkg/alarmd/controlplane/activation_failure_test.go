package controlplane

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestActivationReconciliationCountsIncludesBoundedSortedReappearedSamples(t *testing.T) {
	draining := make([]DrainingQueryGroup, 0, 10)
	newGroups := make(map[execution.QueryGroupIdentity]QueryGroup, 10)
	for index := 9; index >= 0; index-- {
		identity := execution.QueryGroupIdentity(fmt.Sprintf("query-group-%02d", index))
		draining = append(draining, DrainingQueryGroup{QueryGroup: identity})
		newGroups[identity] = QueryGroup{Identity: identity}
	}

	got := activationReconciliationCounts(draining, newGroups)
	wantSamples := []execution.QueryGroupIdentity{
		"query-group-00", "query-group-01", "query-group-02", "query-group-03",
		"query-group-04", "query-group-05", "query-group-06", "query-group-07",
	}
	if got.ReappearedQueryGroups != 10 || !got.ReappearedQueryGroupSamplesTruncated ||
		!reflect.DeepEqual(got.ReappearedQueryGroupSamples, wantSamples) {
		t.Fatalf("reappeared diagnostics=%#v, want count=10 samples=%v truncated=true", got, wantSamples)
	}
}

func TestActivationReconciliationCountsIncludesAllThreeReappearedIdentities(t *testing.T) {
	draining := []DrainingQueryGroup{
		{QueryGroup: "query-group-c"},
		{QueryGroup: "query-group-not-reappeared"},
		{QueryGroup: "query-group-a"},
		{QueryGroup: "query-group-b"},
	}
	newGroups := map[execution.QueryGroupIdentity]QueryGroup{
		"query-group-a": {Identity: "query-group-a"},
		"query-group-b": {Identity: "query-group-b"},
		"query-group-c": {Identity: "query-group-c"},
	}

	got := activationReconciliationCounts(draining, newGroups)
	wantSamples := []execution.QueryGroupIdentity{"query-group-a", "query-group-b", "query-group-c"}
	if got.ReappearedQueryGroups != 3 || got.ReappearedQueryGroupSamplesTruncated ||
		!reflect.DeepEqual(got.ReappearedQueryGroupSamples, wantSamples) {
		t.Fatalf("reappeared diagnostics=%#v, want count=3 samples=%v truncated=false", got, wantSamples)
	}
}
