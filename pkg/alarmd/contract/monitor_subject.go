// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"encoding/json"
	"regexp"
	"strings"
)

// MonitorSubjectFacts carries the strategy facts the subject projection reads
// and that nothing else in the Plan already states.
//
// Python looks these up off the strategy document when a record's dimensions do
// not name an APM service on their own: the strategy's labels, and the result
// table of its first query config. Freezing them here keeps that lookup inside
// the frozen revision, so the subject a Slot produces cannot change because
// someone edited the strategy between two evaluations of the same Slot.
type MonitorSubjectFacts struct {
	// Labels are the strategy's own labels, in their stated order.
	Labels []string `json:"labels,omitempty"`
	// ResultTableID is items[0].query_configs[0].result_table_id - the only one
	// Python inspects, so the only one that is frozen.
	ResultTableID string `json:"result_table_id,omitempty"`
}

// MonitorSubject is the object an event is about: what Python's extract_target
// returns, in the shape the downstream contract asks for.
//
// An empty Type means the record names no object this projection can place. It
// is a real answer, not a failure: custom reporting with no target dimensions
// has no object, and inventing one would attach alerts to something.
type MonitorSubject struct {
	Type string
	ID   string
	// Additional are dimensions the projection worked out that the record did
	// not carry. They stay out of the identity dimensions, because the identity
	// is what the record itself said.
	Additional map[string]json.RawMessage
}

// MonitorSubjectContext is the projected object and the dimensions that were
// left after its identity was taken out - what an output writes as the subject
// and the dimensions of one event.
type MonitorSubjectContext struct {
	Subject    MonitorSubject
	Dimensions map[string]json.RawMessage
}

// The five target types Python added after the original three. Their identity
// is deliberately absent from the dedupe fingerprint - see ProjectMonitorTarget.
const (
	MonitorSubjectHost            = "HOST"
	MonitorSubjectService         = "SERVICE"
	MonitorSubjectTopo            = "TOPO"
	MonitorSubjectK8sPod          = "K8S-POD"
	MonitorSubjectK8sNode         = "K8S-NODE"
	MonitorSubjectK8sService      = "K8S-SERVICE"
	MonitorSubjectK8sWorkload     = "K8S-WORKLOAD"
	MonitorSubjectAPMService      = "APM-SERVICE"
	monitorAPMAppNameDimension    = "app_name"
	monitorAPMServiceNameDimensio = "service_name"
)

var (
	monitorAPMAppLabel     = regexp.MustCompile(`^APM-APP\((.*?)\)`)
	monitorAPMServiceLabel = regexp.MustCompile(`^APM-SERVICE\((.*?)\)`)
	monitorAPMResultTable  = regexp.MustCompile(`^(?:space_)?\d+_bkapm_(?:metric|trace)_([a-zA-Z0-9_-]+)\.__default__$`)
)

// ProjectMonitorSubject returns the object the record is about, and the
// dimensions left after its identity was taken out.
//
// It is Python's extract_target in full. The first four branches are
// ProjectMonitorTarget, which exists on its own because the dedupe fingerprint
// is built from exactly those and must not see the rest; the container and APM
// branches below are reached only when none of them placed the record, which is
// the same order Python uses.
func ProjectMonitorSubject(
	dimensions map[string]json.RawMessage,
	identity MonitorOutputIdentity,
	facts *MonitorSubjectFacts,
) (MonitorSubject, map[string]json.RawMessage, error) {
	targetType, target, data, err := ProjectMonitorTarget(dimensions, identity)
	if err != nil {
		return MonitorSubject{}, nil, err
	}
	if targetType != "" {
		return MonitorSubject{Type: targetType, ID: monitorSubjectText(target)}, data, nil
	}
	// Neither the container nor the APM branch removes a dimension. Python does
	// not either: it enriches rather than consumes, so the dimensions a record
	// carries are still all there afterwards. That is also what keeps adding
	// these branches from moving any existing fingerprint.
	if subject, matched := projectK8sSubject(data); matched {
		return subject, data, nil
	}
	if subject, matched := projectAPMSubject(data, facts); matched {
		return subject, data, nil
	}
	return MonitorSubject{}, data, nil
}

// projectK8sSubject mirrors MonitorEventAdapter.get_k8s_target, minus the pod
// cache lookup.
//
// Python reads a pod's workload and namespace out of a cache that the Python
// pipeline writes, and adds them as supplementary dimensions. alarmd
// deliberately does not read that cache - doing so would tie this pipeline's
// completeness to the other one being alive - so a pod here is placed by the
// dimensions it carries and nothing more. That is the same path Python takes
// when its own lookup finds nothing, including its requirement that a
// namespaced object state its namespace.
func projectK8sSubject(data map[string]json.RawMessage) (MonitorSubject, bool) {
	if _, clustered := data["bcs_cluster_id"]; !clustered {
		return MonitorSubject{}, false
	}
	namespace, namespaced := data["namespace"]
	_ = namespace
	if pod := monitorFirstText(data, "pod", "pod_name"); pod != "" {
		if !namespaced {
			return MonitorSubject{}, true
		}
		return MonitorSubject{Type: MonitorSubjectK8sPod, ID: pod}, true
	}
	kind := monitorFirstText(data, "workload_kind")
	name := monitorFirstText(data, "workload_name")
	if kind != "" && name != "" {
		if !namespaced {
			return MonitorSubject{}, true
		}
		return MonitorSubject{Type: MonitorSubjectK8sWorkload, ID: kind + ":" + name}, true
	}
	// A node is a cluster-level object, so it is the one container branch that
	// does not require a namespace.
	if node := monitorFirstText(data, "node", "node_name"); node != "" {
		return MonitorSubject{Type: MonitorSubjectK8sNode, ID: node}, true
	}
	if service := monitorFirstText(data, "service", "service_name"); service != "" {
		if !namespaced {
			return MonitorSubject{}, true
		}
		return MonitorSubject{Type: MonitorSubjectK8sService, ID: service}, true
	}
	return MonitorSubject{}, true
}

// projectAPMSubject mirrors ApmAlertHelper.get_target and get_apm_target: the
// dimensions first, then the strategy's labels, then its result table - and the
// object exists only when both halves of the name were found.
func projectAPMSubject(data map[string]json.RawMessage, facts *MonitorSubjectFacts) (MonitorSubject, bool) {
	app := monitorFirstText(data, monitorAPMAppNameDimension)
	service := monitorFirstText(data, monitorAPMServiceNameDimensio)
	if app == "" || service == "" {
		if facts == nil {
			return MonitorSubject{}, false
		}
		if app == "" {
			app = monitorLabelValue(monitorAPMAppLabel, facts.Labels)
		}
		if service == "" {
			service = monitorLabelValue(monitorAPMServiceLabel, facts.Labels)
		}
		if app == "" {
			if match := monitorAPMResultTable.FindStringSubmatch(facts.ResultTableID); len(match) == 2 {
				app = match[1]
			}
		}
	}
	if app == "" || service == "" {
		return MonitorSubject{}, false
	}
	subject := MonitorSubject{Type: MonitorSubjectAPMService, ID: app + ":" + service}
	// The two halves are reported as supplementary dimensions when the record
	// did not carry them, because the name was worked out from the strategy and
	// the record alone no longer explains the object.
	additional := make(map[string]json.RawMessage, 2)
	for field, value := range map[string]string{
		monitorAPMAppNameDimension:    app,
		monitorAPMServiceNameDimensio: service,
	} {
		if _, carried := data[field]; carried {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		additional[field] = encoded
	}
	if len(additional) > 0 {
		subject.Additional = additional
	}
	return subject, true
}

func monitorLabelValue(pattern *regexp.Regexp, labels []string) string {
	for _, label := range labels {
		if match := pattern.FindStringSubmatch(label); len(match) == 2 && match[1] != "" {
			return match[1]
		}
	}
	return ""
}

func monitorFirstText(data map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		raw, exists := data[name]
		if !exists {
			continue
		}
		if text := monitorSubjectText(raw); text != "" {
			return text
		}
	}
	return ""
}

func monitorSubjectText(raw json.RawMessage) string {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return ""
	}
	value, err := monitorScalar(raw)
	if err != nil {
		return ""
	}
	text, _ := pythonScalarString(value)
	return text
}
