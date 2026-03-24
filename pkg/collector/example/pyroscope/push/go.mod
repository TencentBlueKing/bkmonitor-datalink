module github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/example/pyroscope/push

go 1.23.0

require (
	connectrpc.com/connect v1.16.2
	github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector v0.0.0-00010101000000-000000000000
)

require (
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	golang.org/x/net v0.40.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.25.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250519155744-55703ea1f237 // indirect
	google.golang.org/grpc v1.72.2 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
)

replace github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector => ../../../
