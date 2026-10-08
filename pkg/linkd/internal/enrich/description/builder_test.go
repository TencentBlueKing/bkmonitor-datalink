package description

import (
	"context"
	"errors"
	"testing"

	"linkd/internal/domain"
)

type resolverFunc func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (Facts, error)

func (f resolverFunc) Resolve(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (Facts, error) {
	return f(ctx, event, evaluation, opening)
}

func TestBuilderValidatesIdentityAndPropagatesFailure(t *testing.T) {
	evaluation := domain.EventEvaluation{Severity: "warning", Action: domain.EventActionTriggered}
	event := domain.Event{BKTenantID: "tenant", EventID: "event", EventSourceID: "source", EventSourceVersion: 1, Fingerprint: "fingerprint", Evaluations: []domain.EventEvaluation{evaluation}, Labels: domain.DimensionMap{}}
	opening := domain.Alert{BKTenantID: "tenant", EventSourceID: "source", EventSourceVersion: 1, Fingerprint: "fingerprint", TriggerEventID: "event", Severity: "warning"}
	calls := 0
	dependencyError := errors.New("dependency unavailable")
	b, err := NewBuilder(resolverFunc(func(_ context.Context, e domain.Event, _ domain.EventEvaluation, _ domain.Alert) (Facts, error) {
		calls++
		e.Labels["mutation"] = domain.NewStringScalar("bad")
		return Facts{}, dependencyError
	}))
	if err != nil {
		t.Fatal(err)
	}
	if content, err := b.BuildContent(context.Background(), event, evaluation, opening); content != "" || !errors.Is(err, dependencyError) {
		t.Fatalf("content %q error %v", content, err)
	}
	if len(event.Labels) != 0 {
		t.Fatal("resolver mutated source")
	}
	for _, change := range []func(*domain.Alert){func(a *domain.Alert) { a.BKTenantID = "other" }, func(a *domain.Alert) { a.EventSourceVersion = 2 }, func(a *domain.Alert) { a.TriggerEventID = "other" }, func(a *domain.Alert) { a.Severity = "critical" }} {
		a := opening
		change(&a)
		if _, err := b.BuildContent(context.Background(), event, evaluation, a); err == nil {
			t.Fatal("accepted mismatched opening")
		}
	}
	if calls != 1 {
		t.Fatal("invalid identity reached resolver")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.BuildContent(ctx, event, evaluation, opening); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := NewBuilder(nil); err == nil {
		t.Fatal("accepted missing resolver")
	}
}

func TestBuilderRendersFrozenFactsAndNeverUsesSourceContent(t *testing.T) {
	evaluation := domain.EventEvaluation{Severity: "warning", Action: domain.EventActionTriggered}
	event := domain.Event{BKTenantID: "tenant", EventID: "event", EventSourceID: "source", EventSourceVersion: 1, Fingerprint: "fingerprint", Evaluations: []domain.EventEvaluation{evaluation}, Content: "untrusted source text"}
	opening := domain.Alert{BKTenantID: "tenant", EventSourceID: "source", EventSourceVersion: 1, Fingerprint: "fingerprint", TriggerEventID: "event", Severity: "warning", Content: "untrusted source text"}
	b, err := NewBuilder(resolverFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (Facts, error) {
		return Facts{ItemName: "AVG(CPU)", Unit: "percent", Value: number(t, "99"), Connector: "and", Algorithms: []Algorithm{{Type: "Threshold", UnitPrefix: "%", Groups: [][]Condition{{{"gt", 80}}}}}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.BuildContent(context.Background(), event, evaluation, opening)
	if err != nil || got != "AVG(CPU) > 80.0%, 当前值99%" {
		t.Fatalf("content %q error %v", got, err)
	}
}
