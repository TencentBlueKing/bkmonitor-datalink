package api

import (
	"context"
	stdjson "encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

// topologyResponseStream 逐项写出批次响应 {"trace_id":...,"data":[item,...]}：每项编码完立即
// 写出，调用方随即释放这些片段，批次只保留正在编码的一项。代理路径由 proxy.JSONStream 加统一外层。
type topologyResponseStream struct {
	stream  *proxy.JSONStream
	traceID string
	items   int
}

func newTopologyResponseStream(ctx context.Context, c *gin.Context, traceID string) *topologyResponseStream {
	return &topologyResponseStream{stream: proxy.NewJSONStream(ctx, c), traceID: traceID}
}

func (s *topologyResponseStream) prefix() ([]byte, error) {
	traceID, err := stdjson.Marshal(s.traceID)
	if err != nil {
		return nil, err
	}
	prefix := append([]byte(`{"trace_id":`), traceID...)
	return append(prefix, `,"data":[`...), nil
}

// item 写出一项的已编码片段，项之间以逗号分隔。
func (s *topologyResponseStream) item(parts [][]byte) error {
	lead := []byte(",")
	if s.items == 0 {
		var err error
		if lead, err = s.prefix(); err != nil {
			return err
		}
	}
	s.items++
	return s.stream.Write(append([][]byte{lead}, parts...)...)
}

// close 写出批次结尾；没有任何项时写出空批次。
func (s *topologyResponseStream) close() error {
	if s.items > 0 {
		return s.stream.Close([]byte("]}"))
	}
	prefix, err := s.prefix()
	if err != nil {
		return err
	}
	return s.stream.Close(prefix, []byte("]}"))
}

func (s *topologyResponseStream) started() bool { return s.stream.Started() }

// detach 让已开始的响应在请求取消后仍能写完有界的失败项。
func (s *topologyResponseStream) detach() { s.stream.Detach() }
