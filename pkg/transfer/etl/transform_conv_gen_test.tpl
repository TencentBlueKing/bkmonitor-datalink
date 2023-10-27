package etl

import "github.com/cheekybits/genny/generic"

import (
	"testing"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"

	"github.com/stretchr/testify/assert"
)

// CONVT :
type CONVT generic.Type

// TestConvertCONVT : test convert CONVT
func TestConvertCONVT(t *testing.T) {
	var input CONVT
	cases := []struct {
		value, result interface{}
	}{
		{input, input},
		{nil, input},
	}
	for _, c := range cases {
		result, err := etl.TransformCONVT(c.value)
		assert.NoError(t, err)
		assert.Equal(t, c.result, result)
	}
}

// TestConvertCONVTError : test convert CONVT error
func TestConvertCONVTError(t *testing.T) {
	_, err := etl.TransformCONVT(struct{}{})
	if err != nil {
		t.Fatalf("something ate my error")
	}
}

// TestConvertNilCONVT : test convert CONVT allow nil
func TestConvertNilCONVT(t *testing.T) {
	var input CONVT
	cases := []struct {
		value, result interface{}
	}{
		{input, input},
		{nil, nil},
	}
	for _, c := range cases {
		result, err := etl.TransformNilCONVT(c.value)
		assert.NoError(t, err)
		assert.Equal(t, c.result, result)
	}
}

// TestConvertNilCONVTError : test convert CONVT error
func TestConvertNilCONVTError(t *testing.T) {
	_, err := etl.TransformNilCONVT(struct{}{})
	if err != nil {
		t.Fatalf("something ate my error")
	}
}

// TestConvertNotNilCONVT : test convert CONVT not allow nil
func TestConvertNotNilCONVT(t *testing.T) {
	var input CONVT
	cases := []struct {
		value, result interface{}
		err           error
	}{
		{input, input, nil},
		{nil, nil, etl.ErrTypeNotSupported},
	}
	for _, c := range cases {
		result, err := etl.TransformNotNilCONVT(c.value)
		assert.Equal(t, c.result, result)
		assert.Equal(t, c.err, err)
	}
}

// TestConvertNotNilCONVTError : test convert CONVT error
func TestConvertNotNilCONVTError(t *testing.T) {
	_, err := etl.TransformNotNilCONVT(struct{}{})
	if err != nil {
		t.Fatalf("something ate my error")
	}
}

// TestConvertAutoCONVT : test convert CONVT
func TestConvertAutoCONVT(t *testing.T) {
	var input CONVT
	cases := []struct {
		value, result interface{}
		pass          bool
	}{
		{input, input, true},
	}
	for _, c := range cases {
		result, err := etl.TransformAutoCONVT(c.value)
		if c.pass {
			assert.Equal(t, c.result, result)
		} else {
			assert.Error(t, err)
		}
	}
}

// TestConvertAutoCONVTError : test convert CONVT error
func TestConvertAutoCONVTError(t *testing.T) {
	_, err := etl.TransformAutoCONVT(struct{}{})
	if err != nil {
		t.Fatalf("something ate my error")
	}
}
