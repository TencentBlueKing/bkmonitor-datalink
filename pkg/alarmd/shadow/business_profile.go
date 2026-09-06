package shadow

import (
	"encoding/json"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// BuildGoBusinessAbnormal projects the actual frozen Plan and ACKed event.
// Recovery remains available through the existing single-chain evidence path.
func BuildGoBusinessAbnormal(input GoFrozenEvidenceInputV2, limit int) (*contract.BusinessAbnormalV1, error) {
	if input.Event.EventKind != contract.TriggerEventAbnormal {
		return nil, errors.New("business comparison excludes Recovery")
	}
	e, err := BuildGoFinalEvidenceV2(input, limit)
	if err != nil {
		return nil, err
	}
	c, err := BuildFrozenComparisonConfigV2(input.Due, input.Requirements, input.Queries)
	if err != nil {
		return nil, err
	}
	config, digest, err := contract.CanonicalBusinessConfigV1(c)
	if err != nil {
		return nil, err
	}
	return buildBusinessReference(input, e, config, digest, limit)
}
func buildBusinessReference(input GoFrozenEvidenceInputV2, e *contract.FinalResultEvidenceV1, config json.RawMessage, digest string, limit int) (*contract.BusinessAbnormalV1, error) {
	var err error
	r := &contract.BusinessAbnormalV1{Schema: contract.BusinessAbnormalReferenceV1, Subject: e.Subject,
		Primary: contract.BusinessPrimaryV1{LevelID: e.Primary.LevelID, Status: e.Primary.Result, Priority: e.Primary.Priority, Values: e.Primary.Values, Unit: e.Primary.Unit},
		Config:  config, ConfigDigest: digest, InputQuality: e.Completeness.Input,
		Native: contract.BusinessNativeV1{EventID: e.Native.EventID, SnapshotKey: e.Context.SnapshotRevision, RecordID: input.Event.RecordRef.RecordID}}
	for _, l := range e.Native.LevelResults {
		if l.LevelID == r.Primary.LevelID {
			r.Primary.Trigger = l.DecisionWindow.Trigger
		}
	}
	r.SemanticDigest, err = contract.BusinessSemanticDigestV1(*r)
	if err != nil {
		return nil, err
	}
	if _, err = contract.EncodeBusinessAbnormalV1(r, limit); err != nil {
		return nil, err
	}
	return r, nil
}
