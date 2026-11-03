// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// loadPublishedSnapshot assembles every Query Group of a publication from
// the object catalog, for tests that read back what a bundle activated.
// Production reads only the Query Groups a caller names.
func loadPublishedSnapshot(
	ctx context.Context,
	repository productionCatalogRepository,
	publication controlplane.SnapshotPublicationRef,
) (controlplane.PublishedSnapshot, error) {
	catalog, ok := repository.(*controlplane.RedisCatalogRepository)
	if !ok {
		return controlplane.PublishedSnapshot{}, errors.New("test: repository is not the Redis catalog repository")
	}
	content, err := catalog.LoadPublishedContent(ctx, publication)
	if err != nil {
		return controlplane.PublishedSnapshot{}, err
	}
	identities := make([]execution.QueryGroupIdentity, 0, len(content.Groups))
	for identity := range content.Groups {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i] < identities[j] })
	groups, err := catalog.LoadContentQueryGroups(ctx, content, identities)
	if err != nil {
		return controlplane.PublishedSnapshot{}, err
	}
	ordered := make([]controlplane.QueryGroup, 0, len(identities))
	for _, identity := range identities {
		ordered = append(ordered, groups[identity])
	}
	return controlplane.PublishedSnapshot{Publication: publication, QueryGroups: ordered}, nil
}
