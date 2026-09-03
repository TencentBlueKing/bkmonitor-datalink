package execution_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestBuildSeriesEvaluationInputRequestSupportsG4InputShapes(t *testing.T) {
	tests := []struct {
		name         string
		requirements []execution.DataRequirement
		wantNames    []execution.DatasetName
	}{
		{name: "threshold primary only", requirements: []execution.DataRequirement{namedInputRequirement("primary", "primary", execution.InputRolePrimary, -60, 0, nil)}, wantNames: []execution.DatasetName{"primary"}},
		{name: "simple ring ratio primary and previous", requirements: []execution.DataRequirement{
			namedInputRequirement("previous", "previous", execution.InputRoleAlgorithmDependency, -120, -60, []execution.NamedInputPoint{{Name: "previous", OffsetSeconds: 60}}),
			namedInputRequirement("primary", "primary", execution.InputRolePrimary, -60, 0, nil),
		}, wantNames: []execution.DatasetName{"primary", "previous"}},
		{name: "os restart primary and uptime history", requirements: []execution.DataRequirement{
			namedInputRequirement("uptime-history", "uptime_history", execution.InputRoleAlgorithmDependency, -1500, 0, []execution.NamedInputPoint{
				{Name: "previous", OffsetSeconds: 60}, {Name: "ten_minute", OffsetSeconds: 600}, {Name: "twenty_five_minute", OffsetSeconds: 1500},
			}),
			namedInputRequirement("primary", "primary", execution.InputRolePrimary, -60, 0, nil),
		}, wantNames: []execution.DatasetName{"primary", "uptime_history"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header, consumer, series, bindings, completions := namedInputFixture(t, test.requirements)
			request, err := execution.BuildSeriesEvaluationInputRequest(header, consumer, series, bindings, completions)
			if err != nil {
				t.Fatalf("BuildSeriesEvaluationInputRequest() error = %v", err)
			}
			if err := request.Validate(header); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if request.Contract != header.Contract || request.Consumer != consumer || request.SeriesIdentity != series {
				t.Fatalf("request scope = %+v", request)
			}
			gotNames := make([]execution.DatasetName, 0, len(request.Inputs))
			for _, input := range request.Inputs {
				gotNames = append(gotNames, input.Requirement.DatasetName)
				if input.Requirement.InputProjection.ValueFields[0] != "value" || input.Requirement.InputProjection.IdentityFields[0] != "host" {
					t.Fatalf("projection was not frozen: %+v", input.Requirement.InputProjection)
				}
				if input.Binding.Provenance.PhysicalQuery != input.Query.Digest || input.Completion.PhysicalQuery != input.Query.Digest {
					t.Fatalf("query provenance is not closed: %+v", input)
				}
			}
			if !reflect.DeepEqual(gotNames, test.wantNames) {
				t.Fatalf("input order = %v, want %v", gotNames, test.wantNames)
			}
			wantIDs := make([]execution.RequirementID, 0, len(request.Inputs))
			for _, input := range request.Inputs {
				wantIDs = append(wantIDs, input.Requirement.RequirementID)
			}
			if !reflect.DeepEqual(request.ExpectedRequirementIDs, wantIDs) {
				t.Fatalf("expected exact set = %v, actual = %v", request.ExpectedRequirementIDs, wantIDs)
			}
			if !reflect.DeepEqual(request.ActualRequirementIDs, wantIDs) {
				t.Fatalf("actual exact set = %v, inputs = %v", request.ActualRequirementIDs, wantIDs)
			}
		})
	}
}

func TestBuildSeriesEvaluationInputRequestFailsClosedAtConsumerSeriesScope(t *testing.T) {
	requirements := []execution.DataRequirement{
		namedInputRequirement("primary", "primary", execution.InputRolePrimary, -60, 0, nil),
		namedInputRequirement("previous", "previous", execution.InputRoleAlgorithmDependency, -120, -60, []execution.NamedInputPoint{{Name: "previous", OffsetSeconds: 60}}),
	}
	header, consumer, series, bindings, completions := namedInputFixture(t, requirements)

	tests := []struct {
		name   string
		mutate func([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion)
	}{
		{name: "missing requirement", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			return bindings[:1], completions
		}},
		{name: "duplicate requirement", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			return append(bindings, bindings[0]), completions
		}},
		{name: "wrong consumer", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			bindings[1].Consumer.LevelID = 4
			return bindings, completions
		}},
		{name: "wrong query", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			bindings[1].Provenance.PhysicalQuery = bindings[0].Provenance.PhysicalQuery
			return bindings, completions
		}},
		{name: "wrong query revision", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			completions[1].QueryRevision = "query-wrong"
			return bindings, completions
		}},
		{name: "wrong time binding", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			bindings[1].QueryWindow.Start--
			return bindings, completions
		}},
		{name: "record outside frozen time window", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			dataset := namedInputDataset(string(series), bindings[1].QueryWindow.End)
			view, err := execution.NewDatasetView(dataset, []uint32{0})
			if err != nil {
				t.Fatal(err)
			}
			bindings[1].Dataset, bindings[1].View = dataset, view
			return bindings, completions
		}},
		{name: "wrong series", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			dataset := namedInputDataset(strings.Repeat("f", 64), int64(header.Contract.Slot.EvaluationTime)-61)
			view, err := execution.NewDatasetView(dataset, []uint32{0})
			if err != nil {
				t.Fatal(err)
			}
			bindings[1].Dataset, bindings[1].View = dataset, view
			return bindings, completions
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutatedBindings := append([]execution.NamedInputBinding(nil), bindings...)
			mutatedCompletions := append([]execution.PhysicalQueryCompletion(nil), completions...)
			mutatedBindings, mutatedCompletions = test.mutate(mutatedBindings, mutatedCompletions)
			_, err := execution.BuildSeriesEvaluationInputRequest(header, consumer, series, mutatedBindings, mutatedCompletions)
			if err == nil {
				t.Fatal("invalid named-input exact set must fail closed")
			}
			var violation *execution.EvaluationInputContractError
			if !errors.As(err, &violation) {
				t.Fatalf("error %T is not a scoped EvaluationInputContractError: %v", err, err)
			}
			if violation.Consumer != consumer || violation.SeriesIdentity != series {
				t.Fatalf("violation scope = %+v", violation)
			}
		})
	}
}

func TestBuildSeriesEvaluationInputRequestFreezesRequirementProjection(t *testing.T) {
	requirements := []execution.DataRequirement{
		namedInputRequirement("primary", "primary", execution.InputRolePrimary, -60, 0, nil),
	}
	header, consumer, series, bindings, completions := namedInputFixture(t, requirements)
	request, err := execution.BuildSeriesEvaluationInputRequest(header, consumer, series, bindings, completions)
	if err != nil {
		t.Fatal(err)
	}
	header.Requirements[0].InputProjection.ValueFields[0] = "mutated"
	header.Requirements[0].RequiredColumns[0] = "mutated"
	if got := request.Inputs[0].Requirement.InputProjection.ValueFields[0]; got != "value" {
		t.Fatalf("frozen value projection = %q", got)
	}
	if got := request.Inputs[0].Requirement.RequiredColumns[0]; got != "host" {
		t.Fatalf("frozen required column = %q", got)
	}
}

func namedInputRequirement(id execution.RequirementID, name execution.DatasetName, role execution.InputRole, start, end int64, points []execution.NamedInputPoint) execution.DataRequirement {
	offsets := make([]int64, 0, len(points))
	for _, point := range points {
		offsets = append(offsets, point.OffsetSeconds)
	}
	return execution.DataRequirement{
		RequirementID: id, DatasetName: name, Role: role, LogicalQueryRef: execution.LogicalQueryRef("query-" + string(id)),
		RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: start, EndOffsetSeconds: end, HalfOpen: true},
		StepMillis:     60_000, AlignmentMillis: 60_000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen,
		ReadinessClass:  execution.ReadinessFinalizedRequired,
		InputProjection: execution.InputProjection{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, IdentityFields: []string{"host"}},
		RequiredColumns: []string{"host", "value"}, PointOffsetsSeconds: offsets, NamedPoints: append([]execution.NamedInputPoint(nil), points...),
	}
}

func namedInputFixture(t *testing.T, requirements []execution.DataRequirement) (execution.InternalExecutionHeader, execution.ConsumerRef, execution.SeriesIdentityDigest, []execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
	t.Helper()
	plans := validInternalExecution().DuePlans
	consumer := execution.ConsumerRef{Plan: plans[0].Identity, LevelID: 5, HasLevel: true}
	series := execution.SeriesIdentityDigest(strings.Repeat("c", 64))
	for index := range requirements {
		requirements[index].Consumers = []execution.DataRequirementConsumer{{
			Consumer: consumer, ConsumerDeadlineUnixMilli: plans[0].CompletionDeadlineUnixMilli,
			DownstreamExecutionReserveMilliSec: 5_000,
		}}
	}
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		t.Fatal(err)
	}
	contractRef := frozenContract()
	contractRef.QueryRevision = "query-primary"
	contractRef.DuePlanSetDigest = digest
	header := execution.InternalExecutionHeader{ExecutionID: "named-input-test", Contract: contractRef, DuePlans: plans,
		Requirements: requirements, DeadlineUnixMilli: plans[0].CompletionDeadlineUnixMilli}
	bindings := make([]execution.NamedInputBinding, 0, len(requirements))
	completions := make([]execution.PhysicalQueryCompletion, 0, len(requirements))
	for index, requirement := range requirements {
		query := execution.PlannedPhysicalQueryRef{Digest: execution.PhysicalQueryDigest("physical-" + string(requirement.RequirementID)), QueryRevision: execution.QueryRevision(requirement.LogicalQueryRef)}
		header.RequiredPhysicalQueries = append(header.RequiredPhysicalQueries, query)
		sourceTime := int64(contractRef.Slot.EvaluationTime) + requirement.RelativeWindow.EndOffsetSeconds - 1
		dataset := namedInputDataset(string(series), sourceTime)
		view, viewErr := execution.NewDatasetView(dataset, []uint32{0})
		if viewErr != nil {
			t.Fatal(viewErr)
		}
		providerRef := execution.ProviderResultRef("provider-" + string(requirement.RequirementID))
		bindings = append(bindings, execution.NamedInputBinding{
			Consumer: consumer, RequirementID: requirement.RequirementID, DatasetName: requirement.DatasetName, Role: requirement.Role,
			ProviderResult: providerRef, QueryWindow: requirement.AbsoluteWindow(contractRef.Slot.EvaluationTime), Dataset: dataset, View: view,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable,
			ImpactScope: execution.ImpactSeries, Provenance: execution.InputProvenance{PhysicalQuery: query.Digest, AttemptNo: 1},
		})
		completions = append(completions, execution.PhysicalQueryCompletion{
			Ref: providerRef, PhysicalQuery: query.Digest, QueryRevision: query.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
			Delivery: execution.SeriesDelivery{PhysicalQuery: query.Digest, QueryRevision: query.QueryRevision, Series: 1, Records: 1, Digest: strings.Repeat(string(rune('a'+index)), 64)},
		})
	}
	return header, consumer, series, bindings, completions
}

func namedInputDataset(series string, sourceTime int64) *execution.Dataset {
	return execution.NewDataset([]contract.CanonicalRecordV2{{
		RecordID: strings.Repeat("b", 64), SourceTime: sourceTime, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: series},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`1`)}, Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"host-a"`)}, ReceivedTime: sourceTime,
	}})
}
