// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTargetExclusionsRemainVisibleInSourceAndPublishedEvidence(t *testing.T) {
	for _, tt := range []struct {
		raw    string
		policy *policy
		want   string
	}{
		{`{"items":[{"target_plan":{"exclude":[{"bk_host_id":101,"secret":"hidden"}]}}]}`, sourcePolicy, `"exclude":[{"bk_host_id":101}]`},
		{`{"plans":[{"target_plan":{"exclude_keys":["101"],"exclude_members":[{"model_id":"cw-Host","model_inst_id":"102"}]}}]}`, publishedPolicy, `"exclude_keys":["101"],"exclude_members":[{"model_id":"cw-Host","model_inst_id":"102"}]`},
		{`{"plans":[{"target_plan":{"exclude_hosts":["101"]}}]}`, publishedPolicy, `"exclude_hosts":["101"]`},
	} {
		projected, _, err := projectJSON([]byte(tt.raw), tt.policy)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(projected)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), tt.want) || strings.Contains(string(encoded), "hidden") {
			t.Fatalf("exclusion evidence lost or leaked fields: %s", encoded)
		}
	}
}
