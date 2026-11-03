// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package enrich

import "linkd/internal/onemodel"

// InstanceFailureReason 为已经失败的实例读取补充诊断，不参与匹配和兜底决策。
// query.InstanceID 为空表示按属性查询，只检查返回身份非空，不推导或比较自然键。
func InstanceFailureReason(tenant string, query InstanceQuery, instance Instance, found bool, err error) string {
	if err != nil {
		return onemodel.ErrorReason(err)
	}
	if !found {
		return "instance_not_found"
	}
	if instance.TenantID != tenant {
		return "tenant_mismatch"
	}
	if instance.ModelCode != query.ModelCode {
		return "model_mismatch"
	}
	if instance.InstanceID == "" {
		return "instance_id_empty"
	}
	if query.InstanceID != "" && instance.InstanceID != query.InstanceID {
		return "instance_id_mismatch"
	}
	return "response_invalid"
}

// ModelFailureReason 为已经失败的模型元数据读取补充诊断，不改变模型有效性判断。
func ModelFailureReason(tenant, code string, model Model, found bool, err error) string {
	if err != nil {
		return onemodel.ErrorReason(err)
	}
	if !found {
		return "model_not_found"
	}
	if model.TenantID != tenant {
		return "tenant_mismatch"
	}
	if model.ModelCode != code {
		return "model_mismatch"
	}
	return "response_invalid"
}
