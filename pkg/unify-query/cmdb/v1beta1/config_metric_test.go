// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func TestRelationMetricNameKeepsExplicitAndDynamicRouting(t *testing.T) {
	require.Empty(t, relationMetricName(nil))
	for _, category := range []string{"", "static", "dynamic"} {
		for _, directional := range []bool{false, true} {
			for _, labels := range []map[string]string{nil, {}, {"metric_name": ""}, {"metric_name": "explicit_metric"}} {
				definition := &relation.RelationDefinition{
					Category: category, IsDirectional: directional, FromResource: "node", ToResource: "pod", Labels: labels,
				}
				want := ""
				if category == "dynamic" {
					want = "node_with_pod_relation"
					if directional {
						want = "node_to_pod_flow"
					}
				}
				if labels["metric_name"] != "" {
					want = "explicit_metric"
				}
				require.Equal(t, want, relationMetricName(definition), "%s, directional=%t, labels=%v", category, directional, labels)
			}
		}
	}
}
