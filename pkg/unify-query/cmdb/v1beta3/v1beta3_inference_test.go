// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func TestInferSourceTypeTieDependsOnlyOnTheNames(t *testing.T) {
	primary := map[ResourceType][]string{"custom_b": {"id"}, "custom_a": {"id"}, "system": {"id"}}
	for _, resources := range [][]ResourceType{
		{"custom_b", "custom_a"},
		{"custom_a", "custom_b"},
	} {
		model := &Model{schemaProvider: contractSchemaProvider{resources: resources, primary: primary, fields: primary}}
		req, _, err := model.buildTimeGraphRequest(context.Background(), "space", "", "", cmdb.Matcher{"id": "x"}, nil, false, nil)
		require.NoError(t, err)
		require.Equal(t, ResourceType("custom_a"), req.SourceType)
	}
	model := &Model{schemaProvider: contractSchemaProvider{resources: []ResourceType{"custom_a", "system"}, primary: primary, fields: primary}}
	req, _, err := model.buildTimeGraphRequest(context.Background(), "space", "", "", cmdb.Matcher{"id": "x"}, nil, false, nil)
	require.NoError(t, err)
	require.Equal(t, ResourceType("system"), req.SourceType, "built-in resources come before custom ones")
}

// Alert enrichment passes kube-state-metrics dimensions as source_info, where a
// Job is job_name and job may be the scrape job.
func TestInferSourceTypeFromJobName(t *testing.T) {
	primary := map[ResourceType][]string{
		"job": {"bcs_cluster_id", "namespace", "job"},
		"pod": {"bcs_cluster_id", "namespace", "pod"},
	}
	model := &Model{schemaProvider: contractSchemaProvider{resources: []ResourceType{"job", "pod"}, primary: primary, fields: primary}}
	for name, matcher := range map[string]cmdb.Matcher{
		"job_name only":       {"bcs_cluster_id": "c", "namespace": "ns", "job_name": "migrate-1"},
		"with the scrape job": {"bcs_cluster_id": "c", "namespace": "ns", "job_name": "migrate-1", "job": "kube-state-metrics"},
	} {
		t.Run(name, func(t *testing.T) {
			req, _, err := model.buildTimeGraphRequest(context.Background(), "space", "", "", matcher, nil, false, nil)
			require.NoError(t, err)
			require.Equal(t, ResourceType("job"), req.SourceType)
			require.Equal(t, "migrate-1", req.SourceInfo["job"])
		})
	}
}
