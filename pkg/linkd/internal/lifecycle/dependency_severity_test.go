// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type dependencyLevelTable map[string]string

func (s dependencyLevelTable) Priority(string) (int, bool) { return 1, true }

func (s dependencyLevelTable) KACLevel(name string) (string, error) { return s[name], nil }

type liveDependencySeverity struct {
	table  dependencyLevelTable
	digest string
}

func (*liveDependencySeverity) Priority(string) (int, bool) { return 1, true }

func (s *liveDependencySeverity) FreezeSeverity() (SeverityTable, string) { return s.table, s.digest }

type dependencySeverityObservation struct{ digest, level string }

type configuredDependencyObserver struct {
	digest       string
	observations *[]dependencySeverityObservation
}

func (*configuredDependencyObserver) BindDependency(context.Context, domain.Event, *store.PolicyDecision, domain.Alert) error {
	return nil
}

func (*configuredDependencyObserver) ClearDependency(context.Context, string, string) error {
	return nil
}

func (s *configuredDependencyObserver) WithDependencyConfigDigest(digest string) DependencyRegistry {
	local := *s
	local.digest = digest
	return &local
}

func (s *configuredDependencyObserver) ObserveDependencyAlert(_ context.Context, a domain.Alert, level func(string) (string, error)) error {
	value, err := level(a.Severity)
	if err != nil {
		return err
	}
	*s.observations = append(*s.observations, dependencySeverityObservation{s.digest, value})
	return nil
}

func TestDependencyIndexObservationUsesLiveSeverityWhileBusinessRemainsFrozen(t *testing.T) {
	p := newTestProcessor(t, memory.New(), NoopFinalHook{})
	source := &liveDependencySeverity{table: dependencyLevelTable{"warning": "2"}, digest: "old"}
	p.severity = source
	p.dependencySeverity = source
	observations := []dependencySeverityObservation{}
	p.dependency = &configuredDependencyObserver{observations: &observations}
	table, digest := source.FreezeSeverity()
	frozen := p.withFrozenSeverity(table, digest)
	source.table = dependencyLevelTable{"warning": "1"}
	source.digest = "new"
	if err := frozen.observeDependencyIndex(t.Context(), domain.Alert{Severity: "warning"}); err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].digest != "new" || observations[0].level != "1" {
		t.Fatal("late CAS wrote old config namespace", observations)
	}
	businessLevel, err := frozen.severity.(interface{ KACLevel(string) (string, error) }).KACLevel("warning")
	if err != nil || businessLevel != "2" || frozen.configDigest != "old" {
		t.Fatal("qualification refresh changed business snapshot", businessLevel, frozen.configDigest, err)
	}
}
