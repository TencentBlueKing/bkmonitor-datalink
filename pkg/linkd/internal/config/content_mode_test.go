package config

import (
	"encoding/json"
	"testing"
)

func TestContentModeConfigurationAndResources(t *testing.T) {
	for _, mode := range []string{"", ContentModeSource, ContentModeBKMonitorDescription} {
		c := EnrichConfig{ContentMode: mode}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var decoded EnrichConfig
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.ContentMode != mode || c.clone().ContentMode != mode {
			t.Fatal("content mode lost through configuration boundary")
		}
		selected, err := c.SelectResources(*validResourcesConfig())
		if err != nil {
			t.Fatal(err)
		}
		if (selected.MySQL != nil) != (mode == ContentModeBKMonitorDescription) || selected.OneModel != nil || selected.KingeyeDisplay != nil {
			t.Fatal("content mode selected unrelated connections")
		}
		_, err = c.SelectResources(ResourcesConfig{})
		if (err != nil) != (mode == ContentModeBKMonitorDescription) {
			t.Fatal("description did not require database with empty processor chain")
		}
	}
	for _, mode := range []string{"latest", "bkmonitor_description ", "SOURCE"} {
		c := EnrichConfig{ContentMode: mode}
		if c.Validate() == nil {
			t.Fatal("unknown content mode accepted")
		}
		if _, err := c.SelectResources(*validResourcesConfig()); err == nil {
			t.Fatal("unknown mode acquired connections")
		}
	}
}
