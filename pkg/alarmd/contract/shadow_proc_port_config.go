package contract

import (
	"encoding/json"
	"slices"
)

func validateProcPortDetector(d ShadowDetectorConfigV2) error {
	if d.MappingVersion != "canonical-proc-port-v1" || d.SourceAlgorithmFamily != "proc_port" || d.SourceMappingVersion != "python-proc-port-three-branches-v1" || d.Operator != "" || d.Threshold != "" {
		return invalid("shadow.config.proc_port", "unsupported source or value mapping")
	}
	want := map[string]string{"value_field": "value", "source_metric": "proc_exists", "nonlisten_field": "nonlisten", "not_accurate_listen_field": "not_accurate_listen", "bind_ip_field": "bind_ip", "value_mapping": "native-proc-port-result-v1"}
	fields, err := validateJSONObjectFields(d.SemanticConfig, "shadow.config.proc_port", []string{"value_field", "source_metric", "nonlisten_field", "not_accurate_listen_field", "bind_ip_field", "value_mapping"}, nil, false)
	if err != nil {
		return err
	}
	for key, expected := range want {
		var value string
		if err := json.Unmarshal(fields[key], &value); err != nil || value != expected {
			return invalid("shadow.config.proc_port", "unmapped semantic field")
		}
	}
	return nil
}

func validateProcPortProjection(p ShadowProjectionConfigV2) error {
	if !slices.Equal(p.ValueFields, []string{"value"}) || !slices.Equal(p.IdentityFields, []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}) || !slices.Equal(p.RequiredDimensions, []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"}) {
		return invalid("shadow.config.proc_port", "frozen projection required")
	}
	return nil
}
