package etl

import "github.com/cheekybits/genny/generic"

import "github.com/cstockton/go-conv"

// CONVT :
type CONVT generic.Type

// TransformMultiplyByCONVT :
func TransformMultiplyByCONVT(right interface{}) func(interface{}) (interface{}, error) {
	number, err := conv.DefaultConv.CONVT(right)
	return func(value interface{}) (interface{}, error) {
		if err != nil {
			return nil, err
		}
		left, err := conv.DefaultConv.CONVT(value)
		if err != nil {
			return nil, err
		}
		return left * number, nil
	}
}

// TransformDivideByCONVT :
func TransformDivideByCONVT(right interface{}) func(interface{}) (interface{}, error) {
	number, err := conv.DefaultConv.CONVT(right)
	return func(value interface{}) (interface{}, error) {
		if err != nil {
			return nil, err
		}
		left, err := conv.DefaultConv.CONVT(value)
		if err != nil {
			return nil, err
		}
		return left / number, nil
	}
}

// TransformAddByCONVT :
func TransformAddByCONVT(right interface{}) func(interface{}) (interface{}, error) {
	number, err := conv.DefaultConv.CONVT(right)
	return func(value interface{}) (interface{}, error) {
		if err != nil {
			return nil, err
		}
		left, err := conv.DefaultConv.CONVT(value)
		if err != nil {
			return nil, err
		}
		return left + number, nil
	}
}

// TransformSubtractByCONVT :
func TransformSubtractByCONVT(right interface{}) func(interface{}) (interface{}, error) {
	number, err := conv.DefaultConv.CONVT(right)
	return func(value interface{}) (interface{}, error) {
		if err != nil {
			return nil, err
		}
		left, err := conv.DefaultConv.CONVT(value)
		if err != nil {
			return nil, err
		}
		return left - number, nil
	}
}
