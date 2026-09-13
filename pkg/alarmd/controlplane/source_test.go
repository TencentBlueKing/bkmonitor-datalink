package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

func TestObserveStableReadsActiveSetAroundDetails(t *testing.T) {
	source := &fakeSource{active: [][]string{{"1002", "1001"}, {"1001", "1002"}, {"1001", "1002"}, {"1002", "1001"}}, documents: map[string]controlplane.SourceStrategy{
		"1001": {SourceID: "1001", Document: json.RawMessage(`{"id":1001,"bk_biz_id":2,"items":[{}]}`), Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
		"1002": {SourceID: "1002", Document: json.RawMessage(`{"id":1002,"bk_biz_id":2,"items":[{}]}`), Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
	}}
	got, err := controlplane.ObserveStable(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if got.ObservationID == "" || got.ObservationID == "observation-1" || len(got.Strategies) != 2 || source.detailCalls != 2 {
		t.Fatalf("observation=%#v calls=%d", got, source.detailCalls)
	}
}

func TestObserveStableAcceptsEmptyActiveSet(t *testing.T) {
	source := &fakeSource{active: [][]string{{}, {}, {}, {}}, documents: map[string]controlplane.SourceStrategy{}}
	got, err := controlplane.ObserveStable(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if got.ObservationID == "" || got.Strategies == nil || len(got.Strategies) != 0 || source.detailCalls != 2 {
		t.Fatalf("empty observation=%#v calls=%d", got, source.detailCalls)
	}
}

func TestObserveStableRejectsMixedRefresh(t *testing.T) {
	source := &fakeSource{active: [][]string{{"1001"}, {"1001", "1002"}}, documents: map[string]controlplane.SourceStrategy{"1001": {SourceID: "1001", Document: json.RawMessage(`{}`)}}}
	_, err := controlplane.ObserveStable(context.Background(), source)
	if !errors.Is(err, controlplane.ErrObservationUnstable) {
		t.Fatalf("error=%v", err)
	}
}

func TestObserveStableRejectsChangedObjectOnConfirmationPoll(t *testing.T) {
	source := &sequenceSource{active: []string{"1001"}, documents: [][]controlplane.SourceStrategy{
		{{SourceID: "1001", Document: json.RawMessage(`{"id":1001,"version":1}`)}},
		{{SourceID: "1001", Document: json.RawMessage(`{"id":1001,"version":2}`)}},
	}}
	_, err := controlplane.ObserveStable(context.Background(), source)
	if !errors.Is(err, controlplane.ErrObservationUnstable) {
		t.Fatalf("error=%v", err)
	}
}

func TestObserveStableRejectsChangedExplicitIdentityFact(t *testing.T) {
	document := json.RawMessage(`{"id":1001,"bk_biz_id":2,"items":[{}]}`)
	source := &sequenceSource{active: []string{"1001"}, documents: [][]controlplane.SourceStrategy{
		{{SourceID: "1001", Document: document, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		{{SourceID: "1001", Document: document, Identity: controlplane.SourceIdentity{TenantID: "tenant-b", BusinessID: "2", SpaceScope: "bkcc__2"}}},
	}}
	_, err := controlplane.ObserveStable(context.Background(), source)
	if !errors.Is(err, controlplane.ErrObservationUnstable) {
		t.Fatalf("error=%v", err)
	}
}

type sequenceSource struct {
	active    []string
	documents [][]controlplane.SourceStrategy
	details   int
}

func (s *sequenceSource) ActiveStrategyIDs(context.Context) ([]string, error) {
	return append([]string(nil), s.active...), nil
}
func (s *sequenceSource) Strategies(context.Context, []string) ([]controlplane.SourceStrategy, error) {
	value := s.documents[s.details]
	s.details++
	return value, nil
}

type fakeSource struct {
	active                   [][]string
	activeCalls, detailCalls int
	documents                map[string]controlplane.SourceStrategy
}

func (s *fakeSource) ActiveStrategyIDs(context.Context) ([]string, error) {
	value := s.active[s.activeCalls]
	s.activeCalls++
	return append([]string(nil), value...), nil
}
func (s *fakeSource) Strategies(_ context.Context, ids []string) ([]controlplane.SourceStrategy, error) {
	s.detailCalls++
	result := make([]controlplane.SourceStrategy, 0, len(ids))
	for _, id := range ids {
		result = append(result, s.documents[id])
	}
	return result, nil
}
