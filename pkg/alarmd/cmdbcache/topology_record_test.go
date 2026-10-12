// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// A topology condition reads the topology Python's fuller wrote, and when it
// wrote none, the record's own bk_obj_id and bk_inst_id (bk-monitor
// c0e828fa8c, bkmonitor/utils/range/target.py:128-141). Python's strategy
// cache adds those two dimensions to a log keyword strategy that has no IP
// target (alarm_backends/core/cache/strategy.py:348-351), so such a strategy
// with a topology target matches by them.

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
)

func TestATopologyConditionReadsTheRecordsOwnObjectWhenNoHostWasPlaced(t *testing.T) {
	chain := instanceChain(t, pythonNamingStore())
	module85 := scopeOf(admission.TargetScopeTopoNode, admission.TargetScopeInclude, "module|85")
	for _, c := range []struct {
		name   string
		record string
		want   bool
	}{
		// No host: the record's node decides.
		{"the record's node is in the target", `{"bk_obj_id":"module","bk_inst_id":85}`, true},
		{"the record's node as text", `{"bk_obj_id":"module","bk_inst_id":"85"}`, true},
		{"the record's node is another", `{"bk_obj_id":"module","bk_inst_id":86}`, false},
		{"only one of the two", `{"bk_obj_id":"module"}`, false},
		// A host the fuller placed writes its chain, which takes precedence:
		// 700002 is in module 91.
		{"a placed host's chain wins", `{"bk_obj_id":"module","bk_inst_id":85,"bk_host_id":"700002"}`, false},
		// 700001 is in module 85 whatever the record's own node says.
		{"a placed host in the target", `{"bk_obj_id":"module","bk_inst_id":86,"bk_host_id":"700001"}`, true},
	} {
		facts := chain.Enrich(jsonDims(t, c.record))
		admit, filter, reason := chain.Admit(module85, &facts)
		if admit != c.want {
			t.Errorf("%s: admit=%v (%s/%s), Python=%v; topo=%v", c.name, admit, filter, reason, c.want, facts.TopoNodes())
		}
	}
}

func jsonDims(t *testing.T, text string) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
