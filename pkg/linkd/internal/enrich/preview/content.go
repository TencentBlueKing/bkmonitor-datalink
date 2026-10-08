package preview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"linkd/internal/config"
	"linkd/internal/domain"
)

// openingPreview 从明确的 opening Event 生成临时 Alert。稳定预览身份仅用于
// 对照；本服务没有写入端口，不能将此身份作为生产 Alert 的生成策略。
func openingPreview(input Input, tenant string, source config.EventSource) (domain.Event, domain.EventEvaluation, domain.Alert, error) {
	if len(input.Event) > 1<<20 {
		return domain.Event{}, domain.EventEvaluation{}, domain.Alert{}, &Error{400, "event exceeds input limits"}
	}
	var event domain.Event
	if err := json.Unmarshal(input.Event, &event); err != nil {
		return domain.Event{}, domain.EventEvaluation{}, domain.Alert{}, &Error{400, "invalid event field type"}
	}
	event, err := event.Normalize()
	if err != nil {
		return domain.Event{}, domain.EventEvaluation{}, domain.Alert{}, &Error{400, "invalid opening event"}
	}
	if event.BKTenantID != tenant || event.EventSourceID != source.EventSourceID || event.EventSourceVersion != source.Version || len(event.RelatedAlertIDs) != 0 {
		return domain.Event{}, domain.EventEvaluation{}, domain.Alert{}, &Error{400, "opening event identity or published version mismatch"}
	}
	var selected domain.EventEvaluation
	count := 0
	for _, evaluation := range event.Evaluations {
		if evaluation.Action == domain.EventActionTriggered && (input.Severity == "" || input.Severity == evaluation.Severity) {
			selected = evaluation
			count++
		}
	}
	if count != 1 {
		return domain.Event{}, domain.EventEvaluation{}, domain.Alert{}, &Error{400, "choose one triggered evaluation with input.severity"}
	}
	digest := sha256.Sum256([]byte(event.BKTenantID + "\x00" + event.EventID + "\x00" + selected.Severity))
	alert := domain.Alert{
		AlertID: "preview-" + hex.EncodeToString(digest[:]), BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, EventSourceVersion: event.EventSourceVersion,
		Fingerprint: event.Fingerprint, Title: event.Title, Content: event.Content, Severity: selected.Severity, Dimensions: event.Dimensions.Clone(), Labels: event.Labels.Clone(), ExtraData: event.ExtraData.Clone(),
		SubjectSystem: event.SubjectSystem, SubjectType: event.SubjectType, SubjectID: event.SubjectID, SubjectName: event.SubjectName, SourceEventID: event.SourceEventID, SourceAlertID: event.SourceAlertID,
		Status: domain.AlertStatusActive, LatestEventID: event.EventID, TriggerEventID: event.EventID, BeginAt: event.OccurredAt, LastOccurredAt: event.OccurredAt, CreateAt: event.CreateAt, UpdateAt: event.CreateAt, EnrichStatus: domain.EnrichStatusPending,
	}
	return event, selected, alert, nil
}
