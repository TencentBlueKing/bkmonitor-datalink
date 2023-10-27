// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func Test_FirstCharToUpper(t *testing.T) {
	var s, res string

	s = "Abc"
	res = FirstCharToUpper(s)
	if res != "Abc" {
		t.Error(res + "!= abc")
		t.FailNow()
	}

	s = "abc"
	res = FirstCharToUpper(s)
	if res != "Abc" {
		t.Error(res + "!= abc")
		t.FailNow()
	}

	s = ""
	res = FirstCharToUpper(s)
	if res != "" {
		t.Error(res + " is not empty")
		t.FailNow()
	}
}

func Test_FirstCharToLower(t *testing.T) {
	var s, res string

	s = "Abc"
	res = FirstCharToLower(s)
	if res != "abc" {
		t.Error(res + "!= abc")
		t.FailNow()
	}

	s = "abc"
	res = FirstCharToLower(s)
	if res != "abc" {
		t.Error(res + "!= abc")
		t.FailNow()
	}

	s = ""
	res = FirstCharToLower(s)
	if res != "" {
		t.Error(res + " is not empty")
		t.FailNow()
	}
}

func Test_CharsToString(t *testing.T) {
	cs := make([]int8, 64)
	cs[0] = 76
	cs[1] = 73
	cs[2] = 78
	cs[3] = 85
	cs[4] = 88
	s := CharsToString(cs)
	if s != "LINUX" {
		t.Error("CharsToString failed", s)
		t.FailNow()
	}
}

func Test_ReadLines(t *testing.T) {
	// try to read this test file
	// and head with "package common"
	lines, err := ReadLines("common_test.go")
	if err != nil {
		t.Error("ReadLines common_test.go failed", err)
		t.FailNow()
	}
	if len(lines) == 0 {
		t.Error("common_test.go empty")
		t.FailNow()
	}

	if !strings.HasPrefix(lines[0], "package common") {
		t.Error("read common_test.go content error")
		t.FailNow()
	}
}

func Test_DiffList(t *testing.T) {
	a := []uint16{1, 2, 3}
	b := []uint16{4, 2, 3}
	c := DiffList(a, b)

	// result
	realRst := []uint16{1}

	if !reflect.DeepEqual(c, realRst) {
		t.FailNow()
	}
}

func Test_GetLocation(t *testing.T) {
	country, city, err := GetLocation()
	if err != nil {
		t.Log("can not get location")
		t.Log(err)
	} else {
		t.Log(country, city)
	}
}

func Test_GetDateTime(t *testing.T) {
	local, utc, zone := GetDateTime()
	t.Log(local, utc, zone)
}

func Test_GetUTCTimestamp(t *testing.T) {
	utc := GetUTCTimestamp()

	//  1xxxxxxxxx
	// now utc start with 1, and len is 10
	if !strings.HasPrefix(utc, "1") {
		t.Error("GetUTCTimestamp", utc)
		t.FailNow()
	}
	if len(utc) != 10 {
		t.Error("GetUTCTimestamp", utc)
		t.FailNow()
	}
}

func Test_AddList(t *testing.T) {
	a := []uint16{1, 2, 3}
	b := []uint16{4, 3, 5}
	c := AddList(a, b)

	// result
	realRst := []uint16{1, 2, 3, 4, 3, 5}

	if !reflect.DeepEqual(c, realRst) {
		t.FailNow()
	}
}

func Test_CombineList(t *testing.T) {
	a := []uint16{1, 2, 3}
	b := []uint16{4, 3, 5}
	c := CombineList(a, b)

	// result
	realRst := []uint16{1, 2, 3, 4, 5}
	realRst2 := []uint16{1, 2, 3, 5, 4}

	if !reflect.DeepEqual(c, realRst) && !reflect.DeepEqual(c, realRst2) {
		t.Error(c)
		t.FailNow()
	}
}

func Test_PrintStruct(t *testing.T) {
	s := make(map[string]string)
	s["k1"] = "v1"
	PrintStruct(s)                // will print '{"k1":"v1"}'
	PrintStruct(Test_PrintStruct) // will print error msg, 'json: unsupported type: func(*testing.T)'
}

func Test_Error(t *testing.T) {
	err := ErrNotImplemented{OS: "linux"}
	s := err.Error()
	msg := "not implemented on linux"
	if s != msg {
		t.Error("err msg not correct")
		t.FailNow()
	}
}

func Test_TryToInt(t *testing.T) {
	s := "123"
	f := TryToInt(s)
	if f == s || reflect.TypeOf(f).Kind() != reflect.Int64 {
		t.Log("TryToNumber with int fail")
		t.FailNow()
	}

	s = "-123"
	f = TryToInt(s)
	if f == s || reflect.TypeOf(f).Kind() != reflect.Int64 {
		t.Log("TryToNumber with negtive int fail")
		t.FailNow()
	}

	s = "123.1234"
	f = TryToInt(s)
	if f != s {
		t.Log("TryToNumber with float fail")
		t.FailNow()
	}

	s = "-123a"
	f = TryToInt(s)
	if f != s {
		t.Log("TryToNumber with string fail")
		t.FailNow()
	}
}

func Test_TryToNumber(t *testing.T) {
	s := "123.1234"
	f := TryToNumber(s)
	if f == s || reflect.TypeOf(f).Kind() != reflect.Float64 {
		t.Log("TryToNumber with float fail")
		t.FailNow()
	}

	s = "123"
	f = TryToNumber(s)
	if f == s || reflect.TypeOf(f).Kind() != reflect.Int64 {
		t.Log("TryToNumber with int fail")
		t.FailNow()
	}

	s = "-123"
	f = TryToNumber(s)
	if f == s || reflect.TypeOf(f).Kind() != reflect.Int64 {
		t.Log("TryToNumber with negtive int fail")
		t.FailNow()
	}

	s = "-123a"
	f = TryToNumber(s)
	if f != s {
		t.Log("TryToNumber with string fail")
		t.FailNow()
	}
}

func Test_TryToFloat64(t *testing.T) {
	want := float64(123.1234)

	s := "123.123"
	_, success := TryToFloat64(s)
	if success {
		t.Log("TryToFloat64 should not work with string now")
		t.FailNow()
	}

	f32 := float32(123.1234)
	f, success := TryToFloat64(f32) // float32 lost precision
	if !success || !(math.Abs(f-want) < 0.00001) {
		t.Log("TryToFloat64 with float32 fail")
		t.FailNow()
	}

	f64 := float64(123.1234)
	f, success = TryToFloat64(f64)
	if !success || !FloatEquals(f, want) {
		t.Error("TryToFloat64 with float64 fail")
		t.FailNow()
	}

	i := int(-123)
	f, success = TryToFloat64(i)
	if !success || !FloatEquals(f, -123) {
		t.Error("TryToFloat64 with int fail")
		t.FailNow()
	}

	i32 := int32(-123)
	f, success = TryToFloat64(i32)
	if !success || !FloatEquals(f, -123) {
		t.Error("TryToFloat64 with int32 fail")
		t.FailNow()
	}

	i64 := int64(-123)
	f, success = TryToFloat64(i64)
	if !success || !FloatEquals(f, -123) {
		t.Error("TryToFloat64 with int64 fail")
		t.FailNow()
	}

	ui := int(123)
	f, success = TryToFloat64(ui)
	if !success || !FloatEquals(f, 123) {
		t.Error("TryToFloat64 with int fail")
		t.FailNow()
	}

	ui32 := int32(123)
	f, success = TryToFloat64(ui32)
	if !success || !FloatEquals(f, 123) {
		t.Error("TryToFloat64 with int32 fail")
		t.FailNow()
	}

	ui64 := int64(123)
	f, success = TryToFloat64(ui64)
	if !success || !FloatEquals(f, 123) {
		t.Error("TryToFloat64 with int64 fail")
		t.FailNow()
	}
}

func Test_MakePifFilePath(t *testing.T) {
	procName := "testbeat"
	runPath := ""
	path, err := MakePifFilePath(procName, runPath)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}

	if !strings.HasSuffix(path, "pid.file") {
		t.Error(err)
		t.FailNow()
	}
}

func Test_ScanPidLine(t *testing.T) {
	c := "123\n"
	pid := ScanPidLine([]byte(c))
	if pid != 123 {
		t.Error("parse pid failed")
		t.FailNow()
	}

	c = "123"
	pid = ScanPidLine([]byte(c))
	if pid != 123 {
		t.Error("parse pid failed")
		t.FailNow()
	}

	// wrong pids
	c = ""
	pid = ScanPidLine([]byte(c))
	if pid != -1 {
		t.Error("parse pid failed")
		t.FailNow()
	}

	c = "abc"
	pid = ScanPidLine([]byte(c))
	if pid != -1 {
		t.Error("parse pid failed")
		t.FailNow()
	}

	c = "-123"
	pid = ScanPidLine([]byte(c))
	if pid != -1 {
		t.Error("parse pid failed")
		t.FailNow()
	}
}

func Test_CounterDiff(t *testing.T) {
	res := CounterDiff(1, 2, math.MaxUint64)
	if res != 18446744073709551614 {
		t.Error("res is ", res)
		t.FailNow()
	}

	res = CounterDiff(2, 1, math.MaxUint64)
	if res != 1 {
		t.Error("res is ", res)
		t.FailNow()
	}
}

func Test_IsValidUrl(t *testing.T) {
	// = true
	if !IsValidUrl("http://www.xxx.com/abc") {
		t.FailNow()
	}

	// = false
	if IsValidUrl("xxx.com") {
		t.FailNow()
	}

	// = false
	if IsValidUrl("") {
		t.FailNow()
	}
}

func TestValidateIPAddress(t *testing.T) {
	ip := "123"
	if ValidateIPAddress(ip) {
		t.Fatal("ip check failed")
	}
	ip = "127.0.0.256"
	if ValidateIPAddress(ip) {
		t.Fatal("ip check failed")
	}
	ip = "0.0.0.0"
	if !ValidateIPAddress(ip) {
		t.Fatal("ip check failed")
	}
	ip = "127.0.0.1"
	if !ValidateIPAddress(ip) {
		t.Fatal("ip check failed")
	}
}

func Test_Ip2uint32Littlendian(t *testing.T) {
	cirAddr := Ip2uint32Littlendian("127.0.0.1")
	if cirAddr != 16777343 {
		t.Error("cirAddr is ", cirAddr)
		t.FailNow()
	}
	invalidAddr := Ip2uint32Littlendian("xxxxx")
	if invalidAddr != 0 {
		t.Error("invalidAddr is ", invalidAddr)
		t.FailNow()
	}
}

func Test_UInt32ToipLittlendian(t *testing.T) {
	cirAddrNum := UInt32ToipLittlendian(16777343)
	if cirAddrNum != "127.0.0.1" {
		t.Error("cirAddrNum is ", cirAddrNum)
		t.FailNow()
	}
}

func Test_MD5(t *testing.T) {
	s := MD5("hello")
	if s != "5d41402abc4b2a76b9719d911017c592" {
		msg := fmt.Sprintf("md5 error, %s", s)
		t.Fatal(msg)
	}
}
