package legacyoutput

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// PodMetadata contains only fields consumed by MonitorEventAdapter.get_k8s_target.
type PodMetadata struct {
	Namespace    string `json:"namespace"`
	WorkloadType string `json:"workload_type"`
	WorkloadName string `json:"workload_name"`
}

// PodResolver uses an explicitly configured metadata source. A nil result is a
// miss, not a fabricated cache hit. Errors remain distinguishable from misses.
type PodResolver interface {
	LookupPod(context.Context, PodLookup) (*PodMetadata, error)
}

type TargetScope struct {
	TenantID   string
	BusinessID int64
}
type PodLookup struct {
	Scope     TargetScope
	ClusterID string
	Namespace *string
	Name      string
}

type TargetProjection struct {
	Type                 string                     `json:"type"`
	Target               json.RawMessage            `json:"target"`
	Dimensions           map[string]json.RawMessage `json:"dimensions"`
	AdditionalDimensions map[string]json.RawMessage `json:"additional_dimensions"`
}

var apmAppLabel = regexp.MustCompile(`^APM-APP\((.*?)\)`)
var apmServiceLabel = regexp.MustCompile(`^APM-SERVICE\((.*?)\)`)
var apmTable = regexp.MustCompile(`^(?:space_)?\d+_bkapm_(?:metric|trace)_([a-zA-Z0-9_-]+)\.__default__$`)

// ProjectTarget preserves the Python target ordering and enrichment boundaries.
// Hosts, services, topology and APM require no external metadata calls.
func ProjectTarget(ctx context.Context, scope TargetScope, strategy json.RawMessage, dimensions map[string]json.RawMessage, fields []string, pods PodResolver) (TargetProjection, error) {
	plan, err := PrepareTarget(strategy)
	if err != nil {
		return TargetProjection{}, err
	}
	return plan.Project(ctx, scope, dimensions, fields, pods)
}

// PreparedTarget holds immutable per-strategy APM fallback fields for a batch.
type PreparedTarget struct{ labelApp, labelService, tableApp json.RawMessage }

func (plan *PreparedTarget) Project(ctx context.Context, scope TargetScope, dimensions map[string]json.RawMessage, fields []string, pods PodResolver) (TargetProjection, error) {
	kind, target, data, err := contract.ProjectMonitorTarget(dimensions, contract.MonitorOutputIdentity{DimensionFields: fields})
	if err != nil {
		return TargetProjection{}, err
	}
	p := TargetProjection{Type: kind, Target: target, Dimensions: data, AdditionalDimensions: map[string]json.RawMessage{}}
	if raw, ok := dimensions["__additional_dimensions"]; ok {
		if err := json.Unmarshal(raw, &p.AdditionalDimensions); err != nil || p.AdditionalDimensions == nil {
			return TargetProjection{}, fmt.Errorf("invalid additional dimensions")
		}
	}
	if kind != "" {
		return p, nil
	}
	// Missing a required IP/service key takes Python's KeyError return path;
	// it must not fall through to K8s/APM after that branch has been selected.
	for _, field := range fields {
		if field == "bk_target_ip" || field == "ip" || field == "bk_target_service_instance_id" || field == "bk_service_instance_id" {
			return p, nil
		}
	}
	if _, ok := data["bcs_cluster_id"]; ok {
		return projectK8S(ctx, scope, p, pods)
	}
	return plan.projectAPM(p)
}

func rawString(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func scalarText(raw json.RawMessage) string  { text, _ := contract.PythonScalarText(raw); return text }
func truthy(raw json.RawMessage) bool {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		f, _ := strconv.ParseFloat(v.String(), 64)
		return f != 0
	}
	return false
}
func notNone(raw json.RawMessage) bool {
	return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
func firstTruthy(data map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if truthy(data[key]) {
			return data[key]
		}
	}
	return nil
}

func projectK8S(ctx context.Context, scope TargetScope, p TargetProjection, pods PodResolver) (TargetProjection, error) {
	d := p.Dimensions
	pod := firstTruthy(d, "pod", "pod_name")
	namespace := d["namespace"]
	if truthy(pod) {
		var ns *string
		if notNone(namespace) {
			value := scalarText(namespace)
			ns = &value
		}
		var metadata *PodMetadata
		if pods != nil {
			var err error
			metadata, err = pods.LookupPod(ctx, PodLookup{Scope: scope, ClusterID: scalarText(d["bcs_cluster_id"]), Namespace: ns, Name: scalarText(pod)})
			if err != nil {
				return TargetProjection{}, err
			}
		}
		if metadata != nil {
			additional := map[string]json.RawMessage{}
			if _, ok := d["workload_kind"]; !ok {
				additional["workload_kind"] = rawString(metadata.WorkloadType)
			}
			if _, ok := d["workload_name"]; !ok {
				additional["workload_name"] = rawString(metadata.WorkloadName)
			}
			if _, ok := d["namespace"]; !ok {
				additional["namespace"] = rawString(metadata.Namespace)
			}
			if len(additional) != 0 {
				p.AdditionalDimensions = additional
			}
			p.Type, p.Target = "K8S-POD", pod
			return p, nil
		}
		if !notNone(namespace) {
			return p, nil
		}
		p.Type, p.Target = "K8S-POD", pod
		return p, nil
	}
	if truthy(d["workload_kind"]) && truthy(d["workload_name"]) {
		if !notNone(namespace) {
			return p, nil
		}
		p.Type, p.Target = "K8S-WORKLOAD", rawString(scalarText(d["workload_kind"])+":"+scalarText(d["workload_name"]))
		return p, nil
	}
	if node := firstTruthy(d, "node", "node_name"); truthy(node) {
		p.Type, p.Target = "K8S-NODE", node
		return p, nil
	}
	if service := firstTruthy(d, "service", "service_name"); truthy(service) {
		if !notNone(namespace) {
			return p, nil
		}
		p.Type, p.Target = "K8S-SERVICE", service
	}
	return p, nil
}

func PrepareTarget(strategy json.RawMessage) (*PreparedTarget, error) {
	var config struct {
		Labels []string `json:"labels"`
		Items  []struct {
			QueryConfigs []struct {
				ResultTableID string `json:"result_table_id"`
			} `json:"query_configs"`
		} `json:"items"`
	}
	if err := json.Unmarshal(strategy, &config); err != nil {
		return nil, err
	}
	labelValue := func(pattern *regexp.Regexp) json.RawMessage {
		for _, label := range config.Labels {
			match := pattern.FindStringSubmatch(label)
			if len(match) > 1 && match[1] != "" {
				return rawString(match[1])
			}
		}
		return nil
	}
	plan := &PreparedTarget{labelApp: labelValue(apmAppLabel), labelService: labelValue(apmServiceLabel)}
	if len(config.Items) > 0 && len(config.Items[0].QueryConfigs) > 0 {
		match := apmTable.FindStringSubmatch(config.Items[0].QueryConfigs[0].ResultTableID)
		if len(match) > 1 {
			plan.tableApp = rawString(match[1])
		}
	}
	return plan, nil
}

func (plan *PreparedTarget) projectAPM(p TargetProjection) (TargetProjection, error) {
	app, service := p.Dimensions["app_name"], p.Dimensions["service_name"]
	if !truthy(app) {
		app = plan.labelApp
	}
	if !truthy(service) {
		service = plan.labelService
	}
	if !truthy(app) {
		app = plan.tableApp
	}
	if !truthy(app) || !truthy(service) {
		return p, nil
	}
	p.AdditionalDimensions = map[string]json.RawMessage{}
	if _, ok := p.Dimensions["app_name"]; !ok {
		p.AdditionalDimensions["app_name"] = app
	}
	if _, ok := p.Dimensions["service_name"]; !ok {
		p.AdditionalDimensions["service_name"] = service
	}
	p.Type, p.Target = "APM-SERVICE", rawString(scalarText(app)+":"+scalarText(service))
	return p, nil
}
