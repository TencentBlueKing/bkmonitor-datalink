// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package kafka

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Shopify/sarama"
)

// produceAPIKey is the Kafka API key of the Produce request;
// produceVersionWithRecordBatches is the first Produce version that carries
// the record batch format and with it record headers (Kafka 0.11), and
// produceVersionBeforeRecordBatches the one the floor speaks.
const (
	produceAPIKey                     int16 = 0
	produceVersionWithRecordBatches   int16 = 3
	produceVersionBeforeRecordBatches int16 = 2
)

// ProtocolReasonUnsupportedByBroker is the one reason a negotiation names:
// a broker accepts no Produce version that carries record headers, so the
// producer speaks the floor and the standard RawEvent cannot be sent.
const ProtocolReasonUnsupportedByBroker = "PROTOCOL_UNSUPPORTED_BY_BROKER"

// BrokerProtocol is what one broker answered to ApiVersions about Produce.
// ID is the node id the cluster's metadata gave it. A broker that did not
// answer has Answered false and Error set, and its versions are meaningless.
type BrokerProtocol struct {
	Address           string `json:"address"`
	ID                int32  `json:"id"`
	Answered          bool   `json:"answered"`
	ProduceMinVersion int16  `json:"produce_min_version"`
	ProduceMaxVersion int16  `json:"produce_max_version"`
	Error             string `json:"error,omitempty"`
}

// ProtocolNegotiation is the protocol the producer speaks and the answers
// it was decided from. It is a fact about the cluster at open time, kept
// for readers: the fleet page shows it, and a standard RawEvent refused for
// want of headers names it.
type ProtocolNegotiation struct {
	// Configured is the floor the producer is built with and falls back to
	// (MinimumBrokerVersion); Wanted the version the program has a use for
	// (RecordHeaderBrokerVersion); Negotiated what the producer speaks.
	Configured string `json:"configured"`
	Wanted     string `json:"wanted"`
	Negotiated string `json:"negotiated"`
	// ProduceVersion is the Produce request version the producer will send
	// under Negotiated; WantedProduceVersion the one record headers need.
	ProduceVersion       int16 `json:"produce_version"`
	WantedProduceVersion int16 `json:"wanted_produce_version"`
	// HeadersSupported is whether Negotiated carries record headers, that
	// is whether the standard RawEvent can be sent at all.
	HeadersSupported bool             `json:"headers_supported"`
	Brokers          []BrokerProtocol `json:"brokers"`
	// Reason is empty when every broker accepts the wanted version, else
	// ProtocolReasonUnsupportedByBroker.
	Reason string `json:"reason,omitempty"`
	// MetadataFrom is the bootstrap address whose Metadata named the brokers
	// above; empty when none answered.
	MetadataFrom string `json:"metadata_from,omitempty"`

	version sarama.KafkaVersion
}

// Version is the protocol the producer is opened with.
func (negotiation ProtocolNegotiation) Version() sarama.KafkaVersion { return negotiation.version }

// String is the one line a log or a refusal carries.
func (negotiation ProtocolNegotiation) String() string {
	answers := make([]string, 0, len(negotiation.Brokers))
	for _, broker := range negotiation.Brokers {
		if !broker.Answered {
			answers = append(answers, fmt.Sprintf("node %d %s did not answer (%s)", broker.ID, broker.Address, broker.Error))
			continue
		}
		answers = append(answers, fmt.Sprintf("node %d %s produce v%d..v%d", broker.ID, broker.Address, broker.ProduceMinVersion, broker.ProduceMaxVersion))
	}
	return fmt.Sprintf("negotiated %s (configured %s, wanted %s, record headers %t; %s)",
		negotiation.Negotiated, negotiation.Configured, negotiation.Wanted, negotiation.HeadersSupported, strings.Join(answers, ", "))
}

// clusterBroker is one broker as the cluster's own metadata names it.
type clusterBroker struct {
	ID      int32
	Address string
}

// brokerClient is what the negotiation asks of the cluster: one bootstrap
// address for the broker list, then every listed broker for its API
// versions.
type brokerClient interface {
	Brokers(bootstrap string, config *sarama.Config) ([]clusterBroker, error)
	ApiVersions(addr string, config *sarama.Config) (*sarama.ApiVersionsResponse, error)
}

type saramaBrokerClient struct{}

func (saramaBrokerClient) open(addr string, config *sarama.Config) (*sarama.Broker, error) {
	broker := sarama.NewBroker(addr)
	if err := broker.Open(config); err != nil {
		return nil, err
	}
	if connected, err := broker.Connected(); err != nil || !connected {
		_ = broker.Close()
		if err == nil {
			err = errors.New("not connected")
		}
		return nil, err
	}
	return broker, nil
}

func (client saramaBrokerClient) Brokers(bootstrap string, config *sarama.Config) ([]clusterBroker, error) {
	broker, err := client.open(bootstrap, config)
	if err != nil {
		return nil, err
	}
	defer func() { _ = broker.Close() }()
	metadata, err := broker.GetMetadata(&sarama.MetadataRequest{})
	if err != nil {
		return nil, err
	}
	listed := make([]clusterBroker, 0, len(metadata.Brokers))
	for _, member := range metadata.Brokers {
		if member == nil || member.Addr() == "" {
			continue
		}
		listed = append(listed, clusterBroker{ID: member.ID(), Address: member.Addr()})
	}
	return listed, nil
}

func (client saramaBrokerClient) ApiVersions(addr string, config *sarama.Config) (*sarama.ApiVersionsResponse, error) {
	broker, err := client.open(addr, config)
	if err != nil {
		return nil, err
	}
	defer func() { _ = broker.Close() }()
	return broker.ApiVersions(&sarama.ApiVersionsRequest{})
}

// NegotiateProtocol asks the cluster which brokers it has and each of them
// which Produce versions it accepts, and decides the protocol the producer
// will speak: RecordHeaderBrokerVersion when all of them accept the record
// batch format (Produce v3 or later), otherwise the configured floor.
//
// The broker list comes from the cluster's own metadata, read through the
// first bootstrap address that answers, not from the bootstrap list: the
// configuration usually names one DNS name for a cluster of several nodes,
// and a cluster upgraded one broker at a time has its oldest broker on a
// node the bootstrap name may never resolve to, while the producer's
// partitions may lead on exactly that node. Every listed broker is asked,
// and the newest version they all accept is the only one a whole batch can
// rely on. Asking by the metadata also gives each answer its node id.
//
// A cluster whose metadata cannot be read from any bootstrap address, or a
// listed broker that cannot be asked, fails the negotiation: the answer is
// not known, and the producer is not opened on a guess. The partial answers
// are returned beside the error so a reader can say which broker did not
// answer. The lazy sink retries the open, so an unreachable broker at start
// is the same wait it always was.
func NegotiateProtocol(bootstrap []string, config *sarama.Config) (ProtocolNegotiation, error) {
	return negotiateProtocol(bootstrap, config, saramaBrokerClient{})
}

func negotiateProtocol(bootstrap []string, config *sarama.Config, client brokerClient) (ProtocolNegotiation, error) {
	if len(bootstrap) == 0 || config == nil {
		return ProtocolNegotiation{}, errors.New("kafka: protocol negotiation needs bootstrap brokers and a client configuration")
	}
	negotiation := ProtocolNegotiation{
		Configured: config.Version.String(), Wanted: RecordHeaderBrokerVersion, Negotiated: config.Version.String(),
		ProduceVersion: produceVersionBeforeRecordBatches, WantedProduceVersion: produceVersionWithRecordBatches,
		version: config.Version,
	}
	var listed []clusterBroker
	var metadataErr error
	for _, addr := range bootstrap {
		brokers, err := client.Brokers(addr, config)
		if err != nil {
			if metadataErr == nil {
				metadataErr = fmt.Errorf("kafka: protocol negotiation: bootstrap %s did not answer Metadata: %w", addr, err)
			}
			continue
		}
		if len(brokers) == 0 {
			if metadataErr == nil {
				metadataErr = fmt.Errorf("kafka: protocol negotiation: bootstrap %s lists no brokers", addr)
			}
			continue
		}
		listed, metadataErr = brokers, nil
		negotiation.MetadataFrom = addr
		break
	}
	if metadataErr != nil {
		return negotiation, metadataErr
	}
	negotiation.Brokers = make([]BrokerProtocol, 0, len(listed))
	var failed error
	headers := true
	for _, member := range listed {
		answer := BrokerProtocol{Address: member.Address, ID: member.ID, ProduceMinVersion: -1, ProduceMaxVersion: -1}
		response, err := client.ApiVersions(member.Address, config)
		if err == nil && (response == nil || response.Err != sarama.ErrNoError) {
			err = sarama.ErrUnknown
			if response != nil {
				err = response.Err
			}
		}
		if err != nil {
			answer.Error = err.Error()
			negotiation.Brokers = append(negotiation.Brokers, answer)
			if failed == nil {
				failed = fmt.Errorf("kafka: protocol negotiation: broker %d at %s did not answer ApiVersions: %w", member.ID, member.Address, err)
			}
			continue
		}
		answer.Answered = true
		for _, api := range response.ApiVersions {
			if api != nil && api.ApiKey == produceAPIKey {
				answer.ProduceMinVersion, answer.ProduceMaxVersion = api.MinVersion, api.MaxVersion
			}
		}
		if answer.ProduceMaxVersion < produceVersionWithRecordBatches {
			headers = false
		}
		negotiation.Brokers = append(negotiation.Brokers, answer)
	}
	if failed != nil {
		return negotiation, failed
	}
	if headers && recordHeaderBrokerVersion.IsAtLeast(config.Version) {
		negotiation.version = recordHeaderBrokerVersion
		negotiation.Negotiated = RecordHeaderBrokerVersion
		negotiation.ProduceVersion = produceVersionWithRecordBatches
	}
	negotiation.HeadersSupported = negotiation.version.IsAtLeast(recordHeaderBrokerVersion)
	if !negotiation.HeadersSupported {
		negotiation.Reason = ProtocolReasonUnsupportedByBroker
	}
	return negotiation, nil
}
