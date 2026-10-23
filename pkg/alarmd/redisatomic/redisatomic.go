// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package redisatomic is how alarmd makes several writes to its own runtime
// store at once: one Lua script, sent as one EVALSHA, never a MULTI/EXEC
// transaction.
//
// The reason is the client, not Redis. go-redis (v8.11.5, redis.go:501-503,
// and v9 the same) reads a transaction's MULTI reply first and, when the
// server refuses it - NOAUTH, NOPERM, LOADING, BUSY - returns at once without
// reading the replies of the queued commands and of EXEC, and treats the
// connection as healthy because the error is a server reply. When those
// replies reach the socket after that read, the connection goes back to the
// pool looking clean, and the next command on it reads one of them as its
// own answer, with no error: a GET that returns +OK, a script that returns
// QUEUED. One EVALSHA is one command with one reply, so it cannot leave
// anything behind, and Redis runs a script without interleaving another
// client's commands, so it stays as atomic as the transaction was.
//
// A script here touches the same keys the transaction did. Some of those
// keys carry no common hash tag, which a standalone or Sentinel deployment
// allows and Redis Cluster refuses with CROSSSLOT - the same standing as the
// activation scripts (controlplane/activation_rebuild.go), and the reason
// the configuration refuses Cluster (config/redis.go, RedisConnectionConfig
// validate). A transaction on a Cluster client would not have been atomic
// across those keys either: go-redis splits it into one MULTI per slot.
//
// Reads of stores alarmd does not own are not scripts: a reader's account
// may not be allowed EVAL, and a proxy in front of a cluster refuses a
// script over keys of several slots. They are plain pipelines, which read
// every reply in order, a refused command included, and drop the connection
// on anything that is not a server reply (redis.go:452-461, error.go:68),
// so they cannot misalign either.
package redisatomic

import (
	"context"
	"errors"

	"github.com/go-redis/redis/v8"
)

// Write is one atomic multi-key write: a Lua script, defined once as a
// package variable beside the store that runs it, as the module's other
// scripts are (ownership's acquireScript, rollout's createScript).
type Write struct {
	script *redis.Script
}

// New defines a write. body is the Lua source. What it returns is not read:
// a write either ran or failed.
func New(body string) *Write {
	return &Write{script: redis.NewScript(body)}
}

// Run makes the write: one EVALSHA, and only if the server answers NOSCRIPT
// - it has not cached the body, after a restart, a failover or SCRIPT FLUSH -
// one EVAL with the body, which caches it for the next call. That is
// redis.Script.Run, kept here as the one path every write takes. Any other
// error is returned as the server or the connection gave it, for the caller
// to wrap in its own words and to count by redisfailure.Reason: a script the
// server refused is not retried, since a write retried on any other error
// could be applied twice.
//
// A script that returns nothing answers a nil reply, which go-redis reports
// as redis.Nil; for a write that is success, not an empty read.
func (write *Write) Run(ctx context.Context, client redis.Scripter, keys []string, args ...interface{}) error {
	err := write.script.Run(ctx, client, keys, args...).Err()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	return err
}
