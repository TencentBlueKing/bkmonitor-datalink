package execution

import (
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type QueryScalarKind string

const (
	QueryScalarString  QueryScalarKind = "STRING"
	QueryScalarNumber  QueryScalarKind = "NUMBER"
	QueryScalarBoolean QueryScalarKind = "BOOLEAN"
)

type QueryScalar struct {
	Kind        QueryScalarKind
	StringValue string
	NumberValue string
	BoolValue   bool
}

type QueryFunction struct {
	Method     string
	Field      string
	Without    bool
	Dimensions []string
	Position   int32
	Arguments  []QueryScalar
	Window     string
	Subquery   bool
	Step       string
}

type QueryConditionField struct {
	Field    string
	Operator string
	Values   []QueryScalar
	Wildcard string
	Prefix   string
	Suffix   string
}

type QueryConditions struct {
	Fields     []QueryConditionField
	Connectors []string
}

type QueryClause struct {
	DataSource      string
	Driver          string
	TableID         string
	FieldName       string
	TimeField       string
	IsRegexp        bool
	ReferenceName   string
	Functions       []QueryFunction
	TimeAggregation QueryFunction
	Dimensions      []string
	Conditions      QueryConditions
	Offset          string
	OffsetForward   string
	KeepColumns     []string
	QueryString     string
}

type TimeUnit string
type SeriesIdentityMode string
type GroupKeyRule string
type ValueSelectionMode string
type ReceivedTimeMode string

const (
	TimeUnitMillisecond TimeUnit = "MILLISECOND"
	TimeUnitSecond      TimeUnit = "SECOND"

	SeriesIdentityUQGroupKeysValuesV1      SeriesIdentityMode = "UQ_GROUP_KEYS_VALUES_V1"
	GroupKeyStripTableSuffixV1             GroupKeyRule       = "STRIP_TABLE_SUFFIX_V1"
	ValueSelectionResultOrFirstReferenceV1 ValueSelectionMode = "RESULT_OR_FIRST_REFERENCE_V1"
	ReceivedTimeProviderReceivedAt         ReceivedTimeMode   = "PROVIDER_RECEIVED_AT"
)

type DatasetNormalizationSpec struct {
	DatasetContract         contract.DatasetContractV2
	SourceTimeUnit          TimeUnit
	CanonicalSourceTimeUnit TimeUnit
	SeriesIdentityMode      SeriesIdentityMode
	GroupKeyRule            GroupKeyRule
	ValueSelectionMode      ValueSelectionMode
	CanonicalValueField     string
	ReceivedTimeMode        ReceivedTimeMode
	Version                 string
}

func (spec DatasetNormalizationSpec) Validate() error {
	if spec.DatasetContract.SchemaDigest == "" || spec.DatasetContract.NormalizationDigest == "" ||
		len(spec.DatasetContract.IdentityFields) == 0 || spec.DatasetContract.SourceTimeField == "" ||
		spec.DatasetContract.ReceivedTimeField == "" || spec.SourceTimeUnit != TimeUnitMillisecond ||
		spec.CanonicalSourceTimeUnit != TimeUnitSecond || spec.SeriesIdentityMode != SeriesIdentityUQGroupKeysValuesV1 ||
		spec.GroupKeyRule != GroupKeyStripTableSuffixV1 || spec.ValueSelectionMode != ValueSelectionResultOrFirstReferenceV1 ||
		spec.CanonicalValueField == "" || spec.ReceivedTimeMode != ReceivedTimeProviderReceivedAt || spec.Version == "" {
		return errors.New("alarmd execution: incomplete dataset normalization contract")
	}
	return nil
}

func (spec DatasetNormalizationSpec) NormalizeSourceTime(sourceMillis int64) (int64, error) {
	if err := spec.Validate(); err != nil {
		return 0, err
	}
	if sourceMillis <= 0 {
		return 0, errors.New("alarmd execution: positive UQ source time is required")
	}
	return sourceMillis / 1000, nil
}

type QueryPlanFacts struct {
	QueryRevision    QueryRevision
	Provider         ProviderKind
	ProviderRouteRef ProviderRouteRef
	TenantID         string
	BusinessID       string
	SpaceScope       string
	QueryList        []QueryClause
	MetricMerge      string
	StepMillis       int64
	AlignmentMillis  int64
	DownSampleRange  DownSampleRange
	Timezone         string
	NotTimeAlign     bool
	Normalization    DatasetNormalizationSpec
}

func BuildQueryPlanFacts(facts QueryPlanFacts) (QueryPlanFacts, error) {
	if facts.QueryRevision != "" {
		return QueryPlanFacts{}, errors.New("alarmd execution: QueryPlanFacts builder owns query revision")
	}
	if facts.Provider != ProviderUQ || facts.ProviderRouteRef == "" || facts.TenantID == "" ||
		facts.BusinessID == "" || facts.SpaceScope == "" || len(facts.QueryList) == 0 ||
		facts.MetricMerge == "" || facts.StepMillis <= 0 || facts.AlignmentMillis <= 0 {
		return QueryPlanFacts{}, errors.New("alarmd execution: incomplete query plan facts")
	}
	if err := facts.Normalization.Validate(); err != nil {
		return QueryPlanFacts{}, err
	}
	for _, clause := range facts.QueryList {
		if clause.Driver == "" || clause.TimeField == "" {
			return QueryPlanFacts{}, errors.New("alarmd execution: incomplete query clause")
		}
		if len(clause.Conditions.Connectors) != 0 && len(clause.Conditions.Connectors)+1 != len(clause.Conditions.Fields) {
			return QueryPlanFacts{}, errors.New("alarmd execution: condition connectors must join adjacent fields")
		}
		if err := validateQueryFunctions(clause.Functions); err != nil {
			return QueryPlanFacts{}, err
		}
		if !isZeroQueryFunction(clause.TimeAggregation) {
			if err := validateQueryFunction(clause.TimeAggregation); err != nil {
				return QueryPlanFacts{}, err
			}
		}
		for _, condition := range clause.Conditions.Fields {
			if condition.Field == "" || condition.Operator == "" || len(condition.Values) == 0 {
				return QueryPlanFacts{}, errors.New("alarmd execution: incomplete typed query condition")
			}
			for _, value := range condition.Values {
				if err := value.Validate(); err != nil {
					return QueryPlanFacts{}, err
				}
			}
		}
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-query-plan-facts-v1", facts)
	if err != nil {
		return QueryPlanFacts{}, fmt.Errorf("alarmd execution: derive query revision: %w", err)
	}
	facts.QueryRevision = QueryRevision(digest)
	return facts, nil
}

func (value QueryScalar) Validate() error {
	switch value.Kind {
	case QueryScalarString:
		if value.StringValue == "" || value.NumberValue != "" || value.BoolValue {
			return errors.New("alarmd execution: invalid string query scalar")
		}
	case QueryScalarNumber:
		if value.NumberValue == "" || value.StringValue != "" || value.BoolValue {
			return errors.New("alarmd execution: invalid number query scalar")
		}
	case QueryScalarBoolean:
		if value.StringValue != "" || value.NumberValue != "" {
			return errors.New("alarmd execution: invalid boolean query scalar")
		}
	default:
		return errors.New("alarmd execution: unknown query scalar kind")
	}
	return nil
}

func validateQueryFunctions(functions []QueryFunction) error {
	for _, function := range functions {
		if err := validateQueryFunction(function); err != nil {
			return err
		}
	}
	return nil
}

func isZeroQueryFunction(function QueryFunction) bool {
	return function.Method == "" && function.Field == "" && !function.Without && len(function.Dimensions) == 0 &&
		function.Position == 0 && len(function.Arguments) == 0 && function.Window == "" && !function.Subquery &&
		function.Step == ""
}

func validateQueryFunction(function QueryFunction) error {
	if function.Method == "" || function.Position < 0 {
		return errors.New("alarmd execution: incomplete typed query function")
	}
	for _, argument := range function.Arguments {
		if err := argument.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (facts QueryPlanFacts) Validate() error {
	revision := facts.QueryRevision
	facts.QueryRevision = ""
	built, err := BuildQueryPlanFacts(facts)
	if err != nil {
		return err
	}
	if revision == "" || revision != built.QueryRevision {
		return errors.New("alarmd execution: query revision does not match query plan facts")
	}
	return nil
}
