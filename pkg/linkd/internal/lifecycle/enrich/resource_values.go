// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package enrich

import (
	"fmt"

	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

// ResourceLabels 生成 BaseTarget 资源范围标签，顺序固定为业务、集群、模型实例。
func ResourceLabels(values models.ResourceValues) []string {
	labels := make([]string, 0, 6)
	appendLabel := func(key string, value any) {
		if value == nil {
			return
		}
		text := fmt.Sprint(value)
		if text == "" || text == "0" || text == "<nil>" {
			return
		}
		labels = append(labels, key, key+"|"+text)
	}
	appendLabel(rules.FieldBKBizID, values.BKBizID)
	appendLabel(rules.FieldBKSetID, values.BKSetID)
	if values.ModelID != "" && values.ModelInstID != "" {
		appendLabel(values.ModelID, values.ModelInstID)
	}
	return labels
}

// ApplyModelContext 将模型注册表中的名称与 CMDB 对象身份投影到资源值。
func ApplyModelContext(values *models.ResourceValues, model Model) {
	if name, ok := rules.FirstStringField(model.Fields, rules.FieldObjectModelName); ok {
		values.ModelName = name
	}
	if objectID, ok := rules.FirstStringField(model.Fields, rules.FieldBKCMDBObjectID); ok {
		values.BKObjID = objectID
	}
}

// ApplyTopology 将主机拓扑投影覆盖到资源值。
func ApplyTopology(values *models.ResourceValues, topology models.ResourceTopology) {
	if topology.BKBizID > 0 {
		values.BKBizID, values.BKBizName = topology.BKBizID, topology.BKBizName
	}
	if topology.BKSetID > 0 {
		values.BKSetID, values.BKSetName = topology.BKSetID, topology.BKSetName
	}
	if topology.BKModuleID > 0 {
		values.BKModuleID, values.BKModuleName = topology.BKModuleID, topology.BKModuleName
	}
}

// ResourceValuesFromInstance 将统一实例文档投影为 resource 分组的公共字段。
func ResourceValuesFromInstance(instance Instance, fallbackBizID int64) models.ResourceValues {
	fields := make(map[string]any, len(instance.Fields)+len(instance.Attributes))
	for key, value := range instance.Attributes {
		fields[key] = value
	}
	for key, value := range instance.Fields {
		fields[key] = value
	}
	values := models.ResourceValues{ModelID: instance.ModelCode, ModelInstID: instance.InstanceID}
	values.BKObjID, _ = rules.FirstField(fields, rules.FieldBKObjID)
	if value, exists := rules.FirstField(fields, rules.FieldObjectModelName, rules.FieldBKObjName); exists {
		values.ModelName = fmt.Sprint(value)
	}
	values.BKInstID, _ = rules.FirstField(fields, rules.FieldBKInstID, rules.FieldBKHostID)
	values.BKBizID, _ = rules.FirstField(fields, rules.FieldBKBizID)
	if values.BKBizID == nil {
		values.BKBizID = uniqueBusinessID(fields["bk_biz_ids"])
	}
	if values.BKBizID == nil {
		values.BKBizID = fallbackBizID
	}
	if value, exists := rules.FirstField(fields, rules.FieldBKBizName); exists {
		values.BKBizName = fmt.Sprint(value)
	}
	values.BKSetID, _ = rules.FirstField(fields, rules.FieldBKSetID)
	if value, exists := rules.FirstField(fields, rules.FieldBKSetName); exists {
		values.BKSetName = fmt.Sprint(value)
	}
	values.BKModuleID, _ = rules.FirstField(fields, rules.FieldBKModuleID)
	if value, exists := rules.FirstField(fields, rules.FieldBKModuleName); exists {
		values.BKModuleName = fmt.Sprint(value)
	}
	values.BKCloudID, _ = rules.FirstField(fields, rules.FieldBKCloudID)
	if value, exists := rules.FirstField(fields, rules.FieldBKCloudName); exists {
		values.BKCloudName = fmt.Sprint(value)
	}
	values.CloudPlatformID, _ = rules.FirstField(fields, rules.FieldCloudPlatformID)
	values.DynamicGroupID = []string{}
	values.CWLabels = []string{}
	return values
}

func uniqueBusinessID(value any) any {
	switch values := value.(type) {
	case []any:
		if len(values) == 1 {
			return values[0]
		}
	case []int64:
		if len(values) == 1 {
			return values[0]
		}
	case []int:
		if len(values) == 1 {
			return values[0]
		}
	}
	return nil
}
