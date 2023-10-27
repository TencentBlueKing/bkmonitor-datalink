package etl

import "github.com/cheekybits/genny/generic"

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"

	"github.com/stretchr/testify/assert"
)

// CONVT :
type CONVT generic.Type

// TestTransformMultiplyByCONVT :
func TestTransformMultiplyByCONVT(t *testing.T) {
	cases := []struct {
		left, right interface{}
	}{
		{1, 2},
		{3, 3},
		{5, 4},
	}
	for _, c := range cases {
		fn := etl.TransformMultiplyByCONVT(c.right)
		result, err := fn(c.left)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	}
}

// TestTransformDivideByCONVT :
func TestTransformDivideByCONVT(t *testing.T) {
	cases := []struct {
		left, right interface{}
	}{
		{1, 2},
		{3, 3},
		{5, 4},
	}
	for _, c := range cases {
		fn := etl.TransformDivideByCONVT(c.right)
		result, err := fn(c.left)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	}
}

// TestTransformAddByCONVT :
func TestTransformAddByCONVT(t *testing.T) {
	cases := []struct {
		left, right interface{}
	}{
		{1, 2},
		{3, 3},
		{5, 4},
	}
	for _, c := range cases {
		fn := etl.TransformAddByCONVT(c.right)
		result, err := fn(c.left)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	}
}

// TestTransformSubtractByCONVT :
func TestTransformSubtractByCONVT(t *testing.T) {
	cases := []struct {
		left, right interface{}
	}{
		{1, 2},
		{3, 3},
		{5, 4},
	}
	for _, c := range cases {
		fn := etl.TransformSubtractByCONVT(c.right)
		result, err := fn(c.left)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	}
}