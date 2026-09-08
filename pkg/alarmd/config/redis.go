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
// The pool therefore follows the permits it has to serve rather than the CPU
// quota directly. Permits are already the resource-derived quantity, so keying
// the pool to them keeps one derivation instead of two, and it means the pool
// grows only when the demand on it grows. Zero means "derive"; an explicit
// positive pool_size still wins so an operator can pin it.
const (
	redisPoolMinimum = 16
	redisPoolSpare   = 8
)

// DeriveRedisPoolSize sizes the pool to cover the admitted query concurrency
// plus room for the scheduler probes that run alongside those queries, never
// dropping below the minimum a small deployment needs.
func DeriveRedisPoolSize(admittedConcurrency int) int {
	size := redisPoolMinimum
	if covered := admittedConcurrency + redisPoolSpare; covered > size {
		size = covered
	}
	return size
}

// EffectivePoolSize resolves the value actually handed to the client.
func (c RedisConnectionConfig) EffectivePoolSize(admittedConcurrency int) int {
	if c.PoolSize > 0 {
		return c.PoolSize
	}
	return DeriveRedisPoolSize(admittedConcurrency)
}

func (c RedisConnectionConfig) clone() RedisConnectionConfig {
	c.SentinelAddress = append([]string(nil), c.SentinelAddress...)
	return c
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
