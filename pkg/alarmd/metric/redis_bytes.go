// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"encoding"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

// What a command costs on the wire, worked out from the command itself: the
// connection is not touched, and only there could a byte be counted by
// watching it go, but a connection serves every job on a client and cannot say
// whose bytes they were. Replies are worked back from what go-redis parsed, so
// a status and a bulk string nested in a script's reply read alike. Connection
// setup (AUTH, SELECT) and Sentinel traffic are not counted.

const (
	respTerminator = 2 // \r\n
	respNilBytes   = 5 // $-1\r\n
	respBoolBytes  = 4 // :1\r\n
)

func decimalDigits(value int64) int {
	digits := 1
	if value < 0 {
		digits++
		value = -value
	}
	for value >= 10 {
		value /= 10
		digits++
	}
	return digits
}

func unsignedDigits(value uint64) int {
	digits := 1
	for value >= 10 {
		value /= 10
		digits++
	}
	return digits
}

func bulkBytes(length int) int {
	return 1 + decimalDigits(int64(length)) + respTerminator + length + respTerminator
}

func arrayHeaderBytes(length int) int {
	return 1 + decimalDigits(int64(length)) + respTerminator
}

func integerBytes(value int64) int {
	return 1 + decimalDigits(value) + respTerminator
}

func floatLength(value float64) int {
	var buffer [32]byte
	return len(strconv.AppendFloat(buffer[:0], value, 'f', -1, 64))
}

// sentBytes is a command as go-redis writes it (proto.Writer.WriteArgs), an
// array of bulk strings, with each argument's type written the same way.
func sentBytes(args []interface{}) int {
	total := arrayHeaderBytes(len(args))
	for _, arg := range args {
		total += bulkBytes(argumentLength(arg))
	}
	return total
}

func argumentLength(arg interface{}) int {
	switch value := arg.(type) {
	case nil:
		return 0
	case string:
		return len(value)
	case []byte:
		return len(value)
	case int:
		return decimalDigits(int64(value))
	case int8:
		return decimalDigits(int64(value))
	case int16:
		return decimalDigits(int64(value))
	case int32:
		return decimalDigits(int64(value))
	case int64:
		return decimalDigits(value)
	case uint:
		return unsignedDigits(uint64(value))
	case uint8:
		return unsignedDigits(uint64(value))
	case uint16:
		return unsignedDigits(uint64(value))
	case uint32:
		return unsignedDigits(uint64(value))
	case uint64:
		return unsignedDigits(value)
	case float32:
		return floatLength(float64(value))
	case float64:
		return floatLength(value)
	case bool:
		return 1
	case time.Time:
		var buffer [40]byte
		return len(value.AppendFormat(buffer[:0], time.RFC3339Nano))
	case time.Duration:
		return decimalDigits(value.Nanoseconds())
	case encoding.BinaryMarshaler:
		encoded, err := value.MarshalBinary()
		if err != nil {
			return 0
		}
		return len(encoded)
	}
	return 0
}

// gotReply says the server answered: only then was the request certainly sent
// and is there a reply to count. A pool timeout or a failed dial sent nothing,
// and counting it would read as traffic in the window where there was none; a
// connection cut half way through a write counts nothing either.
func gotReply(err error) bool {
	if err == nil || err == redis.Nil {
		return true
	}
	_, replied := err.(redis.Error)
	return replied
}

// receivedBytes is the reply of a command the server answered (gotReply). A
// reply type not handled here counts nothing, and
// TestEveryReplyTypeTheCodeReadsIsMeasured fails for any type the module's
// calls return.
func receivedBytes(cmd redis.Cmder, command string) int {
	if err := cmd.Err(); err != nil {
		if err == redis.Nil {
			return respNilBytes
		}
		return 1 + len(err.Error()) + respTerminator
	}
	switch reply := cmd.(type) {
	case *redis.Cmd:
		return valueBytes(reply.Val())
	case *redis.StringCmd:
		return bulkBytes(len(reply.Val()))
	case *redis.StatusCmd:
		return 1 + len(reply.Val()) + respTerminator
	case *redis.IntCmd:
		return integerBytes(reply.Val())
	case *redis.BoolCmd:
		if command == "set" {
			return respNilBytes // +OK\r\n or $-1\r\n: SET NX answers the two alike in length
		}
		return respBoolBytes
	case *redis.DurationCmd:
		return integerBytes(durationReplyValue(reply.Val(), command))
	case *redis.SliceCmd:
		return valueBytes(reply.Val())
	case *redis.StringSliceCmd:
		return stringsBytes(reply.Val())
	case *redis.StringStringMapCmd:
		total := arrayHeaderBytes(2 * len(reply.Val()))
		for key, value := range reply.Val() {
			total += bulkBytes(len(key)) + bulkBytes(len(value))
		}
		return total
	case *redis.ScanCmd:
		keys, cursor := reply.Val()
		return arrayHeaderBytes(2) + bulkBytes(unsignedDigits(cursor)) + stringsBytes(keys)
	}
	return 0
}

func stringsBytes(values []string) int {
	total := arrayHeaderBytes(len(values))
	for _, value := range values {
		total += bulkBytes(len(value))
	}
	return total
}

// durationReplyValue is the integer the server sent for TTL or PTTL, which
// go-redis turned into a duration; -1 and -2 it left as they came.
func durationReplyValue(value time.Duration, command string) int64 {
	switch {
	case value < 0:
		return int64(value)
	case command == "ttl":
		return int64(value / time.Second)
	}
	return int64(value / time.Millisecond)
}

// valueBytes is one element of a reply as go-redis's generic reader leaves it.
func valueBytes(value interface{}) int {
	switch element := value.(type) {
	case nil:
		return respNilBytes
	case string:
		return bulkBytes(len(element))
	case int64:
		return integerBytes(element)
	case []interface{}:
		total := arrayHeaderBytes(len(element))
		for _, item := range element {
			total += valueBytes(item)
		}
		return total
	case error:
		return 1 + len(element.Error()) + respTerminator
	}
	return 0
}
