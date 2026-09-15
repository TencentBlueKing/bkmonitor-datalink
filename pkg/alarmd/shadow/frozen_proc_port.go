package shadow

import (
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func frozenProcPortPlan(plan *strategy.CompiledPlan) bool {
	found := false
	for _, level := range plan.Levels() {
		for _, algorithm := range level.Algorithms() {
			if algorithm.Kind() != strategy.DetectorKindProcPort {
				return false
			}
			found = true
		}
	}
	return found
}

func frozenProcPortDetector(algorithm strategy.CompiledAlgorithmPlan, query execution.QueryPlanFacts) (contract.ShadowDetectorConfigV2, error) {
	c, ok := algorithm.ProcPortConfig()
	if !ok || algorithm.Kind() != strategy.DetectorKindProcPort || algorithm.Version() != 1 || len(query.QueryList) != 1 || query.QueryList[0].FieldName != c.SourceMetric {
		return contract.ShadowDetectorConfigV2{}, ErrFrozenConfigUnsupported
	}
	semantic, err := json.Marshal(map[string]string{"value_field": c.ValueField, "source_metric": c.SourceMetric, "nonlisten_field": c.NonListenField, "not_accurate_listen_field": c.NotAccurateListenField, "bind_ip_field": c.BindIPField, "value_mapping": "native-proc-port-result-v1"})
	if err != nil {
		return contract.ShadowDetectorConfigV2{}, err
	}
	return contract.ShadowDetectorConfigV2{Kind: "ProcPort", MappingVersion: "canonical-proc-port-v1", SourceAlgorithmFamily: "proc_port", SourceMappingVersion: "python-proc-port-three-branches-v1", SemanticConfig: semantic}, nil
}
