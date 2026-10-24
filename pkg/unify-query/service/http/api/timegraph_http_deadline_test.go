package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTimeGraphYOLOHTTPWriteDeadlineIsRequestScoped(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "http2"}[http2], func(t *testing.T) {
			enabled := &atomic.Bool{}
			enabled.Store(true)
			router := gin.New()
			slow := func(c *gin.Context) { time.Sleep(120 * time.Millisecond); c.String(http.StatusOK, "complete") }
			router.POST("/timegraph", timeGraphHTTPDeadline(func() bool { return enabled.Load() }), slow)
			router.POST("/ordinary", slow)
			router.POST("/fast", func(c *gin.Context) { c.String(http.StatusOK, "complete") })
			server := httptest.NewUnstartedServer(router)
			server.Config.WriteTimeout = 40 * time.Millisecond
			server.EnableHTTP2 = http2
			if http2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			client := server.Client()
			client.Timeout = time.Second
			transport := client.Transport.(*http.Transport)
			// Keep fixture requests isolated from the package's HTTP mocks.
			if !http2 {
				transport = &http.Transport{}
				client.Transport = transport
			}
			defer transport.CloseIdleConnections()
			request := func(path string) (string, bool, error) {
				req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader("query"))
				require.NoError(t, err)
				reused := false
				req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}))
				response, err := client.Do(req)
				if err != nil {
					return "", reused, err
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				return string(body), reused, err
			}
			body, _, err := request("/timegraph")
			require.NoError(t, err)
			require.Equal(t, "complete", body)
			body, reused, err := request("/fast")
			require.NoError(t, err)
			require.Equal(t, "complete", body)
			require.True(t, reused)
			_, _, err = request("/ordinary")
			require.Error(t, err, "ordinary request must retain its server write deadline")
			enabled.Store(false)
			_, _, err = request("/timegraph")
			require.Error(t, err, "normal-mode TimeGraph must retain its server write deadline")
		})
	}
}

func TestTimeGraphYOLOHTTPAllowsSlowBodyAfterRouting(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		router := gin.New()
		router.POST("/timegraph", timeGraphHTTPDeadline(func() bool { return enabled }), func(c *gin.Context) {
			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.String(http.StatusBadRequest, "read timed out")
				return
			}
			c.String(http.StatusOK, string(body))
		})
		server := httptest.NewUnstartedServer(router)
		server.Config.ReadTimeout = 40 * time.Millisecond
		server.Start()
		client := &http.Client{Transport: &http.Transport{}, Timeout: time.Second}
		reader, writer := io.Pipe()
		written := make(chan struct{})
		go func() {
			defer close(written)
			defer writer.Close()
			time.Sleep(120 * time.Millisecond)
			_, _ = io.WriteString(writer, "complete")
		}()
		response, err := client.Post(server.URL+"/timegraph", "application/json", reader)
		if enabled {
			require.NoError(t, err)
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			require.NoError(t, readErr)
			require.Equal(t, http.StatusOK, response.StatusCode)
			require.Equal(t, "complete", string(body))
		} else if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			require.Equal(t, http.StatusBadRequest, response.StatusCode)
		}
		reader.Close()
		writer.Close()
		<-written
		client.CloseIdleConnections()
		server.Close()
	}
}

func TestTimeGraphYOLOHTTPPreservesClientCancellation(t *testing.T) {
	router := gin.New()
	started, stopped := make(chan struct{}), make(chan struct{})
	router.POST("/timegraph", timeGraphHTTPDeadline(func() bool { return true }), func(c *gin.Context) {
		_, _ = io.Copy(io.Discard, c.Request.Body)
		close(started)
		<-c.Request.Context().Done()
		close(stopped)
	})
	server := httptest.NewServer(router)
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/timegraph", strings.NewReader("query"))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	<-started
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("server did not observe client cancellation")
	}
}

func TestTimeGraphYOLOHTTP2DoesNotClearOtherStreamDeadline(t *testing.T) {
	router := gin.New()
	ordinaryStarted := make(chan struct{})
	slow := func(c *gin.Context) { time.Sleep(120 * time.Millisecond); c.String(http.StatusOK, "complete") }
	router.POST("/timegraph", timeGraphHTTPDeadline(func() bool { return true }), slow)
	router.POST("/ordinary", func(c *gin.Context) { close(ordinaryStarted); slow(c) })
	server := httptest.NewUnstartedServer(router)
	server.EnableHTTP2 = true
	server.Config.WriteTimeout = 40 * time.Millisecond
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	defer client.CloseIdleConnections()
	ordinaryDone := make(chan error, 1)
	go func() {
		response, err := client.Post(server.URL+"/ordinary", "application/json", strings.NewReader("query"))
		if response != nil {
			_, readErr := io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if err == nil {
				err = readErr
			}
		}
		ordinaryDone <- err
	}()
	<-ordinaryStarted
	response, err := client.Post(server.URL+"/timegraph", "application/json", strings.NewReader("query"))
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, "complete", string(body))
	require.Error(t, <-ordinaryDone)
}
