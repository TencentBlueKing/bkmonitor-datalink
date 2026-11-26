module github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/example/pyroscope/push

go 1.25.0

toolchain go1.25.14

require (
	connectrpc.com/connect v1.16.2
	github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector v0.0.0-00010101000000-000000000000
)

require (
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20250313105119-ba97887b0a25 // indirect
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250811160224-6b04f9b4fc78 // indirect
	google.golang.org/grpc v1.72.2 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector => ../../../
