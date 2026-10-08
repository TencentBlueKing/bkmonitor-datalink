// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
)

// The evaluator attributes through both the host business and the cluster
// mapping of the one CMDB index lookup. A lookup left out of the pair would
// attribute every event of its kind as a host the cache does not hold, or a
// cluster nobody mapped, and compile without complaint.
func TestBusinessAttributionReadsHostsAndClustersFromTheOneIndexLookup(t *testing.T) {
	index := cmdbcache.NewHostBusinessLookup(nil)
	lookups := businessAttributionLookups(index)
	if lookups.Hosts != index || lookups.Clusters != index || lookups.Namespaces != index {
		t.Fatalf("lookups = %+v, want the index lookup for hosts, clusters and namespaces", lookups)
	}
}
