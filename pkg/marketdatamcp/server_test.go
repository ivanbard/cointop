package marketdatamcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	apitypes "github.com/cointop-sh/cointop/pkg/api/types"
	"github.com/cointop-sh/cointop/pkg/marketdata"
	"github.com/cointop-sh/cointop/pkg/portfolio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeReader struct{ err error }
type fakePortfolio struct{}

func (fakePortfolio) Load(context.Context, string) (marketdata.Result, error) {
	return testResultPortfolio(), nil
}
func testResultPortfolio() marketdata.Result {
	now := time.Now().UTC()
	return marketdata.Result{Data: portfolio.Snapshot{Currency: "USD", Holdings: []portfolio.Holding{}}, Meta: marketdata.Meta{Provider: "fake", FetchedAt: now, ExpiresAt: now, CacheStatus: "hit"}}
}

func (f fakeReader) result(data any, currency string) (marketdata.Result, error) {
	if f.err != nil {
		return marketdata.Result{}, f.err
	}
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	return marketdata.Result{Data: data, Meta: marketdata.Meta{Provider: "fake", Currency: currency, FetchedAt: now, ExpiresAt: now.Add(time.Minute), CacheStatus: "hit"}}, nil
}

func TestStreamableHTTP(t *testing.T) {
	httpServer := httptest.NewServer(NewHTTPHandler(fakeReader{}, "vtest"))
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "vtest"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
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
}
func (f fakeReader) Prices(context.Context, []string, string) (marketdata.Result, error) {
	return f.result([]marketdata.Price{{ID: "bitcoin", Symbol: "BTC", Price: 42}}, "USD")
}
func (f fakeReader) Coins(context.Context, string) (marketdata.Result, error) {
	return f.result([]apitypes.Coin{{ID: "bitcoin", Symbol: "BTC", Price: 42}}, "USD")
}
func (f fakeReader) Coin(context.Context, string, string) (marketdata.Result, error) {
	return f.result(apitypes.Coin{ID: "bitcoin", Symbol: "BTC", Price: 42}, "USD")
}
func (f fakeReader) Global(context.Context, string) (marketdata.Result, error) {
	return f.result(apitypes.GlobalMarketData{TotalMarketCapUSD: 100}, "USD")
}
func (f fakeReader) Currencies(context.Context) (marketdata.Result, error) {
	return f.result([]string{"BTC", "USD"}, "")
}
func (f fakeReader) CoinHistory(context.Context, string, string, string) (marketdata.Result, error) {
	return f.result(marketdata.CoinHistory{ID: "bitcoin", Range: "24h", Series: apitypes.CoinGraph{Price: [][]float64{{1, 42}}}}, "USD")
}
func (f fakeReader) GlobalHistory(context.Context, string, string) (marketdata.Result, error) {
	return f.result(marketdata.GlobalHistory{Range: "24h", Series: apitypes.MarketGraph{MarketCapByAvailableSupply: [][]float64{{1, 100}}}}, "USD")
}

func connectTestClient(t *testing.T, reader Reader) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := NewServer(reader, "vtest").Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "vtest"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close(); _ = serverSession.Close() })
	return clientSession
}

func TestDiscoveryAndStructuredToolResult(t *testing.T) {
	session := connectTestClient(t, fakeReader{})
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 7 {
		t.Fatalf("got %d tools", len(tools.Tools))
	}
	for _, item := range tools.Tools {
		if item.Annotations == nil || !item.Annotations.ReadOnlyHint || !item.Annotations.IdempotentHint || item.Annotations.DestructiveHint == nil || *item.Annotations.DestructiveHint {
			t.Fatalf("unsafe annotations for %s", item.Name)
		}
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_prices", Arguments: map[string]any{"coins": []string{"btc"}, "currency": "USD"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.StructuredContent == nil || len(result.Content) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !json.Valid([]byte(text.Text)) {
		t.Fatalf("missing JSON text compatibility: %#v", result.Content)
	}
}

func TestResourcesAndSafeErrors(t *testing.T) {
	session := connectTestClient(t, fakeReader{})
	resources, err := session.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources.Resources) != 1 || resources.Resources[0].URI != "cointop://currencies" {
		t.Fatalf("unexpected resources: %#v", resources.Resources)
	}
	read, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "cointop://prices/btc,eth?currency=USD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Contents) != 1 || !json.Valid([]byte(read.Contents[0].Text)) {
		t.Fatalf("invalid resource: %#v", read)
	}

	failing := connectTestClient(t, fakeReader{err: errors.Join(marketdata.ErrUnavailable, errors.New("secret upstream detail"))})
	result, err := failing.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_currencies", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	if result.Content[0].(*mcp.TextContent).Text != "market data provider unavailable" {
		t.Fatalf("unsafe error: %q", result.Content[0].(*mcp.TextContent).Text)
	}
}

func TestPortfolioDiscoveryIsOptIn(t *testing.T) {
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := NewServerWithOptions(fakeReader{}, "vtest", Options{Portfolio: fakePortfolio{}}).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "vtest"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 8 {
		t.Fatalf("got %d tools", len(tools.Tools))
	}
	resources, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, resource := range resources.Resources {
		if resource.URI == "cointop://portfolio" {
			found = true
		}
	}
	if !found {
		t.Fatal("portfolio resource not advertised")
	}
}
