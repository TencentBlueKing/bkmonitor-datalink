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
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/elastic/beats/libbeat/common"
)

type MapStr = common.MapStr

const (
	TimeFormat     = "2006-01-02 15:04:05"
	TimeZoneFormat = "Z07"
)

// DateTime
type DateTime struct {
	Zone     int    `json:"timezone"`
	Datetime string `json:"datetime"`
	UTCTime  string `json:"utctime"`
	Country  string `json:"country"`
	City     string `json:"city"`
}

// FirstCharToUpper
func FirstCharToUpper(str string) string {
	for i, v := range str {
		return string(unicode.ToUpper(v)) + str[i+1:]
	}
	return ""
}

func FirstCharToLower(str string) string {
	for i, v := range str {
		return string(unicode.ToLower(v)) + str[i+1:]
	}
	return ""
}

func CharsToString(ca []int8) string {
	s := make([]byte, len(ca))
	var lens int
	for ; lens < len(ca); lens++ {
		if ca[lens] == 0 {
			break
		}
		s[lens] = uint8(ca[lens])
	}
	return string(s[0:lens])
}

func ReadLines(filename string) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return []string{""}, err
	}
	defer f.Close()

	var ret []string

	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		ret = append(ret, strings.Trim(line, "\n"))
	}

	return ret, nil
}

func GetDateTime() (localtime string, utctime string, zone int) {
	t := time.Now()
	var err error
	zone, err = strconv.Atoi(t.Format(TimeZoneFormat))
	if err != nil {
		zone = 0
		// logp.Warn("strconv.Atoi Err: ", err)
	}
	localtime = t.Format(TimeFormat)
	utctime = t.UTC().Format(TimeFormat)
	return
}

func GetLocation() (country, city string, err error) {
	// Redhat,CentOS
	// #cat /etc/sysconfig/clock
	// ZONE="Asia/Shanghai"
	path := "/etc/sysconfig/clock"
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return
	}
	content := string(b)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		words := strings.Split(line, "=")
		if words[0] == "ZONE" {
			zone := strings.Trim(words[1], "\"")
			regAndCity := strings.Split(zone, "/")
			if len(regAndCity) != 2 {
				country = ""
				city = ""
				err = nil
				return
			}
			country = regAndCity[0]
			city = regAndCity[1]
			err = nil
			return
		}
	}

	// Ubuntu, Debian(releases Etch and later）
	// # cat /etc/timezone
	// America/New_York
	path = "/etc/timezone"
	b, err = ioutil.ReadFile(path)
	if err != nil {
		return
	}
	zone := string(b)
	regAndCity := strings.Split(zone, "/")
	if len(regAndCity) != 2 {
		country = ""
		city = ""
		err = nil
		return
	}
	country = regAndCity[0]
	city = regAndCity[1]
	err = nil
	return
}

func GetUTCTimestamp() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}

// DiffList : different list, return: left - right
// ex: left=[1,2,3], right=[2,3,4]. result=[1]
func DiffList(left []uint16, right []uint16) []uint16 {
	result := []uint16{}
	m := map[uint16]bool{}
	for _, e := range left {
		m[e] = true
	}
	for _, e := range right {
		m[e] = false
	}
	for e, v := range m {
		if v {
			result = append(result, e)
		}
	}
	return result
}

// AddList: left + right
// ex: left=[1,2,3], right=[2,3,4]. result=[1,2,3,2,3,4]
func AddList(left []uint16, right []uint16) []uint16 {
	return append(left, right...)
}

// CombineList: left + right, remove duplicate elements
// ex: left=[1,2,3], right=[2,3,4]. result=[1,2,3,4]
func CombineList(left []uint16, right []uint16) []uint16 {
	remain := DiffList(right, left)
	return AddList(left, remain)
}

// PrintStruct print struct to json
// just for debug
func PrintStruct(data interface{}) {
	jsonbytes, err := json.Marshal(data)
	if err != nil {
		fmt.Println(err)
		// log.Error("convert to json faild: ", err)
		return
	}
	fmt.Println(string(jsonbytes))
}

// ErrNotImplemented
type ErrNotImplemented struct {
	OS string
}

func (e ErrNotImplemented) Error() string {
	return "not implemented on " + e.OS
}

// TryToInt converts value to int, if not a number, return the key
func TryToInt(key string) interface{} {
	value, err := strconv.ParseInt(key, 10, 64)
	if err != nil {
		return key
	}
	return value
}

// TryToNumber converts string to number, if not a number, return the key
func TryToNumber(key string) interface{} {
	intValue, err := strconv.ParseInt(key, 10, 64)
	if err == nil {
		return intValue
	}
	floatValue, err := strconv.ParseFloat(key, 64)
	if err == nil {
		return floatValue
	}
	return key
}

// TryToFloat64 try int, uint, float numbers to float64. not work with string and others type
func TryToFloat64(number interface{}) (float64, bool) {
	rv := reflect.ValueOf(number)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(reflect.ValueOf(number).Int()), true
	case reflect.Uint, reflect.Uintptr, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(reflect.ValueOf(number).Uint()), true
	case reflect.Float32, reflect.Float64:
		return float64(reflect.ValueOf(number).Float()), true
	default:
		return 0, false
	}
}

var EPSILON float64 = 0.0000001

// FloatEquals compare float64 values, can has EPSILON precision
func FloatEquals(a, b float64) bool {
	return (a-b) < EPSILON && (b-a) < EPSILON
}

// MakePifFilePath make a new pid path
// default pid lock file: procPath/pid.file
// or pid lock file: pidFilePath/procName.pid if set runPath
func MakePifFilePath(procName, runPath string) (string, error) {
	pidFilePath := ""
	if runPath == "" {
		absPath, err := filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			return "", err
		}
		pidFilePath = filepath.Join(absPath, "pid.file")
	} else {
		// create pid lock file
		procName = procName + ".pid"
		pidFilePath = filepath.Join(runPath, procName)
	}
	return pidFilePath, nil
}

// reference from lockfile
func ScanPidLine(content []byte) int {
	if len(content) == 0 {
		return -1
	}

	var pid int
	if _, err := fmt.Sscanln(string(content), &pid); err != nil {
		return -1
	}

	if pid <= 0 {
		return -1
	}
	return pid
}

// Min unsigned integer
func MinUInt(x, y uint64) uint64 {
	if x < y {
		return x
	}
	return y
}

// Max unsigned integer
func MaxUInt(x, y uint64) uint64 {
	if x > y {
		return x
	}
	return y
}

// ValidateIPAddress validates an Ip address.
func ValidateIPAddress(val string) bool {
	ip := net.ParseIP(strings.TrimSpace(val))
	if ip != nil {
		return true
	}
	return false
}

// Ip2uint32Littlendian transport ip to uint32 127.0.0.1<=>16777343
func Ip2uint32Littlendian(ip string) uint32 {
	var long uint32
	if net.ParseIP(ip) == nil {
		return 0
	}
	binary.Read(bytes.NewBuffer(net.ParseIP(ip).To4()), binary.LittleEndian, &long)
	net.ParseIP(ip)
	return long
}

// UInt32ToipLittlendian transport uint to ip 16777343 <=> 127.0.0.1
func UInt32ToipLittlendian(nn uint32) string {
	ip := make(net.IP, 4)
	binary.LittleEndian.PutUint32(ip, nn)
	return ip.String()
}

// CounterDiff considering overflow
func CounterDiff(a, b, max uint64) uint64 {
	if a < b {
		return a + max - b
	} else {
		return a - b
	}
}

// IsValidUrl tests a string to determine if it is a url or not.
func IsValidUrl(str string) bool {
	_, err := url.ParseRequestURI(str)
	if err != nil {
		return false
	} else {
		return true
	}
}

// MD5 make md5 to string
func MD5(text string) string {
	ctx := md5.New()
	ctx.Write([]byte(text))
	return hex.EncodeToString(ctx.Sum(nil))
}
