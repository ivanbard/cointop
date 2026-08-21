package marketdatahttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cointop-sh/cointop/pkg/api/types"
	"github.com/cointop-sh/cointop/pkg/marketdata"
)

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
func (handlerProvider) CoinLink(string) string                                { return "" }
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
	}{{"/v1/health", 200, "data"}, {"/unknown", 404, "error"}} {
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
