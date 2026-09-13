// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	RedisModeStandalone = "standalone"
	RedisModeSentinel   = "sentinel"
)

// RedisConnectionConfig follows the two Redis connection modes already used
// by datalink. Business state prefixes and TTLs deliberately stay outside the
// connection contract.
type RedisConnectionConfig struct {
	Mode             string   `yaml:"mode"`
	Address          string   `yaml:"address"`
	SentinelAddress  []string `yaml:"sentinel_address"`
	MasterName       string   `yaml:"master_name"`
	Username         string   `yaml:"username"`
	Password         string   `yaml:"password"`
	SentinelUsername string   `yaml:"sentinel_username"`
	SentinelPassword string   `yaml:"sentinel_password"`
	DB               int      `yaml:"db"`
	DialTimeout      Duration `yaml:"dial_timeout"`
	ReadTimeout      Duration `yaml:"read_timeout"`
	WriteTimeout     Duration `yaml:"write_timeout"`
	PoolSize         int      `yaml:"pool_size"`
}

// A fixed connection pool is the smallest concurrency gate this process has.
// Every scheduler probe and every admitted query round-trips through it, so a
// pool below the concurrency the scheduler is licensed to run converts command
// latency into queueing latency with no gate reporting that it did: the client
// still returns, only later.
//
// Keying the pool to the query permits was wrong, and a block profile from a
// running Worker says how wrong: goroutines spent 354 of 4,192 recorded hours
// parked in the pool's waitTurn, about thirty-two goroutines queued at any
// moment against a pool of forty-eight. The permits never covered that demand
// because the goroutines doing the queueing hold no permit. The heaviest
// caller is the Slot source, not the query: reading the activation header, the
// schedule timeline, the retirement boundary and the ownership fence all
// happen before a Slot is even eligible to query, and the scheduler runs those
// for every ready runner at once. With the active execution limit left
// unlimited on purpose, nothing else bounds that fan-out, so the pool became
// the process's real execution gate while reporting a pool that looked idle:
// waiting on the pool's semaphore increments neither the miss nor the timeout
// counter.
//
// So the pool follows the container's CPU budget, which is the one quantity
// known at startup that scales with how much work the Worker is expected to
// carry, and which automaxprocs already derives from the cgroup. Redis work is
// network-bound, so useful concurrency per CPU is far above one; the factor
// below leaves several times the measured steady-state demand. Zero means
// "derive"; an explicit positive pool_size still wins so an operator can pin
// it, and a deployment that wants the pool to stop being a gate can raise it
// without a release.
const (
	redisPoolMinimum       = 64
	redisPoolPerCPU        = 16
	redisPoolDerivedCeling = 512
)

// DeriveRedisPoolSize sizes the pool from the CPU budget the container was
// given, never below the floor a small deployment needs and never above a
// ceiling that keeps one Worker's connection count reasonable for a shared
// Redis.
func DeriveRedisPoolSize(cpuBudget int) int {
	size := redisPoolMinimum
	if covered := cpuBudget * redisPoolPerCPU; covered > size {
		size = covered
	}
	if size > redisPoolDerivedCeling {
		size = redisPoolDerivedCeling
	}
	// The pool must never become the process's execution gate, so the ceiling
	// yields to the query admission derived from the same CPU budget. Covering
	// the permits is only the floor: the Slot source reads the control plane
	// for every ready runner while holding no permit, and that fan-out is what
	// the block profile found queueing.
	if admitted := DeriveScheduler(CapacityInputs{CPUBudget: cpuBudget}).AdmittedConcurrency(); size <= admitted {
		size = admitted + redisPoolMinimum
	}
	return size
}

// EffectivePoolSize resolves the value actually handed to the client.
func (c RedisConnectionConfig) EffectivePoolSize(cpuBudget int) int {
	if c.PoolSize > 0 {
		return c.PoolSize
	}
	return DeriveRedisPoolSize(cpuBudget)
}

func (c RedisConnectionConfig) clone() RedisConnectionConfig {
	c.SentinelAddress = append([]string(nil), c.SentinelAddress...)
	return c
}

// Destination renders where this connection points, and nothing else. It feeds
// the startup evidence surface, which is credential-free by contract, so the
// password and the sentinel password are deliberately absent - a master name
// and a sentinel address are coordinates, not secrets.
func (c RedisConnectionConfig) Destination() string {
	if c.Mode == RedisModeSentinel {
		return fmt.Sprintf("sentinel %s [%s]/%d", c.MasterName, strings.Join(c.SentinelAddress, ","), c.DB)
	}
	return fmt.Sprintf("standalone %s/%d", c.Address, c.DB)
}

func (c RedisConnectionConfig) validate(field string) error {
	if c.Mode != RedisModeStandalone && c.Mode != RedisModeSentinel {
		return fmt.Errorf("%s mode must be standalone or sentinel", field)
	}
	if c.DB < 0 || c.DialTimeout.Duration() <= 0 || c.ReadTimeout.Duration() <= 0 ||
		c.WriteTimeout.Duration() <= 0 || c.PoolSize < 0 {
		return fmt.Errorf("%s db, timeouts and pool_size are invalid", field)
	}
	if c.Username != "" && strings.TrimSpace(c.Username) != c.Username {
		return fmt.Errorf("%s username must be canonical text", field)
	}
	if c.SentinelUsername != "" && strings.TrimSpace(c.SentinelUsername) != c.SentinelUsername {
		return fmt.Errorf("%s sentinel_username must be canonical text", field)
	}

	if c.Mode == RedisModeStandalone {
		if !canonicalRedisAddress(c.Address) {
			return fmt.Errorf("%s address must be a canonical host:port", field)
		}
		return nil
	}
	if !canonicalText(c.MasterName) || len(c.SentinelAddress) == 0 {
		return fmt.Errorf("%s sentinel_address and master_name are required", field)
	}
	seen := make(map[string]struct{}, len(c.SentinelAddress))
	for _, address := range c.SentinelAddress {
		if !canonicalRedisAddress(address) {
			return fmt.Errorf("%s sentinel_address must contain canonical host:port values", field)
		}
		if _, duplicate := seen[address]; duplicate {
			return fmt.Errorf("%s sentinel_address must not contain duplicates", field)
		}
		seen[address] = struct{}{}
	}
	return nil
}

func canonicalRedisAddress(address string) bool {
	if address == "" || strings.TrimSpace(address) != address {
		return false
	}
	host, port, err := net.SplitHostPort(address)
	return err == nil && host != "" && port != ""
}

func validateRuntimePrefixIsolation(statePrefix, strategyCachePrefix string) error {
	if !canonicalRedisPrefix(statePrefix) {
		return errors.New("redis state_prefix must be non-empty canonical text without Redis hash tags")
	}
	if !canonicalRedisPrefix(strategyCachePrefix) {
		return errors.New("phase_two control strategy_cache_prefix must be canonical Redis text")
	}
	if strings.HasPrefix(statePrefix, strategyCachePrefix) || strings.HasPrefix(strategyCachePrefix, statePrefix) {
		return errors.New("redis state_prefix must not overlap phase_two strategy_cache_prefix")
	}
	return nil
}

func canonicalRedisPrefix(prefix string) bool {
	return prefix != "" && strings.TrimSpace(prefix) == prefix && !strings.ContainsAny(prefix, "{} \t\r\n")
}
