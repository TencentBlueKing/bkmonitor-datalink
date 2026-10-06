package strategy

import (
	"reflect"
	"testing"
)

func TestTraditionalUnitConversionPreservesDescendingFactorOrder(t *testing.T) {
	for _, tc := range []struct {
		unit    string
		factors []int64
	}{{"decmbytes", []int64{1000, 1000}}, {"h", []int64{60, 60, 1000, 1000, 1000}}, {"d", []int64{24, 60, 60, 1000, 1000, 1000}}} {
		t.Run(tc.unit, func(t *testing.T) {
			conversion, ok := comparisonUnitConversion(tc.unit, nil)
			if !ok || !conversion.Round || !reflect.DeepEqual(conversion.Factors, tc.factors) {
				t.Fatalf("conversion %+v", conversion)
			}
		})
	}
	conversion, ok := comparisonUnitConversion("short", nil)
	if !ok || conversion.Round {
		t.Fatal("unscaled unit rounds")
	}
	prefix := "h"
	conversion, ok = comparisonUnitConversion("ns", &prefix)
	if !ok || !reflect.DeepEqual(conversion.Factors, []int64{60, 60, 1000, 1000, 1000}) {
		t.Fatalf("prefix conversion %+v", conversion)
	}
	plan := CompiledAlgorithmPlan{config: compiledAlgorithmConfig{TraditionalComparison: &TraditionalComparisonConfig{DataConversion: conversion, AlgorithmConversion: conversion, ThresholdConversion: conversion}}}
	config, _ := plan.TraditionalComparisonConfig()
	config.DataConversion.Factors[0] = 1
	config.AlgorithmConversion.Factors[0] = 2
	config.ThresholdConversion.Factors[0] = 3
	frozen, _ := plan.TraditionalComparisonConfig()
	if frozen.DataConversion.Factors[0] != 60 || frozen.AlgorithmConversion.Factors[0] != 60 || frozen.ThresholdConversion.Factors[0] != 60 {
		t.Fatal("unit factors escaped immutable plan")
	}
}
