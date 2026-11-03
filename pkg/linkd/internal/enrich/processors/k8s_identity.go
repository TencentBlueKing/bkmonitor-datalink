package processors

import (
	"context"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/rules"
)

// fallbackK8sIdentity 只供 OneModel 查询成功但未命中的分支调用。
// DATA 策略可以按 OneModel 自然键生成身份；实例策略继续要求真实实例命中。
// 返回的身份不表示 OneModel 已存在该实例；调用方以 fallback_succeeded 保留未命中原因。
// 仅在没有其他未解决问题时标记 succeeded，该成功结果遵循 reuse_on_active 参与快照复用。
func fallbackK8sIdentity(ctx context.Context, scope *enrich.Scope, code string) (string, []enrich.Diagnostic, error) {
	strategy, found, err := scope.CWStrategy(ctx)
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	if err != nil || !found || rules.Classify(strategy, scope.Facts().Dimensions).Main != rules.MainData {
		return "", nil, nil
	}
	id, diagnostics := buildK8sIdentity(code, scope.Facts().Dimensions)
	if len(diagnostics) != 0 {
		return "", diagnostics, nil
	}
	// 明确声明的身份不能被另一条自然键覆盖，即使两者在 OneModel 中都未命中。
	diagnostics, err = k8sInstanceClaims(scope, enrich.Instance{ModelCode: code, InstanceID: id})
	if err != nil || len(diagnostics) != 0 {
		return "", diagnostics, err
	}
	return id, nil, nil
}

// buildK8sIdentity 与 OneModel 的 KUBERNETES_IDENTITY_DIMENSIONS 保持相同顺序。
// 原值不排序、哈希或转义；拒绝分隔符、非法文本和超长身份，避免生成另一资源的键。
func buildK8sIdentity(code string, dimensions domain.DimensionMap) (string, []enrich.Diagnostic) {
	fields := k8sIdentityDimensionFields(code, dimensions)
	if len(fields) == 0 {
		return "", []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"dimensions.model_id"}}}
	}
	parts := make([]string, 0, len(fields))
	paths := make([]string, 0, len(fields))
	var diagnostics []enrich.Diagnostic
	for _, field := range fields {
		path := "dimensions." + field
		paths = append(paths, path)
		value := dimensionText(dimensions, field)
		if value == "" {
			diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeMissingField, Fields: []string{path}})
		} else if strings.Contains(value, "|") || !resourceIdentityText(value, 1024) {
			diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{path}})
		}
		parts = append(parts, value)
	}
	if len(diagnostics) != 0 {
		return "", diagnostics
	}
	id := strings.Join(parts, "|")
	if !resourceIdentityText(id, 1024) {
		return "", []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: paths}}
	}
	return id, nil
}

func k8sIdentityDimensionFields(code string, dimensions domain.DimensionMap) []string {
	if code == "cw-K8s_Pod" && dimensionText(dimensions, rules.FieldWorkloadKind) == "Pod" {
		// 与 OneModelK8sReader 一致：独立 Pod 的名称可由 workload_name 表达。
		return []string{rules.FieldBCSClusterID, rules.FieldNamespace, rules.FieldWorkloadName}
	}
	return k8sIdentityFields[code]
}

// 成功兜底也保存诊断；只有未被兜底解决的问题才使处理器降级。
// 当前 fallback_succeeded 仅由 K8s DATA 主实例的自然键兜底产生。
func hasUnresolvedDiagnostics(diagnostics []enrich.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != enrich.DiagnosticCodeFallbackSucceeded {
			return true
		}
	}
	return false
}
