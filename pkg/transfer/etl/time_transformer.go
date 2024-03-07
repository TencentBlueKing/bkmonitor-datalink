// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"strconv"
	"time"

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/types"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// TimeParser :
type TimeParser func(layout, value string, loc *time.Location) (time.Time, error)

// TimeFormatter :
type TimeFormatter func(layout string, t time.Time) string

func defaultTimeParser(layout, value string, loc *time.Location) (time.Time, error) {
	return time.ParseInLocation(layout, value, loc)
}

func timestampParserByDuration(d time.Duration) TimeParser {
	duration := int64(d)
	return func(layout, value string, loc *time.Location) (i time.Time, e error) {
		return time.Unix(0, conv.Int64(value)*duration), nil
	}
}

var layoutParsers = map[string]TimeParser{
	"epoch_nanos":  timestampParserByDuration(time.Nanosecond),
	"epoch_micros": timestampParserByDuration(time.Microsecond),
	"epoch_millis": timestampParserByDuration(time.Millisecond),
	"epoch_second": timestampParserByDuration(time.Second),
	"epoch_minute": timestampParserByDuration(time.Minute),
}

func timestampFormatByDuration(d time.Duration) TimeFormatter {
	duration := int64(d / time.Nanosecond)
	return func(layout string, t time.Time) string {
		return strconv.FormatInt(t.UnixNano()/duration, 10)
	}
}

func defaultTimeFormatter(layout string, t time.Time) string {
	return t.Format(layout)
}

var layoutFormatters = map[string]TimeFormatter{
	"epoch_nanos":  timestampFormatByDuration(time.Nanosecond),
	"epoch_micros": timestampFormatByDuration(time.Microsecond),
	"epoch_millis": timestampFormatByDuration(time.Millisecond),
	"epoch_second": timestampFormatByDuration(time.Second),
	"epoch_minute": timestampFormatByDuration(time.Minute),
}

// TransformNumberToTime
func TransformNumberToTime(from interface{}) (interface{}, error) {
	value, err := utils.ParseTime(from)
	if err != nil {
		return nil, err
	}

	return value.UTC(), nil
}

// TransformUTCTime :
func TransformUTCTime(layout string, tz int) TransformFn {
	loc := utils.ParseFixedTimeZone(tz)
	parser, ok := layoutParsers[layout]
	if !ok {
		parser = defaultTimeParser
	}

	return func(value interface{}) (interface{}, error) {
		var ts string
		switch t := value.(type) {
		case int, int64, float64:
			return TransformNumberToTime(value)
		case string:
			ts = t
		case []byte:
			ts = string(t)
		case nil:
			return nil, nil
		case time.Time:
			return t.UTC(), nil
		case types.TimeStamp:
			return t.Time.UTC(), nil
		default:
			ts = conv.String(t)
		}

		t, err := parser(layout, ts, loc)
		return t.UTC(), err
	}
}

// TransformTimeWithUTCLayout :
func TransformTimeWithUTCLayout(layout string) TransformFn {
	return TransformUTCTime(layout, 0)
}

// TransformUTCTimeStamp :
func TransformUTCTimeStamp(layout string, tz int) TransformFn {
	fn := TransformUTCTime(layout, tz)
	return func(from interface{}) (to interface{}, err error) {
		v, err := fn(from)
		if err != nil {
			return nil, err
		}
		return TransformAutoTimeStamp(v)
	}
}

// TransformTimeStampWithUTCLayout :
func TransformTimeStampWithUTCLayout(layout string) TransformFn {
	return TransformUTCTimeStamp(layout, 0)
}

// TransformTimeStamp :
func TransformTimeStamp(value interface{}) (interface{}, error) {
	ts, err := conv.DefaultConv.Int64(value)
	if err != nil {
		return nil, err
	}
	return types.NewTimeStamp(time.Unix(ts, 0)), nil
}

// TransformAutoTimeStamp :
func TransformAutoTimeStamp(value interface{}) (interface{}, error) {
	t, err := conv.DefaultConv.Time(value)
	if err != nil {
		return TransformTimeStamp(value)
	}
	return types.NewTimeStamp(t), nil
}

// TransformTimeByName :
func TransformTimeByName(name string, tz int) TransformFn {
	layout, ok := define.GetTimeLayout(name)
	if !ok {
		return TransformErrorForever(errors.WithMessagef(define.ErrValue, "unknown layout name %s", name))
	}
	return TransformUTCTime(layout, tz)
}

// TransformTimeStampByName :
func TransformTimeStampByName(name string, tz int) TransformFn {
	layout, ok := define.GetTimeLayout(name)
	if !ok {
		return TransformErrorForever(errors.WithMessagef(define.ErrValue, "unknown layout name %s", name))
	}
	return TransformUTCTimeStamp(layout, tz)
}

// TransformTimeStingByLayout :
func TransformTimeStingByLayout(layout string) TransformFn {
	formatter, ok := layoutFormatters[layout]
	if !ok {
		formatter = defaultTimeFormatter
	}
	return func(from interface{}) (to interface{}, err error) {
		var value time.Time
		switch from.(type) {
		case int, int64, float64:
			v, err := TransformNumberToTime(from)
			if err != nil {
				return nil, err
			}
			value = v.(time.Time)
		default:
			value, err = conv.DefaultConv.Time(from)
			if err != nil {
				return nil, err
			}
			value = value.UTC()
		}
		return formatter(layout, value), nil
	}
}

// TransformTimeStampStingByLayout :
func TransformTimeStampStingByLayout(layout string) TransformFn {
	fn := TransformTimeStingByLayout(layout)
	return func(from interface{}) (to interface{}, err error) {
		value, ok := from.(types.TimeStamp)
		if !ok {
			return errors.WithMessagef(define.ErrType, "type %T not supported", from), nil
		}
		return fn(value.Time)
	}
}

// TransformTimeStingByName :
func TransformTimeStingByName(name string) TransformFn {
	layout, ok := define.GetTimeLayout(name)
	if !ok {
		return TransformErrorForever(errors.WithMessagef(define.ErrValue, "unknown layout name %s", name))
	}
	return TransformTimeStingByLayout(layout)
}

// TransformTimeStampStingByName :
func TransformTimeStampStingByName(name string) TransformFn {
	layout, ok := define.GetTimeLayout(name)
	if !ok {
		return TransformErrorForever(errors.WithMessagef(define.ErrValue, "unknown layout name %s", name))
	}
	return TransformTimeStampStingByLayout(layout)
}
