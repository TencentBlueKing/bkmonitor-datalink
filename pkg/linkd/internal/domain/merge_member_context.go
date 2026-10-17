// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package domain

// MergeMemberContext 是冻结关系中成员执行所需的固定事实，不包含整组成员或可变进度。
type MergeMemberContext struct {
	ID                string        `json:"id"`
	TenantID          string        `json:"bk_tenant_id"`
	WindowID          string        `json:"window_id"`
	GroupKey          string        `json:"group_key"`
	Policy            PolicyVersion `json:"policy"`
	ParentAlertID     string        `json:"parent_alert_id"`
	ParentFingerprint string        `json:"parent_fingerprint"`
	ParentTitle       string        `json:"parent_title"`
	MemberCount       int           `json:"member_count"`
	EndReason         string        `json:"end_reason"`
}

func (r MergeRelation) MemberContext() MergeMemberContext {
	return MergeMemberContext{ID: r.ID, TenantID: r.TenantID, WindowID: r.WindowID, GroupKey: r.GroupKey, Policy: r.Policy, ParentAlertID: r.ParentAlertID, ParentFingerprint: r.ParentFingerprint, ParentTitle: r.ParentTitle, MemberCount: len(r.Members), EndReason: r.EndReason}
}
