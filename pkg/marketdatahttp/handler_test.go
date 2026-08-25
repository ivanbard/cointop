package marketdatahttp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cointop-sh/cointop/pkg/api/types"
	"github.com/cointop-sh/cointop/pkg/marketdata"
)

type testPortfolio struct{}

func (testPortfolio) Load(context.Context, string) (marketdata.Result, error) {
	return marketdata.Result{Data: map[string]any{"holdings": []any{}}}, nil
}

type handlerProvider struct{}

func (handlerProvider) Ping() error { return nil }
func (handlerProvider) GetAllCoinData(_ string, ch chan []types.Coin) error {
	go func() {
		defer close(ch)
		ch <- []types.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Rank: 1, Price: 42}, {ID: "ethereum", Name: "Ethereum", Symbol: "ETH", Rank: 2, Price: 3}}
	}()
	return nil
}
func (handlerProvider) GetCoinGraphData(string, string, string, int64, int64) (types.CoinGraph, error) {
	return types.CoinGraph{}, nil
}
func (handlerProvider) GetGlobalMarketGraphData(string, int64, int64) (types.MarketGraph, error) {
	return types.MarketGraph{}, nil
}
func (handlerProvider) GetGlobalMarketData(string) (types.GlobalMarketData, error) {
	return types.GlobalMarketData{TotalMarketCapUSD: 100}, nil
}
func (handlerProvider) GetCoinData(name, _ string) (types.Coin, error) {
	if name == "missing" {
		return types.Coin{}, nil
	}
	return types.Coin{ID: name, Name: "Bitcoin", Symbol: "BTC", Price: 42}, nil
}
func (handlerProvider) GetCoinDataBatch([]string, string) ([]types.Coin, error) {
	return []types.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Price: 42}}, nil
}
func (handlerProvider) CoinLink(slug string) string                           { return "https://example.test/" + slug }
func (handlerProvider) SupportedCurrencies() []string                         { return []string{"USD", "BTC"} }
func (handlerProvider) Price(string, string) (float64, error)                 { return 42, nil }
func (handlerProvider) GetExchangeRate(string, string, bool) (float64, error) { return 1, nil }

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	service, err := marketdata.NewService(handlerProvider{}, marketdata.Config{Provider: "fake", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(service)
}

func TestHealthAndJSONNotFound(t *testing.T) {
	handler := newTestHandler(t)
	for _, tc := range []struct {
		path   string
		status int
		key    string
	}{{"/v1/health", 200, "data"}, {"/v1/ready", 200, "data"}, {"/unknown", 404, "error"}} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, tc.path, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != tc.status {
			t.Fatalf("%s: expected %d got %d", tc.path, tc.status, recorder.Code)
		}
		if recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("%s: expected JSON", tc.path)
		}
		var body map[string]interface{}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body[tc.key]; !ok {
			t.Fatalf("%s: missing %s", tc.path, tc.key)
		}
	}
}

type unavailableProvider struct{ handlerProvider }

func (unavailableProvider) Ping() error { return errors.New("offline") }

func TestReadyReturns503WithReadinessMetadata(t *testing.T) {
	service, err := marketdata.NewService(unavailableProvider{}, marketdata.Config{Provider: "primary", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	NewHandler(service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data marketdata.Health `json:"data"`
		Meta marketdata.Meta   `json:"meta"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Status != "not_ready" || body.Meta.PrimaryProvider != "primary" || body.Meta.CacheStatus != "miss" {
		t.Fatalf("body=%+v", body)
	}
}

func TestChartRoutesAndRangeValidation(t *testing.T) {
	handler := newTestHandler(t)
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/v1/charts/coins/bitcoin?currency=USD&range=7d", http.StatusOK},
		{"/v1/charts/global?currency=USD&range=ytd", http.StatusOK},
		{"/v1/charts/global?range=2y", http.StatusBadRequest},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if recorder.Code != tc.status {
			t.Fatalf("%s: expected %d got %d: %s", tc.path, tc.status, recorder.Code, recorder.Body.String())
		}
	}
}

func TestTUIProviderRoutes(t *testing.T) {
	handler := newTestHandler(t)
	for _, path := range []string{"/v1/exchange-rate?from=BTC&to=USD", "/v1/links/coins/bitcoin"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPortfolioRouteIsRuntimeGated(t *testing.T) {
	disabled := newTestHandler(t)
	recorder := httptest.NewRecorder()
	disabled.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/portfolio", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled status=%d", recorder.Code)
	}
	service, err := marketdata.NewService(handlerProvider{}, marketdata.Config{Provider: "fake", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	enabled := NewHandlerWithOptions(service, HandlerOptions{Portfolio: testPortfolio{}})
	recorder = httptest.NewRecorder()
	enabled.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/portfolio", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("enabled status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestPriceStreamValidationAndInitialEvent(t *testing.T) {
	handler := newTestHandler(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/stream/prices?coins=btc&interval=1s", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("validation status=%d", recorder.Code)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/stream/prices?coins=btc,eth&currency=USD&interval=15s")
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(response.Body)
	var lines []string
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
		lines = append(lines, scanner.Text())
	}
	_ = response.Body.Close()
	joined := strings.Join(lines, "\n")
	if response.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(joined, "event: prices") || !strings.Contains(joined, "\"cacheStatus\"") {
		t.Fatalf("unexpected stream: headers=%v lines=%s", response.Header, joined)
	}
}

func TestCoinsPaginationAndValidation(t *testing.T) {
	handler := newTestHandler(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/coins?limit=1&offset=1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		Data struct {
			Items []interface{} `json:"items"`
			Total int           `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Items) != 1 || result.Data.Total != 2 {
		t.Fatalf("unexpected pagination: %+v", result.Data)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/coins?limit=999", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", recorder.Code)
	}
}

func TestLoopbackValidation(t *testing.T) {
	for _, address := range []string{"127.0.0.1:7070", "[::1]:7070", "localhost:7070"} {
		if err := ValidateListenAddress(address); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:7070", "192.168.1.5:7070"} {
		if err := ValidateListenAddress(address); err == nil {
			t.Errorf("expected %s to fail", address)
		}
	}
	for _, endpoint := range []string{"http://127.0.0.1:7070/base", "http://user:pass@localhost:7070"} {
		if err := ValidateEndpoint(endpoint); err == nil {
			t.Errorf("expected %s to fail", endpoint)
		}
	}
}
