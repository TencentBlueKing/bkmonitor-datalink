// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.
package v1beta3

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"

// Exact reverse projections share endpoint identities and attributes. Under
// unlimited shared storage their second application needs only the reverse
// edge, not another scan, identity lookup, or node-attribute merge.
func (q *TimeGraph) topologyReversePair(relations []cmdb.Relation) bool {
	if !yoloMode || q.shared == nil || len(relations) != 2 || q.maxNodes > 0 || q.maxEdges > 0 || q.maxNodeInfos > 0 {
		return false
	}
	a, b := relations[0], relations[1]
	if len(a.V) != 2 || len(b.V) != 2 || a.V[0] != b.V[1] || a.V[1] != b.V[0] || a.RelationType != b.RelationType || a.MetricName != b.MetricName || (a.Category != string(RelationCategoryStatic) && a.Category != string(RelationCategoryDynamic)) || b.Category != a.Category {
		return false
	}
	opposite := (a.Direction == string(DirectionOutbound) && b.Direction == string(DirectionInbound)) || (a.Direction == string(DirectionInbound) && b.Direction == string(DirectionOutbound))
	return opposite
}
