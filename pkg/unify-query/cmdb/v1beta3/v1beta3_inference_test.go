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
// Job is job_name and job may be the scrape job. The built-in schema is used so
// that the real primary keys and inference order are what is tested.
func TestInferSourceTypeFromJobName(t *testing.T) {
	model := &Model{schemaProvider: NewStaticSchemaProvider()}
	for name, c := range map[string]struct {
		source   cmdb.Resource
		matcher  cmdb.Matcher
		wantType ResourceType
		wantInfo map[string]string
	}{
		"job_name only": {
			matcher:  cmdb.Matcher{"bcs_cluster_id": "c", "namespace": "ns", "job_name": "migrate-1"},
			wantType: "job",
			wantInfo: map[string]string{"bcs_cluster_id": "c", "namespace": "ns", "job": "migrate-1"},
		},
		"with the scrape job": {
			matcher:  cmdb.Matcher{"bcs_cluster_id": "c", "namespace": "ns", "job_name": "migrate-1", "job": "kube-state-metrics"},
			wantType: "job",
			wantInfo: map[string]string{"bcs_cluster_id": "c", "namespace": "ns", "job": "migrate-1"},
		},
		"explicit job source with the scrape job": {
			source:   "job",
			matcher:  cmdb.Matcher{"bcs_cluster_id": "c", "namespace": "ns", "job_name": "migrate-1", "job": "kube-state-metrics"},
			wantType: "job",
			wantInfo: map[string]string{"bcs_cluster_id": "c", "namespace": "ns", "job": "migrate-1"},
		},
		"a pod that also carries job_name stays a pod": {
			matcher:  cmdb.Matcher{"bcs_cluster_id": "c", "namespace": "ns", "pod_name": "migrate-1-x", "job_name": "migrate-1"},
			wantType: "pod",
			wantInfo: map[string]string{"bcs_cluster_id": "c", "namespace": "ns", "pod": "migrate-1-x"},
		},
		"a container that also carries job_name stays a container": {
			matcher:  cmdb.Matcher{"bcs_cluster_id": "c", "namespace": "ns", "pod_name": "migrate-1-x", "container_name": "main", "job_name": "migrate-1"},
			wantType: "container",
			wantInfo: map[string]string{"bcs_cluster_id": "c", "namespace": "ns", "pod": "migrate-1-x", "container": "main"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			req, _, err := model.buildTimeGraphRequest(context.Background(), "space", "", c.source, c.matcher, nil, false, nil)
			require.NoError(t, err)
			require.Equal(t, c.wantType, req.SourceType)
			require.Equal(t, c.wantInfo, req.SourceInfo)
		})
	}
}
