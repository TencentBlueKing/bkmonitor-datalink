package comparator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// BusinessCaptureHeader freezes a finite execution of the existing Comparator
// against archived Kafka coordinates. It is not a persistent service registry.
type BusinessCaptureHeader struct {
	PythonSourceManifest  json.RawMessage                    `json:"python_source_manifest"`
	PythonSnapshotsSHA256 string                             `json:"python_snapshots_sha256"`
	Manifest              contract.ValidationEpochManifestV1 `json:"manifest"`
	Ranges                []BusinessRange                    `json:"ranges"`
	Limits                BusinessLimits                     `json:"limits"`
	GraceMillis           int64                              `json:"grace_millis"`
}

type BusinessCaptureFrame struct {
	PythonSummary    json.RawMessage                  `json:"python_summary"`
	Kind             string                           `json:"kind"`
	Offset           BusinessOffset                   `json:"offset"`
	ObservedAt       int64                            `json:"observed_at"`
	Value            json.RawMessage                  `json:"value"`
	Subject          contract.ShadowSubjectV1         `json:"subject"`
	Receipt          *contract.ChainCoverageReceiptV1 `json:"receipt"`
	Config           contract.ComparisonConfigV2      `json:"full_config"`
	CompletedAt      int64                            `json:"completed_at"`
	PythonCompletion *BusinessPythonCompletion        `json:"-"`
	Reason           string                           `json:"reason"`
}

// These are archived reader facts, not a timeout-based completion assertion.
type BusinessPythonCompletion struct {
	Topic             string                    `json:"topic"`
	Complete          bool                      `json:"complete"`
	ReferenceComplete bool                      `json:"reference_complete"`
	FinishedAt        int64                     `json:"finished_at"`
	SummarySHA256     string                    `json:"summary_sha256"`
	Partitions        []BusinessPythonPartition `json:"partitions"`
}
type BusinessPythonPartition struct {
	Partition int32 `json:"partition"`
	Start     int64 `json:"start"`
	End       int64 `json:"end"`
	Low       int64 `json:"low"`
	High      int64 `json:"high"`
	ReadEnd   int64 `json:"read_end"`
}

func (r *BusinessRun) VerifyPythonCompletion(proof BusinessPythonCompletion) (time.Time, error) {
	if !proof.Complete || !proof.ReferenceComplete || proof.FinishedAt <= 0 || len(proof.SummarySHA256) != 64 {
		return time.Time{}, errors.New("Python capture incomplete")
	}
	seen := map[BusinessPartition]bool{}
	for _, fact := range proof.Partitions {
		key := BusinessPartition{contract.ShadowPython, proof.Topic, fact.Partition}
		p, ok := r.partitions[key]
		if !ok || seen[key] || fact.Start != p.start || fact.End != p.end || fact.Low > fact.Start || fact.High < fact.End || fact.ReadEnd != fact.End || p.read != p.end {
			return time.Time{}, errors.New("Python frozen range not closed")
		}
		seen[key] = true
	}
	for key := range r.partitions {
		if key.Chain == contract.ShadowPython && !seen[key] {
			return time.Time{}, errors.New("Python partition proof absent")
		}
	}
	return time.UnixMilli(proof.FinishedAt), nil
}

func decodeBusinessLine(wire []byte, out any) error {
	if _, err := contract.CanonicalJSONV2(json.RawMessage(wire)); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(wire))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

// RunBusinessCapture streams bounded evidence/receipt frames; the first line
// is the frozen header. This is the executable caller for the new finite
// Comparator profile. Audit output is broker-ACKed by sink before committable
// offsets are returned. No source consumer-group commit is performed here.
func RunBusinessCapture(ctx context.Context, input io.Reader, sink BusinessAuditSink) (string, map[BusinessPartition]int64, error) {
	return runBusinessCaptureWithClock(ctx, input, sink, time.Now)
}

func runBusinessCaptureWithClock(ctx context.Context, input io.Reader, sink BusinessAuditSink, now func() time.Time) (string, map[BusinessPartition]int64, error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), (2<<20)+4096)
	if !scanner.Scan() {
		return "UNPROVEN", nil, errors.New("business capture header missing")
	}
	var header BusinessCaptureHeader
	if err := decodeBusinessLine(scanner.Bytes(), &header); err != nil {
		return "UNPROVEN", nil, err
	}
	if err := contract.ValidateValidationEpochManifestV1(&header.Manifest); err != nil {
		return "UNPROVEN", nil, err
	}
	if header.Manifest.ComparisonVersion != "python-business-kafka-v1" || header.GraceMillis < 0 || header.Limits.MessageBytes > 1<<20 {
		return "UNPROVEN", nil, errors.New("business capture profile or bound")
	}
	if uint64(header.Limits.Entries) > header.Manifest.Limits.MaxEntries || uint64(header.Limits.Bytes) > header.Manifest.Limits.MaxRetainedBytes || uint64(header.Limits.MessageBytes) > header.Manifest.Limits.MaxMessageBytes || header.Limits.MaxAge/time.Second > time.Duration(header.Manifest.Limits.MaxAgeSeconds) {
		return "UNPROVEN", nil, errors.New("business capture exceeds frozen Epoch bounds")
	}
	for _, p := range header.Ranges {
		if p.Chain == contract.ShadowGo && p.Topic != header.Manifest.GoTopic.Name {
			return "UNPROVEN", nil, errors.New("Go capture Topic differs from Manifest")
		}
	}
	r, err := NewBusinessRun(header.Manifest.EpochID, header.Ranges, header.Limits)
	if err != nil {
		return "UNPROVEN", nil, err
	}
	if err = validatePythonSourceManifest(header); err != nil {
		return "UNPROVEN", nil, err
	}
	var pythonScope struct {
		StrategyIDs []json.RawMessage `json:"strategy_ids"`
	}
	_ = json.Unmarshal(header.PythonSourceManifest, &pythonScope)
	r.pythonStrategies = map[string]bool{}
	for _, id := range pythonScope.StrategyIDs {
		var text string
		if json.Unmarshal(id, &text) != nil {
			text = string(id)
		}
		r.pythonStrategies[text] = true
	}
	if len(r.pythonStrategies) == 0 {
		return "UNPROVEN", nil, errors.New("Python strategy scope missing")
	}
	r.target = &header.Manifest.Target
	abort := func(reason string, cause error) (string, map[BusinessPartition]int64, error) {
		_ = r.Gap(reason)
		if err := r.PublishPending(ctx, sink); err != nil {
			return "PENDING_AUDIT", nil, err
		}
		return "UNPROVEN", nil, cause
	}
	var retainedSince time.Time
	expired := func() bool {
		return !retainedSince.IsZero() && now().Sub(retainedSince) > r.limits.MaxAge
	}
	closed := false
	goCaptureStarted, goCaptureClosed := false, false
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "PENDING", nil, err
		}
		if expired() {
			return abort("COMPARATOR_CAPACITY_GAP", ErrBusinessBackpressure)
		}
		if closed {
			return abort("EVIDENCE_GAP", errors.New("business data after close"))
		}
		admittedAt := now()
		var frame BusinessCaptureFrame
		if len(scanner.Bytes()) > header.Limits.MessageBytes*2+4096 {
			return abort("COMPARATOR_CAPACITY_GAP", errors.New("business capture frame bound"))
		}
		var nativeDiscriminator struct {
			Schema string           `json:"schema"`
			Raw    *json.RawMessage `json:"value_base64"`
		}
		if json.Unmarshal(scanner.Bytes(), &nativeDiscriminator) == nil && (nativeDiscriminator.Raw != nil || nativeDiscriminator.Schema == "go-finite-capture-v1" || nativeDiscriminator.Schema == "go-finite-capture-summary-v1") {
			if nativeDiscriminator.Schema == "go-finite-capture-v1" {
				if goCaptureStarted {
					return abort("EVIDENCE_GAP", errors.New("duplicate Go capture header"))
				}
				err = validateGoCaptureHeader(scanner.Bytes(), header)
				goCaptureStarted = err == nil
			} else if nativeDiscriminator.Schema == "go-finite-capture-summary-v1" {
				if !goCaptureStarted || goCaptureClosed {
					return abort("EVIDENCE_GAP", errors.New("Go capture summary sequence"))
				}
				err = r.verifyGoCaptureSummary(scanner.Bytes())
				goCaptureClosed = err == nil
			} else {
				if !goCaptureStarted || goCaptureClosed {
					return abort("EVIDENCE_GAP", errors.New("Go raw capture sequence"))
				}
				err = r.observeGoCapture(admittedAt, scanner.Bytes())
			}
			if err != nil {
				return abort("EVIDENCE_GAP", err)
			}
			if retainedSince.IsZero() && (len(r.entries) > 0 || len(r.scopes) > 0) {
				retainedSince = admittedAt
			}
			continue
		}
		if len(scanner.Bytes()) > header.Limits.MessageBytes {
			return abort("COMPARATOR_CAPACITY_GAP", errors.New("business capture frame bound"))
		}
		var discriminator struct {
			Kind           string `json:"kind"`
			Classification string `json:"classification"`
		}
		if err = json.Unmarshal(scanner.Bytes(), &discriminator); err == nil && discriminator.Kind == "" && discriminator.Classification != "" {
			err = r.ObservePythonCapture(admittedAt, scanner.Bytes())
			if err != nil {
				_ = r.Gap("REFERENCE_GAP")
				if auditErr := r.PublishPending(ctx, sink); auditErr != nil {
					return "PENDING_AUDIT", nil, auditErr
				}
				return "UNPROVEN", nil, err
			}
			if retainedSince.IsZero() && (len(r.entries) > 0 || len(r.scopes) > 0) {
				retainedSince = admittedAt
			}
			continue
		}
		if err = decodeBusinessLine(scanner.Bytes(), &frame); err != nil {
			_ = r.Gap("EVIDENCE_GAP")
			if auditErr := r.PublishPending(ctx, sink); auditErr != nil {
				return "PENDING_AUDIT", nil, auditErr
			}
			return "UNPROVEN", nil, err
		}
		switch frame.Kind {
		case "EVIDENCE":
			if frame.Offset.Chain != contract.ShadowGo {
				err = errors.New("Python evidence requires original capture row")
				break
			}
			err = r.Observe(admittedAt, frame.Offset, frame.Value)
		case "NATIVE":
			record, decodeErr := contract.DecodeShadowResultRecordV1(frame.Value, header.Limits.MessageBytes)
			knownSingleChain := record.Kind == contract.ShadowFinalResult && record.Evidence != nil && record.Evidence.Chain == contract.ShadowGo && record.Evidence.EpochID == header.Manifest.EpochID && record.Evidence.ResultKind == contract.TriggerEventRecovery
			if decodeErr != nil || (record.Kind != contract.ShadowNativeEvent && !knownSingleChain) {
				err = errors.New("not a known native Event")
			} else {
				err = r.SkipNative(frame.Offset)
			}
		case "GO_COVERAGE":
			err = r.ObserveGoCoverage(admittedAt, frame.Offset, frame.Value)
			if err == nil {
				e, _ := contract.DecodeGoCoverageRecord(frame.Value, header.Limits.MessageBytes)
				var observed time.Time
				if frame.ObservedAt > 0 {
					observed = time.UnixMilli(frame.ObservedAt)
				}
				r.setScopeAuditFirst(e.Identity, observed)
			}
		case "GO_RECEIPT":
			err = r.BindGoReceipt(frame.Subject, frame.Receipt, frame.Config, time.UnixMilli(frame.CompletedAt))
		case "GAP":
			err = r.Gap(frame.Reason)
		case "CLOSE":
			if goCaptureStarted && !goCaptureClosed {
				return abort("EVIDENCE_GAP", errors.New("Go range proof missing"))
			}
			if len(frame.PythonSummary) != 0 {
				var proof BusinessPythonCompletion
				proof, err = projectPythonSummary(frame.PythonSummary, header)
				if err != nil {
					break
				}
				frame.PythonCompletion = &proof
			}
			if frame.PythonCompletion == nil {
				return abort("REFERENCE_GAP", errors.New("Python range proof missing"))
			}
			var completed time.Time
			completed, err = r.VerifyPythonCompletion(*frame.PythonCompletion)
			if err == nil {
				_, err = r.Finalize(time.UnixMilli(frame.ObservedAt), completed, time.Duration(header.GraceMillis)*time.Millisecond)
			}
			closed = true
		default:
			err = errors.New("business capture frame kind")
		}
		if err != nil {
			reason := "EVIDENCE_GAP"
			if frame.Offset.Chain == contract.ShadowPython || frame.Kind == "CLOSE" {
				reason = "REFERENCE_GAP"
			}
			if errors.Is(err, ErrBusinessBackpressure) {
				reason = "COMPARATOR_CAPACITY_GAP"
			}
			_ = r.Gap(reason)
			if auditErr := r.PublishPending(ctx, sink); auditErr != nil {
				return "PENDING_AUDIT", nil, auditErr
			}
			return "UNPROVEN", nil, err
		}
		if retainedSince.IsZero() && (len(r.entries) > 0 || len(r.scopes) > 0) {
			retainedSince = admittedAt
		}
	}
	if expired() {
		return abort("COMPARATOR_CAPACITY_GAP", ErrBusinessBackpressure)
	}
	if err = scanner.Err(); err != nil {
		return abort("EVIDENCE_GAP", err)
	}
	if !closed {
		return "PENDING", nil, errors.New("business capture has no closed range")
	}
	if err = r.publishPending(ctx, sink, expired); err != nil {
		return "PENDING_AUDIT", nil, err
	}
	offsets := map[BusinessPartition]int64{}
	for p := range r.partitions {
		offsets[p], _ = r.Committable(p)
	}
	return r.Result(), offsets, nil
}
