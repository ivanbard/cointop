package cmd

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cointop-sh/cointop/pkg/api/types"
	"github.com/cointop-sh/cointop/pkg/marketdata"
	"github.com/cointop-sh/cointop/pkg/marketdatahttp"
	"github.com/cointop-sh/cointop/pkg/marketdatamcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	log "github.com/sirupsen/logrus"
)

type apiTestProvider struct{}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func (apiTestProvider) Ping() error { return nil }
func (apiTestProvider) GetAllCoinData(_ string, ch chan []types.Coin) error {
	go func() {
		defer close(ch)
		ch <- []types.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Price: 42}}
	}()
	return nil
}
func (apiTestProvider) GetCoinGraphData(string, string, string, int64, int64) (types.CoinGraph, error) {
	return types.CoinGraph{}, nil
}
func (apiTestProvider) GetGlobalMarketGraphData(string, int64, int64) (types.MarketGraph, error) {
	return types.MarketGraph{}, nil
}
func (apiTestProvider) GetGlobalMarketData(string) (types.GlobalMarketData, error) {
	return types.GlobalMarketData{TotalMarketCapUSD: 100}, nil
}
func (apiTestProvider) GetCoinData(name, _ string) (types.Coin, error) {
	return types.Coin{ID: name, Name: "Bitcoin", Symbol: "BTC", Price: 42}, nil
}
func (apiTestProvider) GetCoinDataBatch([]string, string) ([]types.Coin, error) {
	return []types.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Price: 42}}, nil
}
func (apiTestProvider) CoinLink(slug string) string           { return "https://example.test/" + slug }
func (apiTestProvider) SupportedCurrencies() []string         { return []string{"USD", "BTC"} }
func (apiTestProvider) Price(string, string) (float64, error) { return 42, nil }
func (apiTestProvider) GetExchangeRate(string, string, bool) (float64, error) {
	return 1, nil
}

func newAPIHandlerForTest(t *testing.T) (http.Handler, *lockedBuffer) {
	t.Helper()
	service, err := marketdata.NewService(apiTestProvider{}, marketdata.Config{Provider: "fake", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var logs lockedBuffer
	logger := log.New()
	logger.SetOutput(&logs)
	return newAPIHandler(service, marketdatahttp.HandlerOptions{}, marketdatamcp.Options{}, logger), &logs
}

func TestAPIHandlerStreamsRESTThroughLogging(t *testing.T) {
	handler, logs := newAPIHandlerForTest(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/stream/prices?coins=btc&currency=USD&interval=15s", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	scanner := bufio.NewScanner(response.Body)
	var lines []string
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(joined, "event: prices") {
		t.Fatalf("unexpected stream: status=%d headers=%v lines=%s", response.StatusCode, response.Header, joined)
	}
	cancel()
	_ = response.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.String(), "path=/v1/stream/prices") || !strings.Contains(logs.String(), "status=200") {
		if time.Now().After(deadline) {
			t.Fatalf("SSE completion was not logged with status 200: %s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAPIHandlerServesMCPThroughLogging(t *testing.T) {
	handler, logs := newAPIHandlerForTest(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "api-composition-test", Version: "vtest"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_global_market", Arguments: map[string]any{"currency": "USD"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.StructuredContent == nil {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !strings.Contains(logs.String(), "market data API request") || !strings.Contains(logs.String(), "path=/mcp") {
		t.Fatalf("MCP request was not logged: %s", logs.String())
	}
}
