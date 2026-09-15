// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import "strings"

type ShadowSourceVersionV1 struct {
	Commit string `json:"commit"`
	Image  string `json:"image"`
	Schema string `json:"schema"`
}
type ShadowTopicV1 struct {
	Name          string `json:"name"`
	ConsumerGroup string `json:"consumer_group"`
	Partitions    uint32 `json:"partitions"`
}
type ShadowResourceLimitsV1 struct {
	MaxEntries       uint64 `json:"max_entries"`
	MaxRetainedBytes uint64 `json:"max_retained_bytes"`
	MaxAgeSeconds    uint64 `json:"max_age_seconds"`
	MaxQueueEntries  uint64 `json:"max_queue_entries"`
	MaxQueueBytes    uint64 `json:"max_queue_bytes"`
	MaxAuditInflight uint64 `json:"max_audit_inflight"`
	MaxMessageBytes  uint64 `json:"max_message_bytes"`
}
type ShadowTargetScopeV1 struct {
	TenantID             string   `json:"tenant_id"`
	BusinessID           string   `json:"business_id"`
	DataType             string   `json:"data_type"`
	Capabilities         []string `json:"capabilities"`
	ExcludedCapabilities []string `json:"excluded_capabilities"`
}
type ValidationEpochManifestV1 struct {
	Schema                Schema                 `json:"schema"`
	RequiredFeatures      []string               `json:"required_features"`
	EpochID               string                 `json:"validation_epoch_id"`
	StartedAt             int64                  `json:"started_at"`
	EligibleFrom          int64                  `json:"eligible_from"`
	ExpectedEnd           int64                  `json:"expected_end"`
	Target                ShadowTargetScopeV1    `json:"target_scope"`
	Python                ShadowSourceVersionV1  `json:"python"`
	Go                    ShadowSourceVersionV1  `json:"go"`
	StrategyObservation   string                 `json:"strategy_source_observation"`
	StrategyPublication   string                 `json:"strategy_publication"`
	ComparisonVersion     string                 `json:"comparison_contract_version"`
	IdentityVersion       string                 `json:"identity_contract_version"`
	PythonTopic           ShadowTopicV1          `json:"python_topic"`
	GoTopic               ShadowTopicV1          `json:"go_topic"`
	AuditTopic            ShadowTopicV1          `json:"audit_topic"`
	Limits                ShadowResourceLimitsV1 `json:"resource_limits"`
	GracePolicyRevision   string                 `json:"grace_policy_revision"`
	RuntimeConfigDigests  []string               `json:"runtime_config_digests"`
	KnownExclusionReasons []string               `json:"known_exclusion_reasons"`
}

func ValidateValidationEpochManifestV1(m *ValidationEpochManifestV1) error {
	if m == nil {
		return invalid("shadow.epoch", "nil")
	}
	if err := shadowHeader(m.Schema, m.RequiredFeatures, ValidationEpochManifestSchemaV1, "", ""); err != nil {
		return err
	}
	if m.EpochID == "" || m.StartedAt < 0 || m.EligibleFrom < m.StartedAt || m.ExpectedEnd <= m.EligibleFrom || m.ComparisonVersion == "" || m.IdentityVersion == "" || m.GracePolicyRevision == "" || m.StrategyObservation == "" || m.StrategyPublication == "" {
		return invalid("shadow.epoch", "incomplete frozen identity or window")
	}
	if m.Target.TenantID == "" || m.Target.BusinessID == "" || m.Target.DataType != "SERIES" || len(m.Target.Capabilities) == 0 || m.Target.ExcludedCapabilities == nil {
		return invalid("shadow.epoch.scope", "SERIES capability scope required")
	}
	caps := map[string]bool{}
	for _, c := range append(append([]string{}, m.Target.Capabilities...), m.Target.ExcludedCapabilities...) {
		if c == "" || caps[c] {
			return invalid("shadow.epoch.capability", "empty, duplicate or overlapping capability")
		}
		caps[c] = true
	}
	for _, v := range []ShadowSourceVersionV1{m.Python, m.Go} {
		if len(v.Commit) != 40 || v.Image == "" || v.Schema == "" {
			return invalid("shadow.epoch.source", "exact source and image required")
		}
	}
	topics := map[string]bool{}
	for _, t := range []ShadowTopicV1{m.PythonTopic, m.GoTopic, m.AuditTopic} {
		if !strings.Contains(t.Name, "shadow") || topics[t.Name] || t.ConsumerGroup == "" || t.Partitions == 0 {
			return invalid("shadow.epoch.topic", "three isolated shadow topics required")
		}
		topics[t.Name] = true
	}
	l := m.Limits
	if l.MaxEntries == 0 || l.MaxRetainedBytes == 0 || l.MaxAgeSeconds == 0 || l.MaxQueueEntries == 0 || l.MaxQueueBytes == 0 || l.MaxAuditInflight == 0 || l.MaxMessageBytes == 0 {
		return invalid("shadow.epoch.limits", "all resource dimensions required")
	}
	if len(m.RuntimeConfigDigests) == 0 || m.KnownExclusionReasons == nil {
		return invalid("shadow.epoch", "resolved runtime identity and exclusions required")
	}
	for _, d := range m.RuntimeConfigDigests {
		if !sha256Pattern.MatchString(d) {
			return invalid("shadow.epoch.runtime", "invalid runtime config digest")
		}
	}
	seen := map[string]bool{}
	for _, r := range m.KnownExclusionReasons {
		if r == "" || seen[r] {
			return invalid("shadow.epoch.exclusions", "empty or duplicate reason")
		}
		seen[r] = true
	}
	return nil
}

// ValidateShadowExclusionV1 accepts only the reasons frozen in this Epoch.
// A manifest is an input fact, not proof that an Epoch has passed.
func ValidateShadowExclusionV1(m *ValidationEpochManifestV1, reason string) error {
	if err := ValidateValidationEpochManifestV1(m); err != nil {
		return err
	}
	for _, r := range m.KnownExclusionReasons {
		if r == reason {
			return nil
		}
	}
	return invalid("shadow.exclusion", "reason is not frozen in this Epoch")
}
