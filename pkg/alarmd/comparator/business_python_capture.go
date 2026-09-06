package comparator

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type pythonCaptureRecord struct {
	Topic          string          `json:"topic"`
	Partition      int32           `json:"partition"`
	Offset         int64           `json:"offset"`
	RawSHA256      string          `json:"raw_sha256"`
	RawBase64      string          `json:"raw_base64"`
	Classification string          `json:"classification"`
	Reason         string          `json:"reason,omitempty"`
	Reference      json.RawMessage `json:"reference,omitempty"`
}

// ObservePythonCapture consumes capture_to_stream's actual JSONL without
// rewriting its raw message, offset, projection or classification.
func (r *BusinessRun) ObservePythonCapture(now time.Time, line []byte) error {
	var row pythonCaptureRecord
	if err := decodeBusinessLine(line, &row); err != nil {
		return err
	}
	if len(line) > r.limits.MessageBytes {
		return ErrBusinessBackpressure
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(row.RawBase64)
	if err != nil {
		return errors.New("Python raw base64")
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != row.RawSHA256 {
		return errors.New("Python raw digest")
	}
	offset := BusinessOffset{BusinessPartition{contract.ShadowPython, row.Topic, row.Partition}, row.Offset, row.RawSHA256}
	switch row.Classification {
	case "REFERENCE", "DUPLICATE":
		ref, err := contract.DecodeBusinessAbnormalV1(row.Reference, r.limits.MessageBytes)
		if err != nil {
			return err
		}
		var native struct {
			EventID  string          `json:"event_id"`
			Status   string          `json:"status"`
			Tenant   string          `json:"bk_tenant_id"`
			Strategy json.RawMessage `json:"strategy_id"`
			Business json.RawMessage `json:"bk_biz_id"`
			Time     int64           `json:"time"`
			Level    uint32          `json:"severity"`
		}
		if _, err = contract.CanonicalJSONV2(json.RawMessage(raw)); err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &native); err != nil {
			return err
		}
		identifier := func(value json.RawMessage) string {
			var text string
			if json.Unmarshal(value, &text) == nil {
				return text
			}
			return string(value)
		}
		if native.EventID != ref.Native.EventID || native.Status != "ABNORMAL" || native.Tenant != ref.Subject.TenantID || identifier(native.Strategy) != ref.Subject.StrategyID || identifier(native.Business) != ref.Subject.BusinessID || native.Time != ref.Subject.SourceTime || native.Level != ref.Primary.LevelID {
			return errors.New("Python raw/reference identity mismatch")
		}
		return r.Observe(now, offset, row.Reference)
	case "OUT_OF_SCOPE":
		var native struct {
			Strategy json.RawMessage `json:"strategy_id"`
			Status   string          `json:"status"`
		}
		if _, err = contract.CanonicalJSONV2(json.RawMessage(raw)); err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &native); err != nil || len(native.Strategy) == 0 {
			return errors.New("Python exclusion source unknown")
		}
		var strategy string
		if json.Unmarshal(native.Strategy, &strategy) != nil {
			strategy = string(native.Strategy)
		}
		if r.pythonStrategies == nil || (r.pythonStrategies[strategy] && native.Status == "ABNORMAL") {
			return errors.New("known Python abnormal cannot be excluded")
		}

		p, ok := r.partitions[offset.BusinessPartition]
		if !ok || p.read != offset.Offset || offset.Offset >= p.end {
			return errors.New("Python excluded offset")
		}
		if r.offsetCount() >= r.limits.OffsetEntries {
			return ErrBusinessBackpressure
		}
		p.pending[offset.Offset] = ""
		p.read++
		r.advance(p)
		return nil
	case "GAP":
		return errors.New("Python reference gap")
	default:
		return errors.New("Python unknown classification")
	}
}

// Actual read_range/capture_to_stream summary fields. Additional timing/counter
// fields remain archived in the raw summary; only closure facts are projected.
func projectPythonSummary(raw json.RawMessage, header BusinessCaptureHeader) (BusinessPythonCompletion, error) {
	if _, err := contract.CanonicalJSONV2(raw); err != nil {
		return BusinessPythonCompletion{}, err
	}
	var summary struct {
		Schema            string                    `json:"schema"`
		Topic             string                    `json:"topic"`
		Complete          bool                      `json:"complete"`
		ReferenceComplete bool                      `json:"reference_complete"`
		FinishedAt        float64                   `json:"finished_at"`
		ManifestHash      string                    `json:"manifest_sha256"`
		SnapshotsHash     string                    `json:"snapshots_sha256"`
		Partitions        []BusinessPythonPartition `json:"partitions"`
		Counts            map[string]uint64         `json:"classification_counts"`
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		return BusinessPythonCompletion{}, err
	}
	manifest, err := contract.CanonicalJSONV2(header.PythonSourceManifest)
	if err != nil {
		return BusinessPythonCompletion{}, err
	}
	h := sha256.Sum256(manifest)
	if summary.Schema != "python-business-kafka-range-v1" || summary.ManifestHash != hex.EncodeToString(h[:]) || summary.SnapshotsHash != header.PythonSnapshotsSHA256 || len(summary.SnapshotsHash) != 64 || summary.Counts == nil || summary.Counts["GAP"] != 0 || summary.FinishedAt <= 0 {
		return BusinessPythonCompletion{}, errors.New("Python capture source manifest or snapshot mismatch")
	}
	h = sha256.Sum256(raw)
	return BusinessPythonCompletion{Topic: summary.Topic, Complete: summary.Complete, ReferenceComplete: summary.ReferenceComplete, FinishedAt: int64(summary.FinishedAt * 1000), SummarySHA256: hex.EncodeToString(h[:]), Partitions: summary.Partitions}, nil
}

func validatePythonSourceManifest(header BusinessCaptureHeader) error {
	if len(header.PythonSourceManifest) == 0 || len(header.PythonSnapshotsSHA256) != 64 {
		return errors.New("Python frozen source manifest required")
	}
	var source struct {
		Topic      string `json:"topic"`
		Partitions []struct {
			Partition int32 `json:"partition"`
			Start     int64 `json:"start"`
			End       int64 `json:"end"`
		} `json:"partitions"`
	}
	if err := json.Unmarshal(header.PythonSourceManifest, &source); err != nil {
		return err
	}
	seen := map[int32]bool{}
	for _, p := range source.Partitions {
		if seen[p.Partition] {
			return errors.New("Python source partition duplicate")
		}
		seen[p.Partition] = true
		found := false
		for _, r := range header.Ranges {
			if r.Chain == contract.ShadowPython && r.Topic == source.Topic && r.Partition == p.Partition && r.Start == p.Start && r.End == p.End {
				found = true
			}
		}
		if !found {
			return errors.New("Python range differs from source manifest")
		}
	}
	for _, r := range header.Ranges {
		if r.Chain == contract.ShadowPython && (r.Topic != source.Topic || !seen[r.Partition]) {
			return errors.New("Python source partition absent")
		}
	}
	return nil
}
