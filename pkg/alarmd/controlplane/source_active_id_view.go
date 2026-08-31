// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package controlplane

import (
	"context"
	"errors"
	"fmt"
)

var ErrSelectedStrategyUnavailable = errors.New("alarmd controlplane: selected strategy is not active")

// ActiveIDStrategySourceView is a temporary G1 execution view. It always reads
// and validates the canonical active set before narrowing the IDs passed to the
// normal source/compiler path. It does not rewrite source objects or identities.
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
	result := make([]string, 0, len(view.selected))
	for _, id := range active {
		if _, selected := view.selected[id]; selected {
			result = append(result, id)
		}
	}
	if len(result) != len(view.selected) {
		return nil, fmt.Errorf("%w: selected=%d active=%d", ErrSelectedStrategyUnavailable, len(view.selected), len(result))
	}
	return result, nil
}

func (view *ActiveIDStrategySourceView) Strategies(ctx context.Context, ids []string) ([]SourceStrategy, error) {
	if view == nil || view.source == nil || len(view.selected) == 0 {
		return nil, errors.New("alarmd controlplane: active-ID source view is required")
	}
	canonical, err := canonicalActiveSet(ids)
	if err != nil {
		return nil, err
	}
	for _, id := range canonical {
		if _, selected := view.selected[id]; !selected {
			return nil, errors.New("alarmd controlplane: strategy request is outside the selected execution view")
		}
	}
	return view.source.Strategies(ctx, canonical)
}
