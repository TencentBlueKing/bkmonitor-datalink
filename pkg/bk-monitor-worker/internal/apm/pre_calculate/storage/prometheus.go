package storage

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/prompb"
)

type PrometheusStorageData struct {
	Value []prompb.TimeSeries
}

type PrometheusWriterOption func(options *PrometheusWriterOptions)

type PrometheusWriterOptions struct {
	enabled bool
	url     string
	headers map[string]string
}

func PrometheusWriterEnabled(b bool) PrometheusWriterOption {
	return func(options *PrometheusWriterOptions) {
		options.enabled = b
	}
}

func PrometheusWriterUrl(u string) PrometheusWriterOption {
	return func(options *PrometheusWriterOptions) {
		options.url = u
	}
}

func PrometheusWriterHeaders(h map[string]string) PrometheusWriterOption {
	return func(options *PrometheusWriterOptions) {
		options.headers = h
	}
}

type prometheusWriter struct {
	config PrometheusWriterOptions

	client *http.Client
}

func (p *prometheusWriter) WriteBatch(data []PrometheusStorageData) error {
	var series []prompb.TimeSeries
	for _, item := range data {
		series = append(series, item.Value...)
	}

	reqBytes, err := proto.Marshal(&prompb.WriteRequest{Timeseries: series})
	if err != nil {
		return err
	}
	compressedData := snappy.Encode(nil, reqBytes)
	req, err := http.NewRequest("POST", p.config.url, bytes.NewBuffer(compressedData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	for k, v := range p.config.headers {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return errors.Errorf("[PromRemoteWrite] request failed: %s", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if resp.StatusCode >= 500 && resp.StatusCode < 600 {
		return fmt.Errorf("[PromRemoteWrite] remote write returned HTTP status %v; err = %w: %s", resp.Status, err, body)
	}

	return nil
}

func newPrometheusWriterClient(config PrometheusWriterOptions) *prometheusWriter {
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
		},
		Timeout: 10 * time.Second,
	}
	return &prometheusWriter{config: config, client: client}
}
