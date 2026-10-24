// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

// Resource 仅将来源声明解析为同租户对象模型和实例，不依赖监控策略。
type Resource struct{}

// Name 返回通用资源处理器的注册名。
func (Resource) Name() string { return rules.ResourceProcessor }

// Match 允许任何来源显式选择资源解析；没有身份的事件由 Process 跳过。
func (Resource) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 校验所有明确身份的一致性后一次性提交资源字段，不改变来源事实。
func (Resource) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	state, err := scope.EvaluationState()
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	document := state.Document()
	// 1. 完整的一对身份必须来自同一位置，不能把两个不同对象拼成一个。
	identities, fields := resourceIdentities(document)
	if len(fields) != 0 {
		return resourceIdentityFailure(enrich.DiagnosticCodeInvalidField, fields), nil
	}
	if len(identities) == 0 {
		return enrich.ProcessorResult{Status: domain.EnrichStatusSkipped, Value: domain.JSONObject{}}, nil
	}
	values := domain.JSONObject{}
	var primary resourceResolution
	for index, identity := range identities {
		// 2. CMDB 名称只经租户目录映射；最终实例身份取查询响应，不复制输入冒充命中。
		resolved, diagnostics, resolveErr := resolveResourceIdentity(ctx, scope, identity)
		if resolveErr != nil {
			return enrich.ProcessorResult{}, resolveErr
		}
		if len(diagnostics) != 0 {
			status := domain.EnrichStatusFailed
			if index == 0 && resolved.model.ModelCode != "" {
				values = resourceModelValue(resolved.model)
				setResourceIdentityContext(scope, resolved.model, "", "")
				status = domain.EnrichStatusPartial
			}
			return enrich.ProcessorResult{Status: status, Value: values, Diagnostics: diagnostics}, nil
		}
		if index == 0 {
			primary = resolved
			continue
		}
		// 3. 低优先级声明只作一致性校验，冲突或失败不得换对象、提交部分绑定。
		if primary.model.ModelCode != resolved.model.ModelCode || primary.instance.InstanceID != resolved.instance.InstanceID {
			return resourceIdentityFailure("identity_conflict", append(identities[0].fields, identity.fields...)), nil
		}
	}
	values = resourceModelValue(primary.model)
	values["model_inst_id"], _ = json.Marshal(primary.instance.InstanceID)
	cmdbID := ""
	if objectID := resourceModelObjectID(primary.model); objectID != "" {
		field := cmdbInstanceAttribute(objectID)
		if value, exists := primary.instance.Attributes[field]; exists {
			id, valid := resourceCMDBID(value)
			if !valid {
				return resourceIdentityFailure(enrich.DiagnosticCodeInvalidField, []string{"instance.attributes." + field}), nil
			}
			values["bk_inst_id"], _ = json.Marshal(id)
			cmdbID = id
		}
	}
	setResourceIdentityContext(scope, primary.model, primary.instance.InstanceID, cmdbID)
	assignments, err := kingeye.ProjectAssignments(rules.ResourceProcessor, values)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	if name, _ := document["subject_name"].(string); name == "" {
		if display, ok := primary.instance.Fields["display_name"].(string); ok && display != "" {
			assignment, err := domain.PrepareEnrichPatch(domain.EnrichPatch{Op: "set", Path: "$.subject_name", Value: json.RawMessage(strconv.Quote(display))})
			if err != nil {
				return enrich.ProcessorResult{}, err
			}
			assignments = append(assignments, assignment)
		}
	}
	return enrich.ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: values, RuntimePatches: assignments}, nil
}

func setResourceIdentityContext(scope *enrich.Scope, model enrich.Model, instanceID, cmdbID string) {
	values := models.ResourceValues{ModelID: model.ModelCode, ModelName: resourceModelName(model), ModelInstID: instanceID}
	if objectID := resourceModelObjectID(model); objectID != "" {
		values.BKObjID = objectID
	}
	if cmdbID != "" {
		values.BKInstID = cmdbID
	}
	scope.Context().Resource.Set(values)
}

type resourceIdentity struct {
	system, model, instance string
	fields                  []string
}

type resourceResolution struct {
	model    enrich.Model
	instance enrich.Instance
}

func resourceIdentities(document map[string]any) ([]resourceIdentity, []string) {
	var identities []resourceIdentity
	// 已有 canonical 身份优先于 subject 和旧字段；同一命名空间内仍校验重复声明。
	for _, legacy := range []bool{false, true} {
		if legacy {
			system, _ := document["subject_system"].(string)
			model, _ := document["subject_type"].(string)
			instance, _ := document["subject_id"].(string)
			if system != "" || model != "" || instance != "" {
				fields := []string{"subject_system", "subject_type", "subject_id"}
				if (system != "cmdb" && system != "onemodel") || !resourceIdentityText(model, 128) || !resourceIdentityText(instance, 1024) {
					return nil, fields
				}
				if system == "cmdb" {
					if _, ok := resourceCMDBID(instance); !ok {
						return nil, fields
					}
				}
				identities = append(identities, resourceIdentity{system, model, instance, fields})
			}
		}
		modelKey, instKey, system := "model_id", "model_inst_id", "onemodel"
		if legacy {
			modelKey, instKey, system = "bk_obj_id", "bk_inst_id", "cmdb"
		}
		for _, root := range []string{"labels", "extra_data", "dimensions"} {
			object, _ := document[root].(map[string]any)
			modelValue, hasModel := object[modelKey]
			instValue, hasInstance := object[instKey]
			if !hasModel && !hasInstance {
				continue
			}
			fields := []string{root + "." + modelKey, root + "." + instKey}
			model, modelOK := modelValue.(string)
			instance, instOK := instValue.(string)
			if legacy {
				instance, instOK = resourceCMDBID(instValue)
			}
			if !hasModel || !hasInstance || !modelOK || !instOK || !resourceIdentityText(model, 128) || !resourceIdentityText(instance, 1024) {
				return nil, fields
			}
			identities = append(identities, resourceIdentity{system, model, instance, fields})
		}
	}
	return identities, nil
}

func resolveResourceIdentity(ctx context.Context, scope *enrich.Scope, identity resourceIdentity) (resourceResolution, []enrich.Diagnostic, error) {
	var result resourceResolution
	var found bool
	var err error
	if identity.system == "cmdb" {
		result.model, found, err = scope.ModelByCMDBObjectID(ctx, identity.model)
	} else {
		result.model, found, err = scope.ModelByCode(ctx, identity.model)
	}
	if ctx.Err() != nil {
		return result, nil, ctx.Err()
	}
	if err != nil {
		return resourceResolution{}, resourceDependencyDiagnostic("onemodel_model"), fmt.Errorf("resolve resource model: %w", err)
	}
	if !found {
		return resourceResolution{}, resourceDependencyDiagnostic("onemodel_model"), nil
	}
	model := result.model
	if model.TenantID != scope.Facts().BKTenantID || !resourceIdentityText(model.ModelCode, 128) || model.ModelID == "" ||
		identity.system == "onemodel" && model.ModelCode != identity.model || identity.system == "cmdb" && resourceModelObjectID(model) != identity.model {
		return resourceResolution{}, nil, fmt.Errorf("%w: resource model identity mismatch", enrich.ErrInvalidDataSourceResponse)
	}
	query := enrich.InstanceQuery{ModelCode: model.ModelCode}
	if identity.system == "cmdb" {
		number, _ := strconv.ParseInt(identity.instance, 10, 64)
		query.AttributeFilters = []enrich.InstanceAttributeFilter{{Field: cmdbInstanceAttribute(identity.model), Type: enrich.InstanceAttributeLong, Value: number}}
	} else {
		query.InstanceID = identity.instance
	}
	result.instance, found, err = scope.Instance(ctx, query)
	if ctx.Err() != nil {
		return result, nil, ctx.Err()
	}
	if err != nil {
		return result, resourceDependencyDiagnostic(rules.DependencyOneModel), fmt.Errorf("resolve resource instance: %w", err)
	}
	if !found {
		return result, resourceDependencyDiagnostic(rules.DependencyOneModel), nil
	}
	instance := result.instance
	if instance.TenantID != scope.Facts().BKTenantID || instance.ModelCode != model.ModelCode || !resourceIdentityText(instance.InstanceID, 1024) ||
		instance.Fields["entity_uid"] != model.ModelCode+"|"+instance.InstanceID || identity.system == "onemodel" && instance.InstanceID != identity.instance {
		return resourceResolution{}, nil, fmt.Errorf("%w: resource instance identity mismatch", enrich.ErrInvalidDataSourceResponse)
	}
	if identity.system == "cmdb" {
		id, ok := resourceCMDBID(instance.Attributes[cmdbInstanceAttribute(identity.model)])
		if !ok || id != identity.instance {
			return resourceResolution{}, nil, fmt.Errorf("%w: CMDB instance identity mismatch", enrich.ErrInvalidDataSourceResponse)
		}
	}
	return result, nil, nil
}

func resourceModelValue(model enrich.Model) domain.JSONObject {
	value := domain.JSONObject{}
	for key, text := range map[string]string{"model_id": model.ModelCode, "model_name": resourceModelName(model), "bk_obj_id": resourceModelObjectID(model)} {
		if text != "" {
			value[key], _ = json.Marshal(text)
		}
	}
	return value
}

func resourceModelName(model enrich.Model) string {
	name, _ := model.Fields["object_model_name"].(string)
	return name
}
func resourceModelObjectID(model enrich.Model) string {
	object, _ := model.Fields["bk_cmdb_obj_id"].(string)
	return object
}

func cmdbInstanceAttribute(objectID string) string {
	switch objectID {
	case "host", "biz", "set", "module":
		return "bk_" + objectID + "_id"
	case "service_instance":
		return "service_instance_id"
	default:
		return "bk_inst_id"
	}
}

func resourceCMDBID(value any) (string, bool) {
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case json.Number:
		text = v.String()
	case int:
		text = strconv.Itoa(v)
	case int64:
		text = strconv.FormatInt(v, 10)
	case float64:
		// JSON 标量视图可以使用 float64，只有精确安全整数能转换身份。
		if v <= 0 || v > 9007199254740991 || v != float64(int64(v)) {
			return "", false
		}
		text = strconv.FormatInt(int64(v), 10)
	default:
		return "", false
	}
	number, err := strconv.ParseInt(text, 10, 64)
	return text, err == nil && number > 0 && strconv.FormatInt(number, 10) == text
}

func resourceIdentityText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func resourceIdentityFailure(code enrich.DiagnosticCode, fields []string) enrich.ProcessorResult {
	return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: []enrich.Diagnostic{{Code: code, Fields: fields}}}
}

func resourceDependencyDiagnostic(dependency string) []enrich.Diagnostic {
	return []enrich.Diagnostic{{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: dependency}}
}
