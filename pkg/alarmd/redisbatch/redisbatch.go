// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

// Package redisbatch reads many string values in pipelined batches, and can
// know what a read will hold before it reads it: the values' lengths are read
// first, and the caller's admit decides how long a prefix of them one read
// takes. A batch's replies are held at once, and a value may be a megabyte;
// every batched read that must not hold more than it said it would reads
// this way.
package redisbatch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"
)

// Batch is how many keys one pipeline carries: one round trip reads that
// many values. It bounds what one reply holds - that many values - and how
// much one pipeline asks of Redis at once; a longer read is several
// pipelines, one after another. The keys may carry different hash tags:
// they are pipelined rather than sent as one MGET, which a cluster refuses
// across slots.
const Batch = 512

// Client is the one call a batched read makes: a pipeline, one round trip.
type Client interface {
	Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error)
}

// Value is one key's value as a batched read found it. Raw and Missing are
// facts about the key: its value, the reply's own bytes, not a copy, to be
// read and not written; or that it has none. Err is not a fact about the
// key: Redis answered the command with an error instead of the value -
// LOADING or BUSY while the instance restarts or runs a script, a key of
// another type - and said nothing about what the key holds. A caller changes
// nothing it holds for that key, and counts the read failed; the others of
// its batch still read. A batch whose transport failed fails as a whole.
type Value struct {
	Raw     []byte
	Missing bool
	Err     error
}

// UnansweredError is a window of which Redis answered some keys with an
// error rather than their values (Value.Err): Keys of them, the first
// First. The window's other values are facts; these keys' are not.
type UnansweredError struct {
	Keys  int
	First error
}

func (err *UnansweredError) Error() string {
	return fmt.Sprintf("redisbatch: Redis answered %d keys with an error, the first %v", err.Keys, err.First)
}

func (err *UnansweredError) Unwrap() error { return err.First }

// AnsweredCode is the code Redis answered a command with - its reply's
// leading word, such as LOADING, BUSY or WRONGTYPE - and whether err is
// Redis answering at all. A reply that leads with no such word is
// redis_error. Only the word is read, never the rest of the reply.
func AnsweredCode(err error) (string, bool) {
	var reply redis.Error
	if !errors.As(err, &reply) {
		return "", false
	}
	code, _, _ := strings.Cut(reply.Error(), " ")
	if len(code) == 0 || len(code) > 32 || strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_") != "" {
		return "redis_error", true
	}
	return code, true
}

// Answered reports whether err is Redis answering a command with an error,
// rather than the command not reaching it or its answer not coming back.
func Answered(err error) bool {
	var reply redis.Error
	return errors.As(err, &reply)
}

// Values reads keys in pipelined batches of Batch, one round trip each.
func Values(ctx context.Context, client Client, keys []string) ([]Value, error) {
	values := make([]Value, len(keys))
	for start := 0; start < len(keys); start += Batch {
		end := min(start+Batch, len(keys))
		replies := make([]*redis.StringCmd, end-start)
		if _, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for offset, key := range keys[start:end] {
				replies[offset] = pipe.Get(ctx, key)
			}
			return nil
		}); err != nil && !Answered(err) {
			return nil, err
		}
		for offset, reply := range replies {
			// The reply's own bytes, not a copy: a copy held the batch twice
			// until the replies were let go.
			value, err := reply.Bytes()
			switch {
			case errors.Is(err, redis.Nil):
				values[start+offset] = Value{Missing: true}
			case err != nil && Answered(err):
				values[start+offset] = Value{Err: err}
			case err != nil:
				return nil, err
			default:
				values[start+offset] = Value{Raw: value}
			}
		}
	}
	return values, nil
}

// Lengths is each key's value length, zero for one that is missing, in
// pipelined batches the way Values reads the values.
func Lengths(ctx context.Context, client Client, keys []string) ([]int64, error) {
	sizes := make([]int64, len(keys))
	for start := 0; start < len(keys); start += Batch {
		end := min(start+Batch, len(keys))
		replies := make([]*redis.IntCmd, end-start)
		if _, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for offset, key := range keys[start:end] {
				replies[offset] = pipe.StrLen(ctx, key)
			}
			return nil
		}); err != nil && !Answered(err) {
			return nil, err
		}
		for offset, reply := range replies {
			size, err := reply.Result()
			switch {
			case err != nil && Answered(err):
				// Redis answered this key with an error: its read will say so
				// in its place, and takes nothing to hold.
				sizes[start+offset] = 0
			case err != nil:
				return nil, err
			default:
				sizes[start+offset] = size
			}
		}
	}
	return sizes, nil
}

// Within reads the longest prefix of keys - at most Batch of them, one
// pipeline of lengths and one of values - whose values admit accepts, one
// value's length at a time in order, and says how many that was; the rest
// are left for another read. admit decides what a refusal means: a reader
// under the observation memory line reads nothing once the line says no, a
// detection reader keeps at least one (FirstThenWithin). A value that grows
// between its length and its read is read whole.
func Within(ctx context.Context, client Client, keys []string, admit func(bytes uint64) bool) ([]Value, int, error) {
	if len(keys) > Batch {
		keys = keys[:Batch]
	}
	sizes, err := Lengths(ctx, client, keys)
	if err != nil {
		return nil, 0, err
	}
	read := 0
	for read < len(sizes) && admit(uint64(sizes[read])) {
		read++
	}
	if read == 0 {
		return nil, 0, nil
	}
	values, err := Values(ctx, client, keys[:read])
	if err != nil {
		return nil, 0, err
	}
	return values, read, nil
}

// Windows reads keys in order a window at a time, for a reader that holds
// one window at once: each window is the values of the keys after the last
// one whose lengths add up to at most bound, or the one value larger than it
// (FirstThenWithin), one pipeline of values. Each key's length is read once,
// in pipelines of Batch ahead of the windows that take them: a window never
// spans two of those pipelines, and the lengths the window did not take are
// kept for the next rather than read again. A value that grows between its
// length and its read is read whole.
type Windows struct {
	client Client
	keys   []string
	bound  int
	// sizes are the lengths of keys[sized:sized+len(sizes)]; next is the
	// first key not read yet.
	sizes       []int64
	sized, next int
}

// NewWindows reads keys a window of at most bound bytes at a time.
func NewWindows(client Client, keys []string, bound int) *Windows {
	return &Windows{client: client, keys: keys, bound: bound}
}

// Next reads the next window: the values of keys[start:start+len(values)].
// It reads nothing, and returns no values, once every key has been read. A
// failed read leaves the window unread; the caller gives the read up. A
// window Redis answered some keys of with an error is read, and returned
// with an *UnansweredError: a caller that gives the read up on any error
// changes nothing for those keys by construction, and one that keeps the
// other keys' facts leaves those keys as they were and counts the read
// failed.
func (windows *Windows) Next(ctx context.Context) (start int, values []Value, err error) {
	if windows.next >= len(windows.keys) {
		return windows.next, nil, nil
	}
	if windows.next >= windows.sized+len(windows.sizes) {
		sizes, err := Lengths(ctx, windows.client, windows.keys[windows.next:min(windows.next+Batch, len(windows.keys))])
		if err != nil {
			return windows.next, nil, err
		}
		windows.sizes, windows.sized = sizes, windows.next
	}
	admit, end := FirstThenWithin(windows.bound), windows.next
	for end < windows.sized+len(windows.sizes) && admit(uint64(windows.sizes[end-windows.sized])) {
		end++
	}
	values, err = Values(ctx, windows.client, windows.keys[windows.next:end])
	if err != nil {
		return windows.next, nil, err
	}
	start, windows.next = windows.next, end
	var unanswered *UnansweredError
	for offset, value := range values {
		if value.Err == nil {
			continue
		}
		if unanswered == nil {
			unanswered = &UnansweredError{First: fmt.Errorf("%s: %w", windows.keys[start+offset], value.Err)}
		}
		unanswered.Keys++
	}
	if unanswered != nil {
		return start, values, unanswered
	}
	return start, values, nil
}

// FirstThenWithin admits the first value whatever its length, and after it
// values while their lengths add up to at most bound. A detection reader
// admits with it: every read makes progress, one value larger than the
// bound included, and holds at most the bound, or that one value, at once.
func FirstThenWithin(bound int) func(uint64) bool {
	admitted, spent := false, uint64(0)
	return func(bytes uint64) bool {
		if admitted && (bound <= 0 || spent+bytes > uint64(bound)) {
			return false
		}
		admitted, spent = true, spent+bytes
		return true
	}
}
