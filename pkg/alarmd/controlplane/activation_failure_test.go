package controlplane

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestDrainingQueryGroupTerminatedUsesDoubledReplayAge(t *testing.T) {
	window := DrainingTerminationWindow(10 * time.Minute)
	if window != 20*time.Minute || DrainingTerminationWindow(0) != 0 || DrainingTerminationWindow(-time.Second) != 0 {
		t.Fatalf("termination window=%s, zero=%s", window, DrainingTerminationWindow(0))
	}
	draining := DrainingQueryGroup{QueryGroup: "query-group", RetiredBoundary: 1000}
	for _, test := range []struct {
		name   string
		now    execution.EvaluationTime
		window time.Duration
		want   bool
	}{
		{name: "before the boundary", now: 900, window: window, want: false},
		{name: "at the boundary", now: 1000, window: window, want: false},
		{name: "exactly at the window", now: 1000 + 1200, window: window, want: false},
		{name: "past the window", now: 1000 + 1201, window: window, want: true},
		{name: "no window never terminates", now: 1000 + 1201, window: 0, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := DrainingQueryGroupTerminated(draining, test.now, test.window); got != test.want {
				t.Fatalf("terminated=%t, want %t", got, test.want)
			}
		})
	}
	if DrainingQueryGroupTerminated(DrainingQueryGroup{QueryGroup: "query-group"}, 5000, window) {
		t.Fatal("zero retirement boundary must not terminate")
	}
}

func TestExpectedDrainingProjectionPrunesRetiredEntries(t *testing.T) {
	oldGroups := map[execution.QueryGroupIdentity]QueryGroup{"retiring": {Identity: "retiring"}, "staying": {Identity: "staying"}}
	newGroups := map[execution.QueryGroupIdentity]QueryGroup{"staying": {Identity: "staying"}, "reappearing": {Identity: "reappearing"}}
	previous := []DrainingQueryGroup{
		{QueryGroup: "drained", RetiredBoundary: 90},
		{QueryGroup: "timed-out", RetiredBoundary: 30},
		{QueryGroup: "undrained", RetiredBoundary: 120},
		{QueryGroup: "reappearing", RetiredBoundary: 60},
	}
	reactivating := map[execution.QueryGroupIdentity]struct{}{"reappearing": {}}
	retired := func(draining DrainingQueryGroup) bool {
		return draining.QueryGroup == "drained" || draining.QueryGroup == "timed-out"
	}
	for _, test := range []struct {
		name    string
		retired func(DrainingQueryGroup) bool
		want    []DrainingQueryGroup
	}{
		{name: "nil predicate keeps every previous entry", retired: nil, want: []DrainingQueryGroup{
			{QueryGroup: "drained", RetiredBoundary: 90}, {QueryGroup: "retiring", RetiredBoundary: 180},
			{QueryGroup: "timed-out", RetiredBoundary: 30}, {QueryGroup: "undrained", RetiredBoundary: 120},
		}},
		{name: "retired entries are pruned and the newly retired group is added", retired: retired, want: []DrainingQueryGroup{
			{QueryGroup: "retiring", RetiredBoundary: 180}, {QueryGroup: "undrained", RetiredBoundary: 120},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := expectedDrainingProjection(previous, oldGroups, newGroups, reactivating, 180, test.retired)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("projection=(%+v,%v), want %+v", got, err, test.want)
			}
		})
	}
}

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
