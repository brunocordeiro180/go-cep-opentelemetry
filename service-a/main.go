package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type CEPRequest struct {
	CEP string `json:"cep"`
}

var serviceBURL = "http://localhost:8081"

var zipkinURL = "http://zipkin:9411/api/v2/spans"

func initTracer(serviceName, zipkinEndpoint string) (func(context.Context) error, error) {
	exporter, err := zipkin.New(
		zipkinEndpoint,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create zipkin exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	bsp := sdktrace.NewBatchSpanProcessor(exporter)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)

	otel.SetTracerProvider(tp)

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	log.Printf("Tracer initialized for service 	'%s'	, exporting to %s\n", serviceName, zipkinEndpoint)

	return tp.Shutdown, nil
}

func isValidCEPInput(cep string) bool {
	match, _ := regexp.MatchString(`^\d{8}$`, cep)
	return match
}

func handleCEPRequest(w http.ResponseWriter, r *http.Request) {
	tracer := otel.Tracer("service-a/handler")
	ctx := r.Context()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CEPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: Malformed JSON", http.StatusBadRequest)
		return
	}

	if !isValidCEPInput(req.CEP) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity) // 422
		fmt.Fprintln(w, "invalid zipcode")
		return
	}

	ctx, span := tracer.Start(ctx, "call-service-b")
	defer span.End()

	targetURL := fmt.Sprintf("%s/weather/%s", serviceBURL, req.CEP)

	serviceBReq, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create request to Service B")
		http.Error(w, fmt.Sprintf("Internal Server Error: Failed to create request to Service B: %v", err), http.StatusInternalServerError)
		return
	}

	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	serviceBResp, err := client.Do(serviceBReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reach Service B")
		http.Error(w, fmt.Sprintf("Internal Server Error: Failed to reach Service B: %v", err), http.StatusInternalServerError)
		return
	}
	defer serviceBResp.Body.Close()

	span.SetAttributes(semconv.HTTPResponseStatusCode(serviceBResp.StatusCode))

	for key, values := range serviceBResp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(serviceBResp.StatusCode)

	if _, err := io.Copy(w, serviceBResp.Body); err != nil {
		log.Printf("Error copying response body from Service B: %v\n", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to copy response body")
	}
}

func main() {

	if url := os.Getenv("SERVICE_B_URL"); url != "" {
		serviceBURL = url
	}
	if url := os.Getenv("OTEL_EXPORTER_ZIPKIN_ENDPOINT"); url != "" {
		zipkinURL = url
	}

	shutdown, err := initTracer("service-a", zipkinURL)
	if err != nil {
		log.Fatalf("Failed to initialize tracer: %v", err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			log.Printf("Failed to shutdown tracer: %v", err)
		}
	}()

	fmt.Println("Starting Service A...")

	httpHandler := otelhttp.NewHandler(http.HandlerFunc(handleCEPRequest), "ServiceA-HTTP-Request")
	http.Handle("/", httpHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Service A listening on port %s, forwarding to Service B at %s, exporting traces to %s\n", port, serviceBURL, zipkinURL)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Error starting Service A: %s\n", err)
	}
}
