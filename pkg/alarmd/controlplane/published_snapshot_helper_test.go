// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// loadPublishedSnapshot assembles every Query Group of a publication from
// the object catalog, in identity order, the way the source reconciler does
// once after a restart. It exists for tests that compare the content that
// was published; production reads only the Query Groups a caller names.
func loadPublishedSnapshot(
	ctx context.Context,
	repository *controlplane.RedisCatalogRepository,
	publication controlplane.SnapshotPublicationRef,
) (controlplane.PublishedSnapshot, error) {
	content, err := repository.LoadPublishedContent(ctx, publication)
	if err != nil {
		return controlplane.PublishedSnapshot{}, err
	}
	identities := make([]execution.QueryGroupIdentity, 0, len(content.Groups))
	for identity := range content.Groups {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i] < identities[j] })
	groups, err := repository.LoadContentQueryGroups(ctx, content, identities)
	if err != nil {
		return controlplane.PublishedSnapshot{}, err
	}
	ordered := make([]controlplane.QueryGroup, 0, len(identities))
	for _, identity := range identities {
		ordered = append(ordered, groups[identity])
	}
	return controlplane.PublishedSnapshot{SchemaVersion: "alarmd-control-snapshot-v1", Publication: publication, QueryGroups: ordered}, nil
}
