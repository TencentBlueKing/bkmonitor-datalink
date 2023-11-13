// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package core

import (
	"fmt"
)

const (
	// SpanMaxSize Maximum of analyses
	SpanMaxSize = 10000
	// HashSecret secret of hash
	HashSecret = "NMXhKSoWSa1APz0T68etCgHnmJQiim1B"
)

// SpanCategory Classification of span
type SpanCategory string

const (
	// CategoryHttp category: Http
	CategoryHttp SpanCategory = "http"
	// CategoryRpc category: Rpc
	CategoryRpc SpanCategory = "rpc"
	// CategoryDb category: Db
	CategoryDb SpanCategory = "db"
	// CategoryMessaging category: Message
	CategoryMessaging SpanCategory = "messaging"
	// CategoryAsyncBackend category: AsyncBackend
	CategoryAsyncBackend SpanCategory = "async_backend"
	// CategoryOther category: Other
	CategoryOther SpanCategory = "other"
)

// SpanStatusCode field from status.code
type SpanStatusCode int

const (
	// StatusCodeUnset status.code=0
	StatusCodeUnset SpanStatusCode = iota
	// StatusCodeOk status.code=1
	StatusCodeOk
	// StatusCodeError status.code=2
	StatusCodeError
)

// SpanKind field from kind
type SpanKind int

const (
	// KindUnspecified span kind: unspecified
	KindUnspecified SpanKind = 0
	// KindInterval span kind: interval
	KindInterval SpanKind = 1
	// KindServer span kind: server
	KindServer SpanKind = 2
	// KindClient span kind: client
	KindClient SpanKind = 3
	// KindProducer span kind: producer
	KindProducer SpanKind = 4
	// KindConsumer span kind: consumer
	KindConsumer SpanKind = 5
)

// SpanKindCategory kind to category mapping
type SpanKindCategory string

const (
	// KindCategoryUnspecified span kind: 0 -> unspecified
	KindCategoryUnspecified SpanKindCategory = "unspecified"
	// KindCategoryInterval span kind: 1 -> unspecified
	KindCategoryInterval SpanKindCategory = "interval"
	// KindCategorySync span kind: 2/3 -> sync
	KindCategorySync SpanKindCategory = "sync"
	// KindCategoryAsync span kind 4/5 -> async
	KindCategoryAsync SpanKindCategory = "async"
)

// ToKindCategory span.kind convert to category by SpanKindCategory
func (s SpanKind) ToKindCategory() SpanKindCategory {
	switch s {
	case KindUnspecified:
		return KindCategoryUnspecified
	case KindInterval:
		return KindCategoryInterval
	case KindServer:
		return KindCategorySync
	case KindClient:
		return KindCategorySync
	case KindProducer:
		return KindCategoryAsync
	case KindConsumer:
		return KindCategoryAsync
	default:
		return ""
	}
}

// IsCalledKind determine whether span.kind is the called party or not
func (s SpanKind) IsCalledKind() bool {
	switch s {
	case KindServer:
		return true
	case KindConsumer:
		return true
	default:
		return false
	}
}

// CommonField span standard field enum.
type CommonField struct {
	Source  FiledSource
	Key     string
	FullKey string
}

// DisplayKey field name in span origin data
func (c *CommonField) DisplayKey() string {
	switch c.Source {
	case SourceAttributes:
		return fmt.Sprintf("attributes.%s", c.Key)
	case SourceResource:
		return fmt.Sprintf("resource.%s", c.Key)
	default:
		return c.Key
	}
}

// FiledSource source of filed
type FiledSource string

const (
	// SourceResource source: span.resource field
	SourceResource FiledSource = "resource"
	// SourceAttributes source: span.attributes field
	SourceAttributes FiledSource = "attributes"
	// SourceOuter source from outer(span_name, kind, etc..)
	SourceOuter FiledSource = "outer"
)

var (
	HttpHostField = CommonField{
		SourceAttributes, "http.host", fmt.Sprintf("%s.%s", SourceAttributes, "http.host"),
	}
	HttpUrlField = CommonField{
		SourceAttributes, "http.url", fmt.Sprintf("%s.%s", SourceAttributes, "http.url"),
	}
	NetPeerNameField = CommonField{
		SourceAttributes, "net.peer.name", fmt.Sprintf("%s.%s", SourceAttributes, "net.peer.name"),
	}
	PeerServiceField = CommonField{
		SourceAttributes, "peer.service", fmt.Sprintf("%s.%s", SourceAttributes, "peer.service"),
	}
	HttpSchemeField = CommonField{
		SourceAttributes, "http.scheme", fmt.Sprintf("%s.%s", SourceAttributes, "http.scheme"),
	}
	HttpFlavorField = CommonField{
		SourceAttributes, "http.flavor", fmt.Sprintf("%s.%s", SourceAttributes, "http.flavor"),
	}
	HttpMethodField = CommonField{
		SourceAttributes, "http.method",
		fmt.Sprintf("%s.%s", SourceAttributes, "http.method"),
	}
	HttpStatusCodeField = CommonField{
		SourceAttributes, "http.status_code",
		fmt.Sprintf("%s.%s", SourceAttributes, "http.status_code"),
	}

	RpcMethodField = CommonField{
		SourceAttributes, "rpc.method", fmt.Sprintf("%s.%s", SourceAttributes, "rpc.method"),
	}
	RpcServiceField = CommonField{
		SourceAttributes, "rpc.service", fmt.Sprintf("%s.%s", SourceAttributes, "rpc.service"),
	}
	RpcSystemField = CommonField{
		SourceAttributes, "rpc.system", fmt.Sprintf("%s.%s", SourceAttributes, "rpc.system"),
	}
	RpcGrpcStatusCode = CommonField{
		SourceAttributes, "rpc.grpc.status_code",
		fmt.Sprintf("%s.%s", SourceAttributes, "rpc.grpc.status_code"),
	}

	DbNameField = CommonField{
		SourceAttributes, "db.name", fmt.Sprintf("%s.%s", SourceAttributes, "db.name"),
	}
	DbOperationField = CommonField{
		SourceAttributes, "db.operation", fmt.Sprintf("%s.%s", SourceAttributes, "db.operation"),
	}
	DbSystemField = CommonField{
		SourceAttributes, "db.system", fmt.Sprintf("%s.%s", SourceAttributes, "db.system"),
	}
	DbStatementField = CommonField{
		SourceAttributes, "db.statement",
		fmt.Sprintf("%s.%s", SourceAttributes, "db.statement"),
	}
	DbTypeField = CommonField{
		SourceAttributes, "db.type", fmt.Sprintf("%s.%s", SourceAttributes, "db.type"),
	}
	DbInstanceField = CommonField{
		SourceAttributes, "db.instance", fmt.Sprintf("%s.%s", SourceAttributes, "db.instance"),
	}

	MessagingRabbitmqRoutingKeyField = CommonField{
		SourceAttributes, "messaging.rabbitmq.routing_key",
		fmt.Sprintf("%s.%s", SourceAttributes, "messaging.rabbitmq.routing_key"),
	}
	MessagingKafkaKeyField = CommonField{
		SourceAttributes, "messaging.kafka.message_key",
		fmt.Sprintf("%s.%s", SourceAttributes, "messaging.kafka.message_key"),
	}
	MessagingRocketmqKeyField = CommonField{
		SourceAttributes, "messaging.rocketmq.message_keys",
		fmt.Sprintf("%s.%s", SourceAttributes, "messaging.rocketmq.message_keys"),
	}

	MessagingSystemField = CommonField{
		SourceAttributes, "messaging.system",
		fmt.Sprintf("%s.%s", SourceAttributes, "messaging.system"),
	}
	MessagingDestinationField = CommonField{
		SourceAttributes, "messaging.destination",
		fmt.Sprintf("%s.%s", SourceAttributes, "messaging.destination"),
	}
	MessagingDestinationKindField = CommonField{
		SourceAttributes, "messaging.destination_kind",
		fmt.Sprintf("%s.%s", SourceAttributes, "messaging.destination_kind"),
	}
	CeleryActionField = CommonField{
		SourceAttributes, "celery.action", fmt.Sprintf("%s.%s", SourceAttributes, "celery.action"),
	}
	CeleryTaskNameField = CommonField{
		SourceAttributes, "celery.task_name",
		fmt.Sprintf("%s.%s", SourceAttributes, "celery.task_name"),
	}

	ServiceNameField = CommonField{
		SourceResource, "service.name", fmt.Sprintf("%s.%s", SourceResource, "service.name"),
	}
	ServiceVersionField = CommonField{
		SourceResource, "service.version",
		fmt.Sprintf("%s.%s", SourceResource, "service.version"),
	}
	TelemetrySdkLanguageField = CommonField{
		SourceResource, "telemetry.sdk.language",
		fmt.Sprintf("%s.%s", SourceResource, "telemetry.sdk.language"),
	}
	TelemetrySdkNameField = CommonField{
		SourceResource, "telemetry.sdk.name",
		fmt.Sprintf("%s.%s", SourceResource, "telemetry.sdk.name"),
	}
	TelemetrySdkVersionField = CommonField{
		SourceResource, "telemetry.sdk.version",
		fmt.Sprintf("%s.%s", SourceResource, "telemetry.sdk.version"),
	}
	ServiceNamespaceField = CommonField{
		SourceResource, "service.namespace", fmt.Sprintf("%s.%s", SourceResource, "service.namespace"),
	}
	ServiceInstanceIdField = CommonField{
		SourceResource, "service.instance.id",
		fmt.Sprintf("%s.%s", SourceResource, "service.instance.id"),
	}
	NetHostIpField = CommonField{
		SourceResource, "net.host.ip", fmt.Sprintf("%s.%s", SourceResource, "net.host.ip"),
	}
	NetHostPortField = CommonField{
		SourceResource, "net.host.port", fmt.Sprintf("%s.%s", SourceResource, "net.host.port"),
	}
	NetHostnameField = CommonField{
		SourceResource, "net.host.name", fmt.Sprintf("%s.%s", SourceResource, "net.host.name"),
	}
	BkInstanceIdField = CommonField{
		SourceResource, "bk.instance.id",
		fmt.Sprintf("%s.%s", SourceResource, "bk.instance.id"),
	}
	KindField     = CommonField{SourceOuter, "kind", "kind"}
	SpanNameField = CommonField{SourceOuter, "span_name", "span_name"}
)

var StandardFields = []CommonField{
	HttpSchemeField,
	HttpFlavorField,
	HttpMethodField,
	HttpStatusCodeField,

	RpcMethodField,
	RpcServiceField,
	RpcSystemField,
	RpcGrpcStatusCode,

	DbNameField,
	DbOperationField,
	DbSystemField,

	MessagingSystemField,
	MessagingDestinationField,
	MessagingDestinationKindField,
	CeleryActionField,
	CeleryTaskNameField,

	ServiceNameField,
	ServiceVersionField,
	TelemetrySdkLanguageField,
	TelemetrySdkNameField,
	TelemetrySdkVersionField,
	ServiceNamespaceField,
	ServiceInstanceIdField,
	NetHostIpField,
	NetHostPortField,
	NetHostnameField,
	BkInstanceIdField,
	KindField,
	SpanNameField,
}

type CategoryPredicate struct {
	AnyFields    []CommonField
	OptionFields []CommonField
}

var CategoryPredicateFieldMapping = map[SpanCategory]CategoryPredicate{
	CategoryHttp: {
		AnyFields: []CommonField{
			HttpHostField,
			HttpUrlField,
			NetPeerNameField,
			PeerServiceField,
			HttpSchemeField,
			HttpFlavorField,
			HttpMethodField,
			HttpStatusCodeField,
		},
	},
	CategoryRpc: {
		AnyFields: []CommonField{
			RpcMethodField,
			RpcServiceField,
			RpcSystemField,
			RpcGrpcStatusCode,
		},
	},
	CategoryDb: {
		AnyFields: []CommonField{
			DbNameField,
			DbOperationField,
			DbSystemField,
			DbStatementField,
			DbTypeField,
			DbInstanceField,
		},
	},
	CategoryMessaging: {
		AnyFields: []CommonField{
			MessagingDestinationField,
			MessagingSystemField,
			MessagingDestinationKindField,
		},
		OptionFields: []CommonField{
			MessagingRabbitmqRoutingKeyField,
			MessagingKafkaKeyField,
			MessagingRocketmqKeyField,
		},
	},
	CategoryAsyncBackend: {
		AnyFields: []CommonField{
			MessagingDestinationField,
			MessagingDestinationKindField,
			MessagingSystemField,
			CeleryTaskNameField,
			CeleryActionField,
		},
	},
}
