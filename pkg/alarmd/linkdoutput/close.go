package linkdoutput

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
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

// CloseReasonSupersededIdentity is the close for an alert a series opened
// under the fingerprint of its record's own address, before its alert
// identity read CMDB's address for a host CMDB places by id or agent: the
// series goes on under its new alert, and nothing evaluates the old one.
const CloseReasonSupersededIdentity = "superseded_identity"

// CloseReasonUnsupported is the close for an alert of a strategy that still
// exists and that alarmd does not detect: its publication refused it for a
// capability alarmd does not support, by design or not yet in this build. It says "nobody judges this any more", not
// "this is normal" - closed, never resolved - and the capability goes in the
// content (CloseRequest.Detail).
const CloseReasonUnsupported = "strategy_unsupported"

// CloseReasonStrategyChanged is the close for an alert an edit of its
// strategy orphaned: the strategy exists and is detected, and its current
// revision can no longer produce or recover the alert - its metrics changed,
// a grouping dimension it was told apart by is gone, its level was removed,
// a no-data alert's filter conditions changed, or no-data detection was
// switched off. Which of those goes in the content (CloseRequest.Detail,
// one of the StrategyChanged* words).
const CloseReasonStrategyChanged = "strategy_changed"

// The words a CloseReasonStrategyChanged close names as its detail.
const (
	StrategyChangedMetric     = "metric_changed"
	StrategyChangedCondition  = "condition_changed"
	StrategyChangedDimensions = "dimensions_changed"
	StrategyChangedLevel      = "level_removed"
	StrategyChangedNoData     = "no_data_disabled"
)

// closeDetailLimit bounds the detail a close's content repeats.
const closeDetailLimit = 128

// CloseRequest carries an active alert's identity. A SET member alone is
// insufficient. It carries no severity: a close decided for a strategy closes
// the alert at whatever level it is, which the message says with
// SeverityAllLevels.
// OccurredAt is a current maintenance decision, never a historical Slot.
//
// AlertInstanceID feeds only the event id; the wire's alert_id is the
// Fingerprint, which is what the link resolves the close by (with the
// tenant and this deployment's event source). A close addressed from the
// link's index set rather than from a listed alert has no instance id to
// carry and carries the Fingerprint in its place: deterministic, so the same
// close at the same time is the same event.
//
// Reason says which close this is. Empty is CloseReasonInactive, which is
// what every caller sent before there was a second reason, and what keeps
// the event id of an inactive close identical across this change.
type CloseRequest struct {
	TenantID, Fingerprint, AlertInstanceID   string
	StrategyID, StrategyRevision, BusinessID int64
	OccurredAt                               time.Time
	Reason                                   string
	// Detail is what the content names beside the reason - for
	// CloseReasonUnsupported, the capability; for CloseReasonStrategyChanged,
	// the change - bounded by closeDetailLimit.
	// It is not part of the event id: the same alert closed for the same
	// reason is one event whatever the detail reads.
	Detail string
}

// closeText is what each reason puts on the event, and the salt its event id
// is derived under. The salts differ so that the same alert closed for two
// different reasons is two events; the inactive salt is the original string
// so that an inactive close keeps the id it has always had.
// detail is the word the content introduces CloseRequest.Detail with, for
// the reasons that carry one.
var closeText = map[string]struct{ salt, title, content, detail string }{
	CloseReasonInactive: {"alarmd-inactive-close-v1", "Strategy is outside its effective time",
		"Close requested because the strategy is currently inactive; this does not indicate metric recovery.", ""},
	CloseReasonAbsent: {"alarmd-absent-strategy-close-v1", "Strategy no longer exists",
		"Close requested because the strategy was disabled or deleted and nothing evaluates this alert any more; this does not indicate metric recovery.", ""},
	CloseReasonTargetOutOfScope: {"alarmd-target-out-of-scope-close-v1", "Target is no longer in the strategy's scope",
		"Close requested because the target this alert is about left the strategy's monitoring scope and nothing evaluates this alert any more; this does not indicate metric recovery.", ""},
	CloseReasonSupersededIdentity: {"alarmd-superseded-identity-close-v1", "The alert's series is now identified under a new fingerprint",
		"Close requested because alarmd now identifies this series under a new identity and nothing evaluates this alert any more; this does not indicate metric recovery.", ""},
	CloseReasonUnsupported: {"alarmd-unsupported-strategy-close-v1", "Strategy is no longer detected by alarmd",
		"Close requested because alarmd does not detect this strategy - it uses a capability alarmd does not support - and nothing evaluates this alert any more; this does not indicate metric recovery.", "Capability"},
	CloseReasonStrategyChanged: {"alarmd-strategy-changed-close-v1", "Strategy was changed and no longer produces this alert",
		"Close requested because the strategy was edited and its current configuration can no longer produce or recover this alert; this does not indicate metric recovery.", "Change"},
}

// NativeFingerprint is whether value is a native source alert fingerprint,
// the only alert a close can address: 16 bytes in lowercase hex.
func NativeFingerprint(value string) bool {
	fingerprint, err := hex.DecodeString(value)
	return err == nil && len(fingerprint) == 16 && hex.EncodeToString(fingerprint) == value
}

func ConvertClose(request CloseRequest) (Event, error) {
	// Match StrategySnapshotRef: negative IDs identify non-BKCC spaces; zero is unset.
	if request.TenantID == "" || request.AlertInstanceID == "" || request.StrategyID <= 0 || request.StrategyRevision <= 0 || request.BusinessID == 0 || request.OccurredAt.Unix() <= 0 {
		return Event{}, errors.New("close requires tenant, active instance, strategy revision, business and current time")
	}
	if !NativeFingerprint(request.Fingerprint) {
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
	content := text.content
	if detail := request.Detail; detail != "" {
		if len(detail) > closeDetailLimit {
			// Cut on a rune boundary: a split rune would be encoded as U+FFFD.
			cut := closeDetailLimit
			for cut > 0 && !utf8.RuneStart(detail[cut]) {
				cut--
			}
			detail = detail[:cut]
		}
		label := text.detail
		if label == "" {
			label = "Detail"
		}
		content += " " + label + ": " + detail + "."
	}
	wire := wireEvent{TenantID: request.TenantID, EventID: id, AlertID: request.Fingerprint,
		Title: text.title, Content: content,
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
