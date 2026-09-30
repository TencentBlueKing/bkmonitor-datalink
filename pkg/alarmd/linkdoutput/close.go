package linkdoutput

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const ActionClosed = "closed"
const CloseReasonInactive = "strategy_inactive"

// SeverityAllLevels is the level a strategy close is sent at: every level of
// the strategy, whichever one the alert is at. Both closes are decided for a
// strategy - it is outside its effective time, or it no longer exists - and
// neither depends on the level the alert reached, so the message does not
// name one. It is the level agreed with the alert link for this purpose; the
// link applies the close to the active alert of the fingerprint whatever its
// level. The alert link's reconciliation, which is where these closes get
// their alerts from, has never carried a level.
const SeverityAllLevels = "__ALL__"

// CloseReasonAbsent is the close for an alert whose strategy no longer
// exists: disabled or deleted, so nothing is left to evaluate it back to
// normal. It is its own reason and not a variant of the inactive one,
// because the two say different things to whoever reads the alert. Inactive
// means "outside its hours, it will be back"; absent means "there is no
// strategy behind this alert any more".
const CloseReasonAbsent = "strategy_absent"

// CloseReasonTargetOutOfScope is the close for an alert whose target left
// the strategy's monitoring scope: the series is still reported, but the
// target filter now turns it away, so nothing evaluates it back to normal.
// The strategy exists and is in effect; what ended is this target's place
// in it.
const CloseReasonTargetOutOfScope = "target_out_of_scope"

// CloseRequest carries an active alert's identity. A SET member alone is
// insufficient. It carries no severity: a close decided for a strategy closes
// the alert at whatever level it is, which the message says with
// SeverityAllLevels.
// OccurredAt is a current maintenance decision, never a historical Slot.
//
// Reason says which close this is. Empty is CloseReasonInactive, which is
// what every caller sent before there was a second reason, and what keeps
// the event id of an inactive close identical across this change.
type CloseRequest struct {
	TenantID, Fingerprint, AlertInstanceID   string
	StrategyID, StrategyRevision, BusinessID int64
	OccurredAt                               time.Time
	Reason                                   string
}

// closeText is what each reason puts on the event, and the salt its event id
// is derived under. The salts differ so that the same alert closed for two
// different reasons is two events; the inactive salt is the original string
// so that an inactive close keeps the id it has always had.
var closeText = map[string]struct{ salt, title, content string }{
	CloseReasonInactive: {"alarmd-inactive-close-v1", "Strategy is outside its effective time",
		"Close requested because the strategy is currently inactive; this does not indicate metric recovery."},
	CloseReasonAbsent: {"alarmd-absent-strategy-close-v1", "Strategy no longer exists",
		"Close requested because the strategy was disabled or deleted and nothing evaluates this alert any more; this does not indicate metric recovery."},
	CloseReasonTargetOutOfScope: {"alarmd-target-out-of-scope-close-v1", "Target is no longer in the strategy's scope",
		"Close requested because the target this alert is about left the strategy's monitoring scope and nothing evaluates this alert any more; this does not indicate metric recovery."},
}

func ConvertClose(request CloseRequest) (Event, error) {
	// Match StrategySnapshotRef: negative IDs identify non-BKCC spaces; zero is unset.
	if request.TenantID == "" || request.AlertInstanceID == "" || request.StrategyID <= 0 || request.StrategyRevision <= 0 || request.BusinessID == 0 || request.OccurredAt.Unix() <= 0 {
		return Event{}, errors.New("close requires tenant, active instance, strategy revision, business and current time")
	}
	fingerprint, err := hex.DecodeString(request.Fingerprint)
	if err != nil || len(fingerprint) != 16 || hex.EncodeToString(fingerprint) != request.Fingerprint {
		return Event{}, errors.New("close requires the native source alert fingerprint")
	}
	reason := request.Reason
	if reason == "" {
		reason = CloseReasonInactive
	}
	text, known := closeText[reason]
	if !known {
		return Event{}, errors.New("close requires a reason this build can name")
	}
	evaluations := []wireEvaluation{{Severity: SeverityAllLevels, Action: ActionClosed, ActionReason: reason}}
	// Stable within an attempt; retries use the same event. Later maintenance
	// rechecks current rules and uses a new current time rather than replaying
	// an old closure across a new active interval.
	idInput, _ := json.Marshal([]any{text.salt, request.TenantID, request.StrategyID,
		request.StrategyRevision, request.AlertInstanceID, request.Fingerprint, SeverityAllLevels, request.OccurredAt.Unix()})
	digest := sha256.Sum256(idInput)
	id := hex.EncodeToString(digest[:])
	wire := wireEvent{TenantID: request.TenantID, EventID: id, AlertID: request.Fingerprint,
		Title: text.title, Content: text.content,
		Evaluations: evaluations,
		Dimensions:  map[string]json.RawMessage{}, OccurredAt: wireTime(request.OccurredAt.Unix()), ProducedAt: wireTime(request.OccurredAt.Unix()),
		Labels:    wireLabels{StrategyID: request.StrategyID, StrategyVersion: request.StrategyRevision, BusinessID: request.BusinessID},
		ExtraData: wireExtraData{EvaluationFamily: evaluationFamilyMetricAlgorithm},
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return Event{}, fmt.Errorf("encode close: %w", err)
	}
	return Event{EventID: id, AlertID: request.Fingerprint, TenantID: request.TenantID, Payload: payload, Severity: SeverityAllLevels, Action: ActionClosed}, nil
}
