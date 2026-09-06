package shadow_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/shadow"
)

func TestFrozenDNFActualCompilerAndGolden(t *testing.T) {
	for name, groups := range map[string]string{
		"or":       `[{"conditions":[{"operator":"GT","threshold_decimal":"0.05"}]},{"conditions":[{"operator":"LTE","threshold_decimal":"-0.05"}]}]`,
		"interval": `[{"conditions":[{"operator":"GTE","threshold_decimal":"30"},{"operator":"LTE","threshold_decimal":"120"}]}]`,
		"five":     `[{"conditions":[{"operator":"EQ","threshold_decimal":"3.0"},{"operator":"EQ","threshold_decimal":"5.0"},{"operator":"EQ","threshold_decimal":"6.0"},{"operator":"EQ","threshold_decimal":"7.0"},{"operator":"EQ","threshold_decimal":"8.0"}]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			due, req, queries := frozenInput(t, func(p *contract.EvaluationPlanV2) {
				a := &p.StrategyIR.Levels[0].DetectPlan.Algorithms[0]
				var cfg map[string]json.RawMessage
				if err := json.Unmarshal(a.Config, &cfg); err != nil {
					t.Fatal(err)
				}
				cfg["groups"] = json.RawMessage(groups)
				a.Config, _ = json.Marshal(cfg)
			})
			before := due.CompiledPlan.Levels()[0].Detectors()[0].Predicate().Facts()
			c, err := shadow.BuildFrozenComparisonConfigV2(due, req, queries)
			if err != nil {
				t.Fatal(err)
			}
			if c.Levels[0].Connector != due.CompiledPlan.Levels()[0].Connector() {
				t.Fatal("Level connector changed")
			}
			d := c.Levels[0].Detectors[0]
			if d.MappingVersion != "canonical-threshold-dnf-v2" || d.Operator != "" || d.Threshold != "" {
				t.Fatal("ambiguous DNF", d)
			}
			var predicate contract.ThresholdDNFConfigV2
			if err = json.Unmarshal(d.SemanticConfig, &predicate); err != nil {
				t.Fatal(err)
			}
			if len(predicate.Groups) != len(before.Children) {
				t.Fatal("OR groups collapsed")
			}
			for i, g := range predicate.Groups {
				if len(g.Conditions) != len(before.Children[i].Children) {
					t.Fatal("AND group collapsed")
				}
				for j, p := range g.Conditions {
					leaf := before.Children[i].Children[j]
					if p.Operator != leaf.Operator || p.Threshold != func() string {
						v, err := contract.NormalizeShadowDecimalV1(leaf.NormalizedThreshold)
						if err != nil {
							t.Fatal(err)
						}
						return v
					}() {
						t.Fatal("actual compiled leaf changed")
					}
				}
			}
			wire, _, err := contract.CanonicalComparisonConfigV2(c)
			if err != nil {
				t.Fatal(err)
			}
			path := "../contract/testdata/shadow-final-v2/frozen_dnf_" + name + ".json"
			if os.Getenv("ALARMD_UPDATE_FROZEN_GOLDEN") == "1" {
				if err = os.WriteFile(path, append(wire, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil || string(want) != string(wire)+"\n" {
				t.Fatalf("actual frozen golden mismatch %s: %v", name, err)
			}
			if _, err = contract.DecodeComparisonConfigV2(wire, len(wire)); err != nil {
				t.Fatal(err)
			}
			c.Levels[0].Detectors[0].SemanticConfig[0] = 'x'
			if !reflect.DeepEqual(before, due.CompiledPlan.Levels()[0].Detectors()[0].Predicate().Facts()) {
				t.Fatal("adapter changed frozen predicate")
			}
		})
	}
}
