// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func TestSelectLegacyPathPrefersFirstPopulatedResult(t *testing.T) {
	empty := pathQueryResult{path: []string{"a", "b"}, ts: []cmdb.MatchersWithTimestamp{}}
	first := pathQueryResult{path: []string{"a", "c", "b"}, ts: []cmdb.MatchersWithTimestamp{{Timestamp: 1000, Matchers: cmdb.Matchers{{"id": "one"}}}}}
	later := pathQueryResult{path: []string{"a", "d", "b"}, ts: []cmdb.MatchersWithTimestamp{{Timestamp: 2000, Matchers: cmdb.Matchers{{"id": "two"}}}}}
	for _, tc := range []struct {
		name    string
		results []pathQueryResult
		want    pathQueryResult
	}{
		{"none", nil, pathQueryResult{}},
		{"only-empty", []pathQueryResult{empty}, empty},
		{"last-empty", []pathQueryResult{{path: []string{"a", "e", "b"}}, empty}, empty},
		{"first-data", []pathQueryResult{empty, first, later}, first},
		{"trailing-empty", []pathQueryResult{first, empty}, first},
		{"first-of-many", []pathQueryResult{first, later}, first},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, selectLegacyPathResult(tc.results)) })
	}
}
