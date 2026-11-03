// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"errors"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// brokerNotWritten are the answers a broker gives a produce request that say
// its records were not written, among those this client does not retry:
// sarama v1.27.1 returns them on the first answer (async_producer.go, the
// "other non-retriable errors" branch), so the answer is the only attempt's.
// The names and codes are the Kafka protocol's error codes. An answer this
// table does not list is not read as a refusal: REQUEST_TIMED_OUT (appended
// on the leader, replication unconfirmed), NOT_ENOUGH_REPLICAS_AFTER_APPEND
// (written, under-replicated), NETWORK_EXCEPTION, and every answer the
// client retries - NOT_LEADER_FOR_PARTITION, LEADER_NOT_AVAILABLE,
// NOT_ENOUGH_REPLICAS, UNKNOWN_TOPIC_OR_PARTITION, CORRUPT_MESSAGE - where an
// earlier attempt may have landed. MESSAGE_TOO_LARGE never reaches this
// reading: it is a refusal for good, through clientRejection.
var brokerNotWritten = map[sarama.KError]bool{
	sarama.ErrInvalidTopic:                true, // INVALID_TOPIC_EXCEPTION (17)
	sarama.ErrMessageSetSizeTooLarge:      true, // RECORD_LIST_TOO_LARGE (18)
	sarama.ErrInvalidRequiredAcks:         true, // INVALID_REQUIRED_ACKS (21)
	sarama.ErrTopicAuthorizationFailed:    true, // TOPIC_AUTHORIZATION_FAILED (29)
	sarama.ErrClusterAuthorizationFailed:  true, // CLUSTER_AUTHORIZATION_FAILED (31)
	sarama.ErrInvalidTimestamp:            true, // INVALID_TIMESTAMP (32)
	sarama.ErrUnsupportedVersion:          true, // UNSUPPORTED_VERSION (35)
	sarama.ErrUnsupportedForMessageFormat: true, // UNSUPPORTED_FOR_MESSAGE_FORMAT (43)
}

// brokerFailureKind reads a failed send: broker_refused when every record
// that failed was answered with a brokerNotWritten error, ack_unknown
// otherwise - a record without an answer, or with an answer that the write
// may have been taken, makes whether the batch landed unknown. A message
// the client retried after a transport failure and then saw refused may
// still have landed on the first attempt; the completion reason is the
// same OUTPUT_ACK_UNKNOWN either way, and the kind names the last answer.
func brokerFailureKind(err error) string {
	var failures sarama.ProducerErrors
	if errors.As(err, &failures) && len(failures) > 0 {
		for _, failure := range failures {
			if failure == nil || !answeredNotWritten(failure.Err) {
				return observability.OutputFailureAckUnknown
			}
		}
		return observability.OutputFailureBrokerRefused
	}
	if answeredNotWritten(err) {
		return observability.OutputFailureBrokerRefused
	}
	return observability.OutputFailureAckUnknown
}

func answeredNotWritten(err error) bool {
	var answer sarama.KError
	return errors.As(err, &answer) && brokerNotWritten[answer]
}

// outputKindError is a sink error that is no dependency failure and no
// refusal but still has a kind to say: a sink already closed, or a send the
// Slot's context ended during. It marks nothing else, so the coordinator's
// reading of it is unchanged.
type outputKindError struct {
	kind string
	err  error
}

func (err *outputKindError) Error() string { return err.err.Error() }

func (err *outputKindError) Unwrap() error { return err.err }

func (err *outputKindError) OutputFailureKind() string { return err.kind }
