// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The admission chain is a transcription of Python's access filters, and three
// separate transcription defects reached production in one day - an alias that
// should not have been inherited, a coercion that was not applied, and a lookup
// order that was reversed. None was found by a test: each was found afterwards,
// by comparing what the two sides produced.
//
// Reading the Python source more carefully is the same activity that produced
// those defects, so it cannot be the guarantee. This replays a corpus recorded
// from the running Python instead: for each case the corpus carries the raw
// dimensions alarmd would see, the CMDB facts of the hosts involved, the scope
// alarmd actually compiled, and Python's own verdict. A disagreement is then
// computed rather than read.
//
// The corpus holds production identities, so it is not committed. Point
// ALARMD_DIFFERENTIAL_CORPUS at one to run this; without it the test skips.
type differentialCorpus struct {
	SnapshotRevision string                             `json:"snapshot_revision"`
	DisableStates    []string                           `json:"disable_states"`
	CompiledScopes   map[string]*contract.TargetScopeV2 `json:"compiled_scopes"`
	Hosts            map[string]differentialHost        `json:"hosts"`
	Cases            []differentialCase                 `json:"cases"`
	Summary          map[string]int                     `json:"summary"`
}

type differentialHost struct {
	HostID     string   `json:"bk_host_id"`
	IP         string   `json:"bk_host_innerip"`
	CloudID    string   `json:"bk_cloud_id"`
	BusinessID string   `json:"bk_biz_id"`
	State      string   `json:"bk_state"`
	TopoNodes  []string `json:"topo_nodes"`
}

type differentialCase struct {
	StrategyID            string            `json:"strategy_id"`
	RawDimensions         map[string]any    `json:"raw_dimensions"`
	EnrichedTopo          []string          `json:"enriched_topo"`
	PythonInTarget        bool              `json:"python_in_target"`
	PythonIgnoreMonitored bool              `json:"python_ignore_monitoring"`
	_                     map[string]string `json:"-"`
}

// cacheDocument rebuilds the shape the platform's CMDB cache stores, so the
// index under test is built by the same decoder production uses rather than by
// a second one written for the test.
func (host differentialHost) cacheDocument(t *testing.T) string {
	t.Helper()
	chain := make([]map[string]any, 0, len(host.TopoNodes))
	for _, node := range host.TopoNodes {
		object, instance, found := strings.Cut(node, "|")
		if !found {
			continue
		}
		chain = append(chain, map[string]any{"bk_obj_id": object, "bk_inst_id": instance})
	}
	document := map[string]any{
		"bk_host_id": host.HostID, "bk_host_innerip": host.IP, "bk_cloud_id": host.CloudID,
		"bk_biz_id": host.BusinessID, "bk_state": host.State,
		"topo_link": map[string]any{"corpus": chain},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode corpus host: %v", err)
	}
	return string(encoded)
}

func TestTheAdmissionChainAgreesWithTheRunningPython(t *testing.T) {
	path := os.Getenv("ALARMD_DIFFERENTIAL_CORPUS")
	if path == "" {
		t.Skip("set ALARMD_DIFFERENTIAL_CORPUS to a corpus recorded from the running Python")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus differentialCorpus
	if err := json.Unmarshal(payload, &corpus); err != nil {
		t.Fatalf("decode corpus: %v", err)
	}
	// A corpus in which nothing matched its own target proves only that the
	// recording was broken, which is how a previous one-off probe produced a
	// confident wrong answer. Refuse to draw conclusions from it.
	if corpus.Summary["mechanism_usable"] != 1 {
		t.Fatalf("corpus %s recorded no strategy matching its own target; it cannot be used as an oracle", path)
	}
	if len(corpus.Cases) == 0 {
		t.Fatalf("corpus %s carries no cases", path)
	}

	builder := newIndexBuilder(time.Now())
	for _, host := range corpus.Hosts {
		document := host.cacheDocument(t)
		fields := []string{}
		if host.IP != "" {
			fields = append(fields, host.IP+"|"+host.CloudID, document)
		}
		if host.HostID != "" {
			fields = append(fields, host.HostID, document)
		}
		builder.addFields(fields)
	}
	store := &Store{index: builder.index, now: time.Now, maxAge: time.Hour, interval: time.Minute}
	hostStatus, installed := admission.NewHostStatusFilter(corpus.DisableStates)
	if !installed {
		t.Fatalf("corpus carries no disabled states, so the host status half cannot be compared")
	}
	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)},
		[]admission.Filter{admission.TargetScopeFilter{}, hostStatus},
	)

	var compared, scopeMissing int
	disagreements := make([]string, 0, 8)
	for index, testCase := range corpus.Cases {
		// A case without a compiled scope is still worth replaying: the host
		// status half of the chain does not depend on the target, and the one
		// production defect that reached it came from a strategy with no
		// target at all. Only cases whose strategy is absent from the catalog
		// are skipped.
		scope, known := corpus.CompiledScopes[testCase.StrategyID]
		if !known {
			scopeMissing++
			continue
		}
		dimensions := make(map[string]json.RawMessage, len(testCase.RawDimensions))
		for name, value := range testCase.RawDimensions {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("encode dimension %q: %v", name, err)
			}
			dimensions[name] = encoded
		}
		facts := chain.Enrich(dimensions)
		plan := admission.PlanContext{
			StrategyID:  testCase.StrategyID,
			TargetScope: admission.TargetScopeFromContract(scope),
		}
		admitted, _, _ := chain.Admit(plan, &facts)
		want := testCase.PythonInTarget && !testCase.PythonIgnoreMonitored
		compared++
		if admitted == want {
			continue
		}
		if len(disagreements) < 8 {
			disagreements = append(disagreements, fmt.Sprintf(
				"case %d strategy %s: alarmd=%t python=%t (in_target=%t ignore_monitoring=%t) dimensions=%v topo=%v",
				index, testCase.StrategyID, admitted, want,
				testCase.PythonInTarget, testCase.PythonIgnoreMonitored,
				testCase.RawDimensions, facts.TopoNodes))
		}
	}
	t.Logf("corpus %s revision %s: compared %d cases, %d without a compiled scope",
		path, corpus.SnapshotRevision, compared, scopeMissing)
	if compared == 0 {
		t.Fatalf("no case named a strategy the catalog knows; the corpus and the catalog do not line up")
	}
	if len(disagreements) > 0 {
		t.Fatalf("the admission chain disagrees with Python on %d of %d cases:\n%s",
			len(disagreements), compared, strings.Join(disagreements, "\n"))
	}
}
