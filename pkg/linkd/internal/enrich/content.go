package enrich

import "errors"

// SourceContentFallbackReason 返回允许使用原始 Event.content 的确定性错误码，
// 不在白名单或没有错误时返回空字符串。创建与只读预览共用此内容策略；调用方
// 仍须校验来源内容非空、大小、Context 和 Alert 身份，不能借此绕过其他校验。
func SourceContentFallbackReason(err error) string {
	var failure interface{ PermanentContentFailure() string }
	if !errors.As(err, &failure) {
		return ""
	}
	switch reason := failure.PermanentContentFailure(); reason {
	case "publication_missing", "nodata_identity_invalid", "nodata_configuration_invalid", "nodata_evidence_missing", "nodata_evidence_invalid":
		return reason
	default:
		return ""
	}
}
