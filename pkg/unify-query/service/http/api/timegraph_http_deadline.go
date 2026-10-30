package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// Register this only on TimeGraph routes, after the server identifies the request.
// Ordinary queries retain the HTTP server deadlines; parent cancellation remains.
func timeGraphHTTPDeadline(enabled func() bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enabled() {
			return
		}
		controller := http.NewResponseController(c.Writer)
		if err := controller.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			log.Warnf(c.Request.Context(), "clear TimeGraph YOLO HTTP read deadline: %s", err)
		}
		if err := controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			log.Warnf(c.Request.Context(), "clear TimeGraph YOLO HTTP write deadline: %s", err)
		}
		// Do not call c.Next: proxy dispatch also invokes registered handlers directly.
	}
}
