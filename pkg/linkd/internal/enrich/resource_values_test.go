package enrich

import (
	"testing"

	"linkd/internal/enrich/rules"
)

func TestResourceCloudContextBelongsToHost(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, modelCode string
		instanceCloud   bool
	}{
		{name: "host instance cloud", modelCode: rules.HostModelCode, instanceCloud: true},
		{name: "host cloud fallback", modelCode: rules.HostModelCode},
		{name: "MySQL instance cloud", modelCode: "cw-MySQL", instanceCloud: true},
		{name: "MySQL must not inherit host cloud", modelCode: "cw-MySQL"},
		{name: "service instance cloud", modelCode: "cw-service_instance", instanceCloud: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instance := Instance{ModelCode: tc.modelCode, InstanceID: "22"}
			if tc.instanceCloud {
				instance.Attributes = map[string]any{"bk_cloud_id": int64(0), "bk_cloud_name": "Default Area"}
			}
			values := ResourceValuesFromInstance(instance, 2)
			host := Instance{ModelCode: rules.HostModelCode, InstanceID: "101", Attributes: map[string]any{
				"bk_cloud_id": int64(0), "bk_cloud_name": "Default Area",
			}}
			ApplyHostCloudContext(&values, host)
			if tc.modelCode == rules.HostModelCode {
				if values.BKCloudID != int64(0) || values.BKCloudName != "Default Area" {
					t.Fatalf("host cloud context lost: %+v", values)
				}
			} else if values.BKCloudID != nil || values.BKCloudName != "" {
				t.Fatalf("component inherited cloud context: %+v", values)
			}
		})
	}
}
