package etl

import "github.com/cheekybits/genny/generic"

import (
	conv "github.com/cstockton/go-conv"
)

// CONVT :
type CONVT generic.Type

// TransformCONVT : convert value to CONVT, return default value when value is nil
func TransformCONVT(value interface{}) (interface{}, error) {
	if value == nil {
		var result CONVT
		return result, nil
	}
	result, err := conv.DefaultConv.CONVT(value)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// TransformAutoCONVT : convert value to CONVT auto
func TransformAutoCONVT(value interface{}) (interface{}, error) {
	result, err := conv.DefaultConv.CONVT(value)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// TransformNotNilCONVT : convert value to CONVT, return error when value is nil
func TransformNotNilCONVT(value interface{}) (interface{}, error) {
	if value == nil {
		return nil, ErrTypeNotSupported
	}
	result, err := conv.DefaultConv.CONVT(value)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// TransformNilCONVT : convert value to CONVT, return nil when value is nil
func TransformNilCONVT(value interface{}) (interface{}, error) {
	if value == nil {
		return nil, nil
	}
	result, err := conv.DefaultConv.CONVT(value)
	if err != nil {
		return nil, err
	}
	return result, nil
}
