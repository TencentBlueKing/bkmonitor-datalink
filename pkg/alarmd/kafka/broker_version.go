// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package kafka

import (
	"fmt"

	"github.com/Shopify/sarama"
)

// The Kafka protocol version is a property of the cluster the process is
// pointed at, not of the process: the producer discovers it when it opens
// (NegotiateProtocol) and speaks the newest version every broker accepts,
// between two bounds the program knows.
//
// MinimumBrokerVersion is the floor: the oldest protocol the program can
// work against at all, set by consumer groups, which the input side needs.
// It is also the version the producer falls back to when the brokers accept
// nothing newer, and the version its configuration is validated against.
//
// RecordHeaderBrokerVersion is the ceiling the program has a use for: record
// headers, which the standard RawEvent carries the tenant in, exist from
// 0.11.0.0. A cluster that accepts Produce v3 or later takes headers; one
// that does not gets the floor, and a standard RawEvent on it is refused by
// name before any broker is asked (OUTPUT_CLIENT_REJECTED), while the
// Python-compatible output, which carries no header, is sent as before.
//
// This replaced a fixed version twice. Built for 0.10.2.0, the client
// refused every record with a header before the network and a deployment's
// standard output stopped; built for 0.11.0.0, a cluster whose brokers
// accept Produce up to v2 closed every connection on the record batch
// format and a deployment's whole output stopped. Neither is a fact the
// program can know without asking.
const (
	MinimumBrokerVersion      = "0.10.2.0"
	RecordHeaderBrokerVersion = "0.11.0.0"
)

// The parsed forms are derived from the strings, not written a second time:
// a mutant that moved a string alone kept deciding by the old parsed value
// while its error text named the new one.
var (
	minimumBrokerVersion      = mustParseKafkaVersion(MinimumBrokerVersion)
	recordHeaderBrokerVersion = mustParseKafkaVersion(RecordHeaderBrokerVersion)
)

func mustParseKafkaVersion(value string) sarama.KafkaVersion {
	version, err := sarama.ParseKafkaVersion(value)
	if err != nil {
		panic("kafka: broker version constant is not a Kafka version: " + err.Error())
	}
	return version
}

// ValidateBrokerVersion parses a broker version and checks it against the
// range the program supports, naming the feature that sets the floor when
// it is below it. scope prefixes the error the way the caller's other
// errors are prefixed.
func ValidateBrokerVersion(scope, value string) (sarama.KafkaVersion, error) {
	version, err := sarama.ParseKafkaVersion(value)
	if err != nil {
		return sarama.KafkaVersion{}, fmt.Errorf("%s: broker_version %q: %w", scope, value, err)
	}
	if !version.IsAtLeast(minimumBrokerVersion) {
		return sarama.KafkaVersion{}, fmt.Errorf(
			"%s: broker_version %q is below %s, the oldest protocol with consumer groups, which the input needs",
			scope, value, MinimumBrokerVersion,
		)
	}
	if !sarama.MaxVersion.IsAtLeast(version) {
		return sarama.KafkaVersion{}, fmt.Errorf(
			"%s: broker_version %q is above %s, the newest protocol this client speaks", scope, value, sarama.MaxVersion,
		)
	}
	return version, nil
}
