package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// MonitorOutputIdentity freezes the source record's identity fields, not the
// algorithm's projected value fields. It does not affect runtime state identity.
type MonitorOutputIdentity struct {
	DimensionFields []string `json:"dimension_fields"`
}

// MonitorDedupeMD5 projects MonitorEventAdapter.extract_target and Event's
// default dedupe fields. Native identities support scalar/null dimensions;
// structured Python tags must be cleaned on the Python capture side first.
func MonitorDedupeMD5(strategyID, businessID string, dimensions map[string]json.RawMessage, identity MonitorOutputIdentity) (string, error) {
	agg := make(map[string]bool, len(identity.DimensionFields))
	for _, field := range identity.DimensionFields {
		agg[field] = true
	}
	data := make(map[string]json.RawMessage)
	for key, raw := range dimensions {
		if (!agg[key] && key != "__NO_DATA_DIMENSION__") || key == "__additional_dimensions" {
			continue
		}
		name := strings.TrimPrefix(key, "tags.")
		if _, exists := data[name]; exists {
			return "", fmt.Errorf("monitor dedupe: ambiguous dimension alias %q", name)
		}
		_, err := monitorScalar(raw)
		if err != nil {
			return "", fmt.Errorf("monitor dedupe dimension %s: %w", key, err)
		}
		data[name] = raw
	}
	for key := range data {
		if strings.HasPrefix(key, "tags.") {
			if _, exists := data[strings.TrimPrefix(key, "tags.")]; exists {
				return "", fmt.Errorf("monitor dedupe: ambiguous repeated tags prefix %q", key)
			}
		}
	}
	pop := func(key string) (json.RawMessage, bool) { value, ok := data[key]; delete(data, key); return value, ok }
	stringValue := func(raw json.RawMessage) string { v, _ := monitorScalar(raw); s, _ := pythonScalarString(v); return s }
	encodeString := func(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
	targetType, target := "", json.RawMessage("null")
	if raw, exists := data["bk_host_id"]; exists && monitorTruthy(raw) {
		pop("bk_host_id")
		targetType, target = "HOST", encodeString(stringValue(raw))
	} else if agg["bk_target_ip"] || agg["ip"] {
		key := "ip"
		if agg["bk_target_ip"] {
			key = "bk_target_ip"
		}
		if ip, exists := pop(key); exists {
			cloudKey := "bk_cloud_id"
			if key == "bk_target_ip" {
				cloudKey = "bk_target_cloud_id"
			}
			cloud, exists := pop(cloudKey)
			if key == "bk_target_ip" && (!exists || bytes.Equal(bytes.TrimSpace(cloud), []byte("null"))) {
				cloud, exists = pop("bk_cloud_id")
			}
			text := stringValue(ip)
			if exists && !bytes.Equal(bytes.TrimSpace(cloud), []byte("null")) {
				text += "|" + stringValue(cloud)
			}
			targetType, target = "HOST", encodeString(text)
		}
	} else if agg["bk_target_service_instance_id"] || agg["bk_service_instance_id"] {
		key := "bk_service_instance_id"
		if agg["bk_target_service_instance_id"] {
			key = "bk_target_service_instance_id"
		}
		if value, exists := pop(key); exists {
			targetType, target = "SERVICE", value
		}
	} else if obj, exists := data["bk_obj_id"]; exists {
		if instance, exists := data["bk_inst_id"]; exists {
			pop("bk_obj_id")
			pop("bk_inst_id")
			targetType, target = "TOPO", encodeString(stringValue(obj)+"|"+stringValue(instance))
		}
	}
	// K8s/APM target values are reset to empty/None by cal_dedupe_md5.
	strategy, err := strconv.ParseInt(strategyID, 10, 64)
	if err != nil || strategy <= 0 {
		return "", fmt.Errorf("monitor dedupe: invalid strategy ID")
	}
	business, err := strconv.ParseInt(businessID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("monitor dedupe: invalid business ID")
	}
	values := []json.RawMessage{json.RawMessage(strconv.FormatInt(strategy, 10)), encodeString(targetType), target, json.RawMessage(strconv.FormatInt(business, 10))}
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		// get_field("tags."+key) and get_tags_value each strip a prefix.
		value, exists := data[strings.TrimPrefix(key, "tags.")]
		if !exists {
			value = json.RawMessage("null")
		}
		values = append(values, value)
	}
	return PythonDedupeMD5(values)
}

func monitorScalar(raw json.RawMessage) (any, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid scalar JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	switch value.(type) {
	case nil, string, bool, json.Number:
		return value, nil
	}
	return nil, fmt.Errorf("native identity must be a JSON scalar or null")
}

func pythonScalarString(value any) (string, error) {
	switch value := value.(type) {
	case nil:
		return "None", nil
	case string:
		return value, nil
	case bool:
		if value {
			return "True", nil
		}
		return "False", nil
	case json.Number:
		return pythonJSONNumberString(value)
	default:
		return "", fmt.Errorf("not a Python scalar")
	}
}

func monitorTruthy(raw json.RawMessage) bool {
	value, _ := monitorScalar(raw)
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case json.Number:
		f, _ := strconv.ParseFloat(value.String(), 64)
		return f != 0
	}
	return false
}
