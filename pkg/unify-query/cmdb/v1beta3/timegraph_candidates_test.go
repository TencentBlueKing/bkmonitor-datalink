// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

func TestCandidatePlannerKeepsInducedAndAttributeEdges(t *testing.T) {
	var relations []cmdb.Relation
	for _, pair := range [][2]cmdb.Resource{{"A", "B"}, {"A", "C"}, {"B", "C"}, {"B", "B"}, {"X", "A"}, {"C", "D"}, {"X", "Y"}, {"D", "E"}} {
		relations = append(relations, cmdb.Relation{V: []cmdb.Resource{pair[0], pair[1]}})
	}
	require.Equal(t, relations[:5], planSharedTopologyCandidates("A", 1, relations))
	require.Equal(t, relations[:6], planSharedTopologyCandidates("A", 2, relations))
	require.Equal(t, relations[:0], planSharedTopologyCandidates("unknown", 1, relations))
	malformed := append(relations, cmdb.Relation{})
	require.Equal(t, malformed, planSharedTopologyCandidates("A", 1, malformed))
}
