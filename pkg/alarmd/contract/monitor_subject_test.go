// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"encoding/json"
	"testing"
)

func subjectDimensions(pairs map[string]string) map[string]json.RawMessage {
	dimensions := make(map[string]json.RawMessage, len(pairs))
	for key, value := range pairs {
		dimensions[key] = json.RawMessage(value)
	}
	return dimensions
}

func subjectIdentity(fields ...string) MonitorOutputIdentity {
	return MonitorOutputIdentity{DimensionFields: fields}
}

// The chain is ordered, and the order decides which object an alert is about.
// Each case here is the branch Python takes for the same record.
func TestTheSubjectChainPlacesEachObjectTheWayThePlatformDoes(t *testing.T) {
	for name, test := range map[string]struct {
		dimensions map[string]string
		identity   []string
		facts      *MonitorSubjectFacts
		wantType   string
		wantID     string
	}{
		"host id wins over the address it came with": {
			dimensions: map[string]string{"bk_host_id": `101`, "bk_target_ip": `"10.0.0.1"`, "bk_target_cloud_id": `0`},
			identity:   []string{"bk_host_id", "bk_target_ip", "bk_target_cloud_id"},
			wantType:   MonitorSubjectHost, wantID: "101",
		},
		"an address carries its cloud": {
			dimensions: map[string]string{"bk_target_ip": `"10.0.0.1"`, "bk_target_cloud_id": `0`},
			identity:   []string{"bk_target_ip", "bk_target_cloud_id"},
			wantType:   MonitorSubjectHost, wantID: "10.0.0.1|0",
		},
		"a service instance": {
			dimensions: map[string]string{"bk_target_service_instance_id": `"55"`},
			identity:   []string{"bk_target_service_instance_id"},
			wantType:   MonitorSubjectService, wantID: "55",
		},
		"a topology node": {
			dimensions: map[string]string{"bk_obj_id": `"module"`, "bk_inst_id": `85`},
			identity:   []string{"bk_obj_id", "bk_inst_id"},
			wantType:   MonitorSubjectTopo, wantID: "module|85",
		},
		"a pod": {
			dimensions: map[string]string{"bcs_cluster_id": `"BCS-K8S-1"`, "pod": `"web-0"`, "namespace": `"default"`},
			identity:   []string{"bcs_cluster_id", "pod", "namespace"},
			wantType:   MonitorSubjectK8sPod, wantID: "web-0",
		},
		"a workload": {
			dimensions: map[string]string{"bcs_cluster_id": `"BCS-K8S-1"`, "workload_kind": `"Deployment"`, "workload_name": `"web"`, "namespace": `"default"`},
			identity:   []string{"bcs_cluster_id", "workload_kind", "workload_name", "namespace"},
			wantType:   MonitorSubjectK8sWorkload, wantID: "Deployment:web",
		},
		"a node needs no namespace, being a cluster-level object": {
			dimensions: map[string]string{"bcs_cluster_id": `"BCS-K8S-1"`, "node": `"node-1"`},
			identity:   []string{"bcs_cluster_id", "node"},
			wantType:   MonitorSubjectK8sNode, wantID: "node-1",
		},
		"an APM service named by the dimensions": {
			dimensions: map[string]string{"app_name": `"shop"`, "service_name": `"cart"`},
			identity:   []string{"app_name", "service_name"},
			wantType:   MonitorSubjectAPMService, wantID: "shop:cart",
		},
		"an APM service named by the strategy's labels": {
			dimensions: map[string]string{"bk_instance_id": `"i-1"`},
			identity:   []string{"bk_instance_id"},
			facts:      &MonitorSubjectFacts{Labels: []string{"team", "APM-APP(shop)", "APM-SERVICE(cart)"}},
			wantType:   MonitorSubjectAPMService, wantID: "shop:cart",
		},
		"an APM app read out of the result table when only the service is labelled": {
			dimensions: map[string]string{"bk_instance_id": `"i-1"`},
			identity:   []string{"bk_instance_id"},
			facts: &MonitorSubjectFacts{
				Labels: []string{"APM-SERVICE(cart)"}, ResultTableID: "2_bkapm_metric_shop.__default__",
			},
			wantType: MonitorSubjectAPMService, wantID: "shop:cart",
		},
		"a record with no object at all": {
			dimensions: map[string]string{"device": `"sda"`},
			identity:   []string{"device"},
		},
		"a namespaced container object without its namespace is not placed": {
			dimensions: map[string]string{"bcs_cluster_id": `"BCS-K8S-1"`, "pod": `"web-0"`},
			identity:   []string{"bcs_cluster_id", "pod"},
		},
		"half an APM name is not a service": {
			dimensions: map[string]string{"app_name": `"shop"`},
			identity:   []string{"app_name"},
		},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			subject, _, err := ProjectMonitorSubject(
				subjectDimensions(test.dimensions), subjectIdentity(test.identity...), test.facts,
			)
			if err != nil {
				t.Fatalf("ProjectMonitorSubject() error = %v", err)
			}
			if subject.Type != test.wantType || subject.ID != test.wantID {
				t.Fatalf("subject = %s/%s, want %s/%s", subject.Type, subject.ID, test.wantType, test.wantID)
			}
		})
	}
}

// The container and APM branches enrich rather than consume: every dimension
// the record carried is still there afterwards. Python does the same, and it is
// what lets the fingerprint stay where it was.
func TestPlacingAContainerOrAPMObjectConsumesNoDimension(t *testing.T) {
	for name, dimensions := range map[string]map[string]string{
		"pod": {"bcs_cluster_id": `"BCS-K8S-1"`, "pod": `"web-0"`, "namespace": `"default"`},
		"apm": {"app_name": `"shop"`, "service_name": `"cart"`},
	} {
		name, dimensions := name, dimensions
		t.Run(name, func(t *testing.T) {
			fields := make([]string, 0, len(dimensions))
			for key := range dimensions {
				fields = append(fields, key)
			}
			_, remaining, err := ProjectMonitorSubject(subjectDimensions(dimensions), subjectIdentity(fields...), nil)
			if err != nil {
				t.Fatalf("ProjectMonitorSubject() error = %v", err)
			}
			if len(remaining) != len(dimensions) {
				t.Fatalf("remaining dimensions = %v, want all %d kept", remaining, len(dimensions))
			}
		})
	}
}

// The reason the two branches were added where they were: an alert's identity
// must not move. A fingerprint is built from ProjectMonitorTarget, which cannot
// see them, so every record that used to have no target still has none there.
func TestTheNewBranchesAreInvisibleToTheAlertFingerprint(t *testing.T) {
	for name, dimensions := range map[string]map[string]string{
		"pod": {"bcs_cluster_id": `"BCS-K8S-1"`, "pod": `"web-0"`, "namespace": `"default"`},
		"apm": {"app_name": `"shop"`, "service_name": `"cart"`},
	} {
		name, dimensions := name, dimensions
		t.Run(name, func(t *testing.T) {
			fields := make([]string, 0, len(dimensions))
			for key := range dimensions {
				fields = append(fields, key)
			}
			identity := subjectIdentity(fields...)
			targetType, target, _, err := ProjectMonitorTarget(subjectDimensions(dimensions), identity)
			if err != nil {
				t.Fatalf("ProjectMonitorTarget() error = %v", err)
			}
			if targetType != "" || string(target) != "null" {
				t.Fatalf("fingerprint target = %q/%s, want the empty target Python records for these", targetType, target)
			}
			// And the object itself is still placed, for the output that wants it.
			subject, _, err := ProjectMonitorSubject(subjectDimensions(dimensions), identity, nil)
			if err != nil || subject.Type == "" {
				t.Fatalf("subject = %+v, err = %v, want the object placed", subject, err)
			}
		})
	}
}

// A name worked out from the strategy rather than from the record is reported
// as a supplementary dimension, so the object stays explainable from the event.
func TestANameTakenFromTheStrategyIsReportedAsASupplementaryDimension(t *testing.T) {
	subject, _, err := ProjectMonitorSubject(
		subjectDimensions(map[string]string{"bk_instance_id": `"i-1"`}),
		subjectIdentity("bk_instance_id"),
		&MonitorSubjectFacts{Labels: []string{"APM-APP(shop)", "APM-SERVICE(cart)"}},
	)
	if err != nil {
		t.Fatalf("ProjectMonitorSubject() error = %v", err)
	}
	for field, want := range map[string]string{"app_name": `"shop"`, "service_name": `"cart"`} {
		if got := string(subject.Additional[field]); got != want {
			t.Fatalf("additional[%s] = %s, want %s", field, got, want)
		}
	}
}
