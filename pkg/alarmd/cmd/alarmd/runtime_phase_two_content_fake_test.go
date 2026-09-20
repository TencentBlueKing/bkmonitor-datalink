// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The catalog fake describes its snapshots the way the manifest and index
// would: identities and Plans, no content. It answers for the same
// publications, and fails the same way, as its snapshot body read, and
// counts its reads apart from the body's.
func (repository *fakeProductionCatalogRepository) LoadPublishedContent(_ context.Context, publication controlplane.SnapshotPublicationRef) (controlplane.PublishedContent, error) {
	repository.contentLoads++
	if repository.snapshotErr != nil {
		return controlplane.PublishedContent{}, repository.snapshotErr
	}
	snapshot, ok := repository.snapshots[publication]
	if !ok {
		if repository.snapshot.Publication != publication {
			return controlplane.PublishedContent{}, controlplane.ErrSnapshotUnavailable
		}
		snapshot = repository.snapshot
	}
	content := controlplane.PublishedContent{Publication: publication, Groups: make(map[execution.QueryGroupIdentity]controlplane.ContentEntry, len(snapshot.QueryGroups))}
	for _, group := range snapshot.QueryGroups {
		plans := make([]execution.PlanIdentity, 0, len(group.Plans))
		for _, plan := range group.Plans {
			plans = append(plans, plan.Identity)
		}
		content.Groups[group.Identity] = controlplane.ContentEntry{Plans: plans}
	}
	return content, nil
}
