module github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/example/pyroscope/push

go 1.24.0

toolchain go1.24.4

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

replace (
	github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector => ../../../
	k8s.io/apiserver => k8s.io/apiserver v0.25.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.25.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.25.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.25.0
	k8s.io/code-generator => k8s.io/code-generator v0.25.0
	k8s.io/component-helpers => k8s.io/component-helpers v0.25.0
	k8s.io/controller-manager => k8s.io/controller-manager v0.25.0
	k8s.io/cri-api => k8s.io/cri-api v0.25.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.25.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.25.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.25.0
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.25.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.25.0
	k8s.io/kubectl => k8s.io/kubectl v0.25.0
	k8s.io/kubelet => k8s.io/kubelet v0.25.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.25.0
	k8s.io/metrics => k8s.io/metrics v0.25.0
	k8s.io/mount-utils => k8s.io/mount-utils v0.25.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.25.0
)
