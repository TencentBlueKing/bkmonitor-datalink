// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

var ErrSelectedStrategyUnavailable = errors.New("alarmd controlplane: selected strategy is not active")

// ActiveIDStrategySourceView is a temporary G1 execution view. Source reads
// remain canonical and complete; SourceReconciler invokes the selection method
// only after the stable observation, immediately before normal compilation.
type ActiveIDStrategySourceView struct {
	source   StrategySource
	selected map[string]struct{}
}

func NewActiveIDStrategySourceView(source StrategySource, selected []string) (*ActiveIDStrategySourceView, error) {
	if source == nil || len(selected) == 0 {
		return nil, errors.New("alarmd controlplane: active-ID source view is incomplete")
	}
	canonical, err := canonicalActiveSet(selected)
	if err != nil {
		return nil, err
	}
	view := &ActiveIDStrategySourceView{source: source, selected: make(map[string]struct{}, len(canonical))}
	for _, id := range canonical {
		view.selected[id] = struct{}{}
	}
	return view, nil
}

func (view *ActiveIDStrategySourceView) ActiveStrategyIDs(ctx context.Context) ([]string, error) {
	if view == nil || view.source == nil || len(view.selected) == 0 {
		return nil, errors.New("alarmd controlplane: active-ID source view is required")
	}
	active, err := view.source.ActiveStrategyIDs(ctx)
	if err != nil {
		return nil, err
	}
	active, err = canonicalActiveSet(active)
	if err != nil {
		return nil, err
	}
	selectedCount := 0
	for _, id := range active {
		if _, selected := view.selected[id]; selected {
			selectedCount++
		}
	}
	if selectedCount != len(view.selected) {
		return nil, fmt.Errorf("%w: selected=%d active=%d", ErrSelectedStrategyUnavailable, len(view.selected), selectedCount)
	}
	return active, nil
}

func (view *ActiveIDStrategySourceView) Strategies(ctx context.Context, ids []string) ([]SourceStrategy, error) {
	if view == nil || view.source == nil || len(view.selected) == 0 {
		return nil, errors.New("alarmd controlplane: active-ID source view is required")
	}
	return view.source.Strategies(ctx, ids)
}

// SelectExecutionStrategies narrows a complete stable Source observation for
// compilation while retaining canonical Source health dispositions from
// unselected objects. It never rewrites strategy contents or identities.
func (view *ActiveIDStrategySourceView) SelectExecutionStrategies(
	strategies []SourceStrategy,
) ([]SourceStrategy, []ObjectDisposition, error) {
	if view == nil || view.source == nil || len(view.selected) == 0 || strategies == nil {
		return nil, nil, errors.New("alarmd controlplane: active-ID source view is required")
	}
	selected := make([]SourceStrategy, 0, len(view.selected))
	audit := make([]ObjectDisposition, 0, len(strategies))
	found := make(map[string]struct{}, len(view.selected))
	for _, strategy := range strategies {
		if _, ok := view.selected[strategy.SourceID]; ok {
			if strategy.SourceDisposition != nil {
				return nil, nil, fmt.Errorf("%w: selected object %s is incomplete", ErrSelectedStrategyUnavailable, strategy.SourceID)
			}
			selected = append(selected, strategy)
			found[strategy.SourceID] = struct{}{}
			continue
		}
		if strategy.SourceDisposition != nil {
			disposition := *strategy.SourceDisposition
			if disposition.SourceID == "" {
				disposition.SourceID = strategy.SourceID
			}
			audit = append(audit, disposition)
			continue
		}
	}
	if len(found) != len(view.selected) {
		return nil, nil, fmt.Errorf("%w: selected=%d observed=%d", ErrSelectedStrategyUnavailable, len(view.selected), len(found))
	}
	sort.Slice(audit, func(i, j int) bool { return lessDisposition(audit[i], audit[j]) })
	return selected, audit, nil
}

// SelectLastGood projects persisted execution facts to the current temporary
// G1 selection. It prevents a selector change from turning a previously
// selected strategy into a removal or retained last-good candidate.
func (view *ActiveIDStrategySourceView) SelectLastGood(snapshot *PublishedSnapshot) *PublishedSnapshot {
	if view == nil || snapshot == nil {
		return snapshot
	}
	projected := *snapshot
	projected.QueryGroups = make([]QueryGroup, 0, len(snapshot.QueryGroups))
	for _, group := range snapshot.QueryGroups {
		selectedGroup := group
		selectedGroup.Plans = make([]FrozenPlan, 0, len(group.Plans))
		for _, plan := range group.Plans {
			if _, selected := view.selected[plan.Identity.StrategyID]; selected {
				selectedGroup.Plans = append(selectedGroup.Plans, plan)
			}
		}
		if len(selectedGroup.Plans) > 0 {
			projected.QueryGroups = append(projected.QueryGroups, selectedGroup)
		}
	}
	return &projected
}

// ValidateCompiledCatalog requires every explicitly selected strategy to have
// entered the normal compiler path. This prevents ordinary last-good retention
// or a healthy selected sibling from hiding an invalid G1 target.
func (view *ActiveIDStrategySourceView) ValidateCompiledCatalog(catalog Catalog) error {
	if view == nil || len(view.selected) == 0 {
		return errors.New("alarmd controlplane: active-ID source view is required")
	}
	accepted := make(map[string]struct{}, len(view.selected))
	for _, disposition := range catalog.Dispositions {
		if disposition.Disposition == DispositionAccepted {
			if _, selected := view.selected[disposition.SourceID]; selected {
				accepted[disposition.SourceID] = struct{}{}
			}
		}
	}
	if len(accepted) != len(view.selected) {
		return fmt.Errorf("%w: selected=%d compiled=%d", ErrSelectedStrategyUnavailable, len(view.selected), len(accepted))
	}
	if len(catalog.QueryGroups) != 1 {
		return fmt.Errorf("%w: selected strategies compiled to %d Query Groups", ErrSelectedStrategyUnavailable, len(catalog.QueryGroups))
	}
	return nil
}
