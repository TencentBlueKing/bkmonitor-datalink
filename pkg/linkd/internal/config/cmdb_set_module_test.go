package config

import (
	"encoding/json"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestCMDBSetModuleConfiguration(t *testing.T) {
	for _, input := range []string{`{}`, `{"cmdb_set_module_enabled":false}`, `{"cmdb_set_module_enabled":true}`} {
		var c EnrichConfig
		if err := json.Unmarshal([]byte(input), &c); err != nil {
			t.Fatal(err)
		}
		want := input == `{"cmdb_set_module_enabled":true}`
		if c.CMDBSetModuleEnabled != want || c.clone().CMDBSetModuleEnabled != want {
			t.Fatalf("config=%#v", c)
		}
		encoded, err := yaml.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var decoded EnrichConfig
		if err := yaml.Unmarshal(encoded, &decoded); err != nil || decoded.CMDBSetModuleEnabled != want {
			t.Fatalf("YAML round trip=%#v, %v", decoded, err)
		}
		encoded, err = json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.CMDBSetModuleEnabled != want {
			t.Fatalf("JSON round trip=%#v, %v", decoded, err)
		}
	}
	var c EnrichConfig
	if json.Unmarshal([]byte(`{"cmdb_set_module_enabled":"false"}`), &c) == nil {
		t.Fatal("non-boolean switch accepted")
	}
}
