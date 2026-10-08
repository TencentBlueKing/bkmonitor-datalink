package description

import (
	"encoding/json"
	"os"
	"testing"
)

// fixture 的 expected 来自指定 bk-monitor 源码中的真实 serializer、检测类、
// Django 模板和单位实现；生成脚本不执行 Linkd，也不查询任何服务。
func TestBKMonitorSourceDescriptionOracle(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/bkmonitor_descriptions.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Commit string `json:"commit"`
		Cases  []struct {
			Name       string             `json:"name"`
			ItemName   string             `json:"item_name"`
			Unit       string             `json:"unit"`
			Value      json.RawMessage    `json:"value"`
			Connector  string             `json:"connector"`
			Algorithms []RuntimeAlgorithm `json:"algorithms"`
			Expected   *string            `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || fixture.Commit == "" || len(fixture.Cases) == 0 {
		t.Fatalf("invalid oracle fixture: %v", err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			value, err := ParseNumber(tc.Value)
			if err != nil {
				t.Fatal(err)
			}
			facts := Facts{ItemName: tc.ItemName, Unit: tc.Unit, Value: value, Connector: tc.Connector}
			for _, compiled := range tc.Algorithms {
				algorithm := Algorithm{Type: compiled.Type, UnitPrefix: compiled.UnitPrefix}
				if compiled.Type == "Threshold" {
					algorithm.Groups, err = configurationThreshold(compiled.Config)
					if err != nil {
						t.Fatal(err)
					}
				}
				facts.Algorithms = append(facts.Algorithms, algorithm)
			}
			got, err := Render(facts)
			if tc.Expected == nil {
				if err == nil || got != "" {
					t.Fatalf("unexpected match: %q error=%v", got, err)
				}
			} else if err != nil || got != *tc.Expected {
				t.Fatalf("got=%q want=%q error=%v", got, *tc.Expected, err)
			}
		})
	}
}
