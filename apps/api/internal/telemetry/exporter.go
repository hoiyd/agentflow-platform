// Package telemetry projects committed execution boundaries into lossy traces.
// It never supplies execution state, resumes Runs, or exports event body text.
package telemetry

import (
	"context"
	"errors"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Config deliberately supports only OTLP/HTTP traces, without content or header
// passthrough. Other SDK environment settings cannot expand this privacy surface.
type Config struct {
	Exporter, Endpoint, ServiceName, SampleRatio string
}

func New(cfg Config) (*Observer, error) {
	if cfg.Exporter == "" || cfg.Exporter == "none" {
		return nil, nil
	}
	if cfg.Exporter != "otlp" {
		return nil, errors.New("OTEL_TRACES_EXPORTER must be none or otlp")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "http://127.0.0.1:4318"
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, errors.New("OTEL_EXPORTER_OTLP_ENDPOINT must be an HTTP(S) origin without credentials, path, query or fragment")
	}
	if endpoint.Scheme == "http" && endpoint.Hostname() != "localhost" && !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		return nil, errors.New("OTLP requires HTTPS except on loopback")
	}
	if port := endpoint.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, errors.New("OTLP endpoint port must be between 1 and 65535")
		}
	}
	if cfg.SampleRatio == "" {
		cfg.SampleRatio = "1"
	}
	ratio, err := strconv.ParseFloat(cfg.SampleRatio, 64)
	if err != nil || math.IsNaN(ratio) || ratio < 0 || ratio > 1 {
		return nil, errors.New("OTEL_TRACES_SAMPLER_ARG must be a ratio between 0 and 1")
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "agentflow-api"
	}
	if !identifier(cfg.ServiceName) {
		return nil, errors.New("OTEL_SERVICE_NAME must be a short identifier")
	}
	exporter, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL(strings.TrimSuffix(cfg.Endpoint, "/")+"/v1/traces"),
		otlptracehttp.WithHeaders(map[string]string{}),
		otlptracehttp.WithTimeout(2*time.Second),
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}),
	)
	if err != nil {
		return nil, errors.New("initialize OTLP HTTP exporter")
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", cfg.ServiceName))),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
		sdktrace.WithBatcher(safeExporter{exporter}, sdktrace.WithMaxQueueSize(1024), sdktrace.WithMaxExportBatchSize(128), sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(2*time.Second)),
		sdktrace.WithSpanLimits(sdktrace.SpanLimits{AttributeCountLimit: 64, AttributeValueLengthLimit: 128, EventCountLimit: 0, LinkCountLimit: 0}),
	)
	return newObserver(provider, defaultLimits()), nil
}

// SDK error handlers must not log endpoint credentials or Collector response bodies.
type safeExporter struct{ sdktrace.SpanExporter }

func (e safeExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if err := e.SpanExporter.ExportSpans(ctx, spans); err != nil {
		return errors.New("OTLP trace export unavailable; execution is unaffected")
	}
	return nil
}
func (e safeExporter) Shutdown(ctx context.Context) error {
	if err := e.SpanExporter.Shutdown(ctx); err != nil {
		return errors.New("OTLP trace exporter shutdown incomplete")
	}
	return nil
}
