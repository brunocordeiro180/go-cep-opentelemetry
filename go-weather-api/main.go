package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var weatherAPIKey = "43a8de906a5a4e4ab67165701253105"

var zipkinURL = "http://zipkin:9411/api/v2/spans"

type ViaCEPResponse struct {
	Localidade string `json:"localidade"`
	Erro       bool   `json:"erro,omitempty"`
}

type WeatherAPIResponse struct {
	Current struct {
		TempC float64 `json:"temp_c"`
	} `json:"current"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type WeatherResponse struct {
	City  string  `json:"city"`
	TempC float64 `json:"temp_C"`
	TempF float64 `json:"temp_F"`
	TempK float64 `json:"temp_K"`
}

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
	tp := sdktrace.NewTracerProvider( // Defined tp here
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	log.Printf("Tracer initialized for service 	'%s'	, exporting to %s\n", serviceName, zipkinEndpoint)
	return tp.Shutdown, nil
}

func isValidCEP(cep string) bool {
	re := regexp.MustCompile(`[^0-9]`)
	cleanedCEP := re.ReplaceAllString(cep, "")
	match, _ := regexp.MatchString(`^\d{8}$`, cleanedCEP)
	return match
}

func getLocationFromCEP(ctx context.Context, cep string) (string, error) {
	tracer := otel.Tracer("service-b/viacep-client")
	ctx, span := tracer.Start(ctx, "call-viacep-api", trace.WithAttributes(
		attribute.String("cep.input", cep),
	))
	defer span.End()

	re := regexp.MustCompile(`[^0-9]`)
	cleanedCEP := re.ReplaceAllString(cep, "")
	apiURL := fmt.Sprintf("http://viacep.com.br/ws/%s/json/", cleanedCEP)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create viacep request")
		return "", fmt.Errorf("error creating ViaCEP request: %w", err)
	}

	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	resp, err := client.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to call viacep api")
		return "", fmt.Errorf("error fetching CEP data: %w", err)
	}
	defer resp.Body.Close()

	span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))

	var viaCEPResp ViaCEPResponse
	if err := json.NewDecoder(resp.Body).Decode(&viaCEPResp); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to decode viacep response")
		return "", fmt.Errorf("invalid zipcode")
	}

	if viaCEPResp.Erro {
		span.SetAttributes(attribute.Bool("viacep.error", true))
		span.SetStatus(codes.Error, "viacep returned error flag")
		return "", fmt.Errorf("can not find zipcode")
	}

	if viaCEPResp.Localidade == "" {
		span.SetStatus(codes.Error, "viacep returned empty location")
		return "", fmt.Errorf("can not find zipcode")
	}

	span.SetAttributes(attribute.String("viacep.location", viaCEPResp.Localidade))
	span.SetStatus(codes.Ok, "location found")
	return viaCEPResp.Localidade, nil
}

func getTemperature(ctx context.Context, location string) (float64, error) {

	tracer := otel.Tracer("service-b/weatherapi-client")
	ctx, span := tracer.Start(ctx, "call-weather-api", trace.WithAttributes(
		attribute.String("weather.location.input", location),
	))
	defer span.End()

	queryParam := url.QueryEscape(location)
	apiURL := fmt.Sprintf("http://api.weatherapi.com/v1/current.json?key=%s&q=%s&aqi=no", weatherAPIKey, queryParam)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create weatherapi request")
		return 0, fmt.Errorf("error creating WeatherAPI request: %w", err)
	}

	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	resp, err := client.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to call weatherapi")
		return 0, fmt.Errorf("error fetching weather data: %w", err)
	}
	defer resp.Body.Close()

	span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))

	var weatherResp WeatherAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&weatherResp); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to decode weatherapi response")
		return 0, fmt.Errorf("error decoding weather API response: %w", err)
	}

	if weatherResp.Error != nil {
		span.SetAttributes(
			attribute.Bool("weatherapi.error", true),
			attribute.Int("weatherapi.error.code", weatherResp.Error.Code),
			attribute.String("weatherapi.error.message", weatherResp.Error.Message),
		)
		if weatherResp.Error.Code == 1006 {
			span.SetStatus(codes.Error, "weatherapi location not found")
			return 0, fmt.Errorf("can not find zipcode")
		}
		span.SetStatus(codes.Error, "weatherapi returned error")
		return 0, fmt.Errorf("WeatherAPI error (%d): %s", weatherResp.Error.Code, weatherResp.Error.Message)
	}

	span.SetAttributes(attribute.Float64("weather.temp_c", weatherResp.Current.TempC))
	span.SetStatus(codes.Ok, "temperature found")
	return weatherResp.Current.TempC, nil
}

func celsiusToFahrenheit(celsius float64) float64 {
	return celsius*1.8 + 32
}

func celsiusToKelvin(celsius float64) float64 {
	return celsius + 273
}

func weatherHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cep := strings.TrimPrefix(r.URL.Path, "/weather/")

	if !isValidCEP(cep) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprintln(w, "invalid zipcode")
		return
	}

	location, err := getLocationFromCEP(ctx, cep)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err.Error() == "can not find zipcode" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintln(w, "can not find zipcode")
		} else if err.Error() == "invalid zipcode" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprintln(w, "invalid zipcode")
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "Internal server error getting location: %v", err)
		}
		return
	}

	tempC, err := getTemperature(ctx, location)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err.Error() == "can not find zipcode" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintln(w, "can not find zipcode")
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "Internal server error getting weather: %v", err)
		}
		return
	}

	tempF := celsiusToFahrenheit(tempC)
	tempK := celsiusToKelvin(tempC)

	response := WeatherResponse{
		City:  location,
		TempC: tempC,
		TempF: tempF,
		TempK: tempK,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding JSON response: %v\n", err)
	}
}

func main() {

	if key := os.Getenv("WEATHER_API_KEY"); key != "" {
		weatherAPIKey = key
	}
	if url := os.Getenv("OTEL_EXPORTER_ZIPKIN_ENDPOINT"); url != "" {
		zipkinURL = url
	}

	shutdown, err := initTracer("service-b", zipkinURL)
	if err != nil {
		log.Fatalf("Failed to initialize tracer: %v", err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			log.Printf("Failed to shutdown tracer: %v", err)
		}
	}()

	fmt.Println("Starting CEP Weather API server (Service B)...")

	httpHandler := otelhttp.NewHandler(http.HandlerFunc(weatherHandler), "ServiceB-HTTP-Request")
	http.Handle("/weather/", httpHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	fmt.Printf("Service B listening on port %s, exporting traces to %s\n", port, zipkinURL)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Error starting Service B: %s\n", err)
	}
}
