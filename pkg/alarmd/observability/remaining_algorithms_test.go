package observability

import "testing"

func TestRemainingAlgorithmFactsSurviveNormalization(t *testing.T) {
	for family, detector := range map[AlgorithmFamily]AlgorithmDetectorKind{
		AlgorithmFamilySimpleYearRound:    AlgorithmDetectorKindSimpleYearRound,
		AlgorithmFamilyAdvancedRingRatio:  AlgorithmDetectorKindAdvancedRingRatio,
		AlgorithmFamilyAdvancedYearRound:  AlgorithmDetectorKindAdvancedYearRound,
		AlgorithmFamilyRingRatioAmplitude: AlgorithmDetectorKindRingRatioAmplitude,
		AlgorithmFamilyYearRoundAmplitude: AlgorithmDetectorKindYearRoundAmplitude,
		AlgorithmFamilyYearRoundRange:     AlgorithmDetectorKindYearRoundRange,
	} {
		t.Run(string(family), func(t *testing.T) {
			observation := Observation{Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: ResultSuccess,
				AlgorithmEvaluations: []AlgorithmEvaluationFact{{SourceAlgorithmFamily: family, DetectorKind: detector, Result: AlgorithmEvaluationResultNormal}},
				AlgorithmInputs: []AlgorithmInputFact{{SourceAlgorithmFamily: family, DetectorKind: detector, InputName: AlgorithmInputNameHistory,
					DependencyPoint: AlgorithmDependencyPointHistorical, Result: AlgorithmInputResultAvailable}},
			}
			got := NormalizeObservation(observation)
			if len(got.AlgorithmEvaluations) != 1 || len(got.AlgorithmInputs) != 1 {
				t.Fatalf("supported algorithm observation dropped: %+v", got)
			}
			observation.AlgorithmInputs[0].DependencyPoint = "history_604800"
			observation.AlgorithmEvaluations[0].DetectorKind = AlgorithmDetectorKindThreshold
			got = NormalizeObservation(observation)
			if len(got.AlgorithmEvaluations) != 0 || len(got.AlgorithmInputs) != 0 {
				t.Fatalf("unbounded offset or wrong detector accepted: %+v", got)
			}
		})
	}
}
