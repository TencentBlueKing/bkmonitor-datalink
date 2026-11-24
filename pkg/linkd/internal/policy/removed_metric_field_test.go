package policy

import "testing"

func TestMetricUniqueIDIsNotAnImplicitPolicyField(t *testing.T) {
	fields := KACFields()
	if _, exists := fields["metric_unique_id"]; exists {
		t.Fatal("removed field is still advertised as builtin")
	}
	if fields["name"] != FieldText || fields["fingerprint"] != FieldKeyword {
		t.Fatal("retained field catalog changed")
	}
	// 同名显式自定义字段仍按普通目录登记，不形成全局名称禁令。
	fields["metric_unique_id"] = FieldText
	if _, exists := KACFields()["metric_unique_id"]; exists {
		t.Fatal("custom registration leaked into builtin catalog")
	}
}
