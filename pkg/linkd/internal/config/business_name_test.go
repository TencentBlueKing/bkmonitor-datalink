package config

import "testing"

func TestCMDBAccessSelectsOneModelForIDOnlyEvents(t *testing.T) {
	t.Parallel()
	source := EnrichConfig{Processors: []EnrichProcessorConfig{{Type: "cmdb-access"}}}
	selected, err := source.SelectResources(ResourcesConfig{OneModel: &OneModelResource{Addresses: []string{"http://onemodel.example:9200"}}})
	if err != nil || selected.OneModel == nil {
		t.Fatalf("business name lookup has no OneModel connection: %v", err)
	}
	if selected.MySQL != nil {
		t.Fatal("ID-only finalization does not require strategy MySQL")
	}
}
