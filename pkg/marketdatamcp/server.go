// Package marketdatamcp exposes cointop's normalized market data through MCP.
package marketdatamcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	apitypes "github.com/cointop-sh/cointop/pkg/api/types"
	"github.com/cointop-sh/cointop/pkg/marketdata"
	"github.com/cointop-sh/cointop/pkg/portfolio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Reader is the transport-independent market-data surface used by MCP.
type Reader interface {
	Prices(context.Context, []string, string) (marketdata.Result, error)
	Coins(context.Context, string) (marketdata.Result, error)
	Coin(context.Context, string, string) (marketdata.Result, error)
	Global(context.Context, string) (marketdata.Result, error)
	Currencies(context.Context) (marketdata.Result, error)
	CoinHistory(context.Context, string, string, string) (marketdata.Result, error)
	GlobalHistory(context.Context, string, string) (marketdata.Result, error)
}
type PortfolioReader interface {
	Load(context.Context, string) (marketdata.Result, error)
}
type Options struct{ Portfolio PortfolioReader }
type updateSubscriber interface {
	SubscribeUpdates(func(marketdata.Update)) func()
}

type PricesInput struct {
	Coins    []string `json:"coins" jsonschema:"one or more provider coin IDs, names, or symbols"`
	Currency string   `json:"currency,omitempty" jsonschema:"conversion currency, defaults to USD"`
}
type CoinsInput struct {
	Currency string `json:"currency,omitempty" jsonschema:"conversion currency, defaults to USD"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum results, 1 through 1000"`
	Offset   int    `json:"offset,omitempty" jsonschema:"zero-based result offset"`
}
type CoinInput struct {
	Identifier string `json:"identifier" jsonschema:"provider coin ID, name, or symbol"`
	Currency   string `json:"currency,omitempty" jsonschema:"conversion currency, defaults to USD"`
}
type GlobalInput struct {
	Currency string `json:"currency,omitempty" jsonschema:"conversion currency, defaults to USD"`
}
type CurrencyInput struct{}
type HistoryInput struct {
	Identifier string `json:"identifier" jsonschema:"provider coin ID, name, or symbol"`
	Currency   string `json:"currency,omitempty" jsonschema:"conversion currency, defaults to USD"`
	Range      string `json:"range,omitempty" jsonschema:"24h, 3d, 7d, 1m, 3m, 6m, ytd, 1y, or all"`
}
type GlobalHistoryInput struct {
	Currency string `json:"currency,omitempty"`
	Range    string `json:"range,omitempty"`
}

type PricesOutput struct {
	Data []marketdata.Price `json:"data"`
	Meta marketdata.Meta    `json:"meta"`
}
type CoinsOutput struct {
	Data []apitypes.Coin `json:"data"`
	Meta marketdata.Meta `json:"meta"`
}
type CoinOutput struct {
	Data apitypes.Coin   `json:"data"`
	Meta marketdata.Meta `json:"meta"`
}
type GlobalOutput struct {
	Data apitypes.GlobalMarketData `json:"data"`
	Meta marketdata.Meta           `json:"meta"`
}
type CurrenciesOutput struct {
	Data []string        `json:"data"`
	Meta marketdata.Meta `json:"meta"`
}
type CoinHistoryOutput struct {
	Data marketdata.CoinHistory `json:"data"`
	Meta marketdata.Meta        `json:"meta"`
}
type GlobalHistoryOutput struct {
	Data marketdata.GlobalHistory `json:"data"`
	Meta marketdata.Meta          `json:"meta"`
}
type PortfolioOutput struct {
	Data portfolio.Snapshot `json:"data"`
	Meta marketdata.Meta    `json:"meta"`
}

func boolPtr(v bool) *bool { return &v }

func tool(name, description string) *mcp.Tool {
	return &mcp.Tool{Name: name, Description: description, Annotations: &mcp.ToolAnnotations{
		ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true),
	}}
}

// NewServer constructs the common MCP server used by stdio and HTTP transports.
func NewServer(reader Reader, version string) *mcp.Server {
	return NewServerWithOptions(reader, version, Options{})
}

func NewServerWithOptions(reader Reader, version string, options Options) *mcp.Server {
	var serverOptions *mcp.ServerOptions
	if _, ok := reader.(updateSubscriber); ok {
		serverOptions = &mcp.ServerOptions{
			SubscribeHandler: func(_ context.Context, req *mcp.SubscribeRequest) error {
				return validatePriceSubscription(req.Params.URI)
			},
			UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
		}
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "cointop", Title: "Cointop Market Data", Version: version}, serverOptions)
	if updates, ok := reader.(updateSubscriber); ok {
		updates.SubscribeUpdates(func(update marketdata.Update) {
			_ = s.ResourceUpdated(context.Background(), &mcp.ResourceUpdatedNotificationParams{URI: update.URI})
		})
	}
	mcp.AddTool(s, tool("get_prices", "Get current prices for up to 100 coins."), func(ctx context.Context, _ *mcp.CallToolRequest, in PricesInput) (*mcp.CallToolResult, PricesOutput, error) {
		result, err := reader.Prices(ctx, in.Coins, in.Currency)
		if err != nil {
			return nil, PricesOutput{}, safeError(err)
		}
		var data []marketdata.Price
		if err := convert(result.Data, &data); err != nil {
			return nil, PricesOutput{}, safeError(err)
		}
		return nil, PricesOutput{Data: data, Meta: result.Meta}, nil
	})
	mcp.AddTool(s, tool("list_coins", "List ranked coins with normalized market data."), func(ctx context.Context, _ *mcp.CallToolRequest, in CoinsInput) (*mcp.CallToolResult, CoinsOutput, error) {
		if in.Limit == 0 {
			in.Limit = 100
		}
		if in.Limit < 1 || in.Limit > 1000 || in.Offset < 0 {
			return nil, CoinsOutput{}, safeError(marketdata.ErrInvalidInput)
		}
		result, err := reader.Coins(ctx, in.Currency)
		if err != nil {
			return nil, CoinsOutput{}, safeError(err)
		}
		var data []apitypes.Coin
		if err := convert(result.Data, &data); err != nil {
			return nil, CoinsOutput{}, safeError(err)
		}
		if in.Offset >= len(data) {
			data = []apitypes.Coin{}
		} else {
			end := in.Offset + in.Limit
			if end > len(data) {
				end = len(data)
			}
			data = data[in.Offset:end]
		}
		return nil, CoinsOutput{Data: data, Meta: result.Meta}, nil
	})
	mcp.AddTool(s, tool("get_coin", "Get normalized details for one coin."), func(ctx context.Context, _ *mcp.CallToolRequest, in CoinInput) (*mcp.CallToolResult, CoinOutput, error) {
		result, err := reader.Coin(ctx, in.Identifier, in.Currency)
		if err != nil {
			return nil, CoinOutput{}, safeError(err)
		}
		var data apitypes.Coin
		if err := convert(result.Data, &data); err != nil {
			return nil, CoinOutput{}, safeError(err)
		}
		return nil, CoinOutput{Data: data, Meta: result.Meta}, nil
	})
	mcp.AddTool(s, tool("get_global_market", "Get global cryptocurrency market totals."), func(ctx context.Context, _ *mcp.CallToolRequest, in GlobalInput) (*mcp.CallToolResult, GlobalOutput, error) {
		result, err := reader.Global(ctx, in.Currency)
		if err != nil {
			return nil, GlobalOutput{}, safeError(err)
		}
		var data apitypes.GlobalMarketData
		if err := convert(result.Data, &data); err != nil {
			return nil, GlobalOutput{}, safeError(err)
		}
		return nil, GlobalOutput{Data: data, Meta: result.Meta}, nil
	})
	mcp.AddTool(s, tool("list_currencies", "List conversion currencies supported by the configured provider."), func(ctx context.Context, _ *mcp.CallToolRequest, _ CurrencyInput) (*mcp.CallToolResult, CurrenciesOutput, error) {
		result, err := reader.Currencies(ctx)
		if err != nil {
			return nil, CurrenciesOutput{}, safeError(err)
		}
		var data []string
		if err := convert(result.Data, &data); err != nil {
			return nil, CurrenciesOutput{}, safeError(err)
		}
		return nil, CurrenciesOutput{Data: data, Meta: result.Meta}, nil
	})
	mcp.AddTool(s, tool("get_coin_history", "Get cached historical series for one coin."), func(ctx context.Context, _ *mcp.CallToolRequest, in HistoryInput) (*mcp.CallToolResult, CoinHistoryOutput, error) {
		result, err := reader.CoinHistory(ctx, in.Identifier, in.Currency, in.Range)
		if err != nil {
			return nil, CoinHistoryOutput{}, safeError(err)
		}
		var data marketdata.CoinHistory
		if err := convert(result.Data, &data); err != nil {
			return nil, CoinHistoryOutput{}, safeError(err)
		}
		return nil, CoinHistoryOutput{Data: data, Meta: result.Meta}, nil
	})
	mcp.AddTool(s, tool("get_global_history", "Get cached historical global-market series."), func(ctx context.Context, _ *mcp.CallToolRequest, in GlobalHistoryInput) (*mcp.CallToolResult, GlobalHistoryOutput, error) {
		result, err := reader.GlobalHistory(ctx, in.Currency, in.Range)
		if err != nil {
			return nil, GlobalHistoryOutput{}, safeError(err)
		}
		var data marketdata.GlobalHistory
		if err := convert(result.Data, &data); err != nil {
			return nil, GlobalHistoryOutput{}, safeError(err)
		}
		return nil, GlobalHistoryOutput{Data: data, Meta: result.Meta}, nil
	})
	if options.Portfolio != nil {
		mcp.AddTool(s, tool("get_portfolio", "Get the explicitly exposed local read-only portfolio."), func(ctx context.Context, _ *mcp.CallToolRequest, in GlobalInput) (*mcp.CallToolResult, PortfolioOutput, error) {
			result, err := options.Portfolio.Load(ctx, in.Currency)
			if err != nil {
				return nil, PortfolioOutput{}, safeError(err)
			}
			var data portfolio.Snapshot
			if err := convert(result.Data, &data); err != nil {
				return nil, PortfolioOutput{}, safeError(err)
			}
			return nil, PortfolioOutput{Data: data, Meta: result.Meta}, nil
		})
		s.AddResource(&mcp.Resource{Name: "portfolio", URI: "cointop://portfolio", MIMEType: "application/json", Description: "Explicitly exposed local portfolio."}, portfolioResource(options.Portfolio))
	}
	registerResources(s, reader)
	return s
}

func validatePriceSubscription(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "cointop" || u.Host != "prices" || strings.TrimSpace(strings.TrimPrefix(u.Path, "/")) == "" {
		return errors.New("only price resources can be subscribed")
	}
	return nil
}

// NewHTTPHandler exposes the same server contract over stateless Streamable HTTP.
func NewHTTPHandler(reader Reader, version string) http.Handler {
	return NewHTTPHandlerWithOptions(reader, version, Options{})
}

func NewHTTPHandlerWithOptions(reader Reader, version string, serverOptions Options) http.Handler {
	server := NewServerWithOptions(reader, version, serverOptions)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		MaxRequestBodyBytes:          1 << 20,
		PropagateRequestCancellation: true,
	})
}

func portfolioResource(reader PortfolioReader) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		result, err := reader.Load(ctx, "USD")
		if err != nil {
			return nil, safeError(err)
		}
		body, err := json.Marshal(result)
		if err != nil {
			return nil, errors.New("market data request failed")
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(body)}}}, nil
	}
}

func registerResources(s *mcp.Server, reader Reader) {
	h := resourceHandler(reader)
	s.AddResource(&mcp.Resource{Name: "currencies", URI: "cointop://currencies", MIMEType: "application/json", Description: "Supported conversion currencies."}, h)
	for _, template := range []*mcp.ResourceTemplate{
		{Name: "prices", URITemplate: "cointop://prices/{coins}{?currency}", MIMEType: "application/json", Description: "Current prices for comma-separated coins."},
		{Name: "coin", URITemplate: "cointop://coins/{identifier}{?currency}", MIMEType: "application/json", Description: "Details for one coin."},
		{Name: "global-market", URITemplate: "cointop://market/global{?currency}", MIMEType: "application/json", Description: "Global market totals."},
		{Name: "coin-history", URITemplate: "cointop://charts/coins/{identifier}{?currency,range}", MIMEType: "application/json", Description: "Historical series for one coin."},
		{Name: "global-history", URITemplate: "cointop://charts/global{?currency,range}", MIMEType: "application/json", Description: "Historical global market series."},
	} {
		s.AddResourceTemplate(template, h)
	}
}

func resourceHandler(reader Reader) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		raw := req.Params.URI
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "cointop" {
			return nil, mcp.ResourceNotFoundError(raw)
		}
		currency := u.Query().Get("currency")
		var result marketdata.Result
		switch u.Host {
		case "currencies":
			result, err = reader.Currencies(ctx)
		case "prices":
			result, err = reader.Prices(ctx, strings.Split(strings.TrimPrefix(u.Path, "/"), ","), currency)
		case "coins":
			result, err = reader.Coin(ctx, strings.TrimPrefix(u.Path, "/"), currency)
		case "market":
			if u.Path != "/global" {
				return nil, mcp.ResourceNotFoundError(raw)
			}
			result, err = reader.Global(ctx, currency)
		case "charts":
			path := strings.TrimPrefix(u.Path, "/")
			if strings.HasPrefix(path, "coins/") {
				result, err = reader.CoinHistory(ctx, strings.TrimPrefix(path, "coins/"), currency, u.Query().Get("range"))
			} else if path == "global" {
				result, err = reader.GlobalHistory(ctx, currency, u.Query().Get("range"))
			} else {
				return nil, mcp.ResourceNotFoundError(raw)
			}
		default:
			return nil, mcp.ResourceNotFoundError(raw)
		}
		if err != nil {
			return nil, safeError(err)
		}
		body, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("encode market data")
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: raw, MIMEType: "application/json", Text: string(body)}}}, nil
	}
}

func convert(value any, target any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func safeError(err error) error {
	switch {
	case errors.Is(err, marketdata.ErrInvalidInput):
		return errors.New("invalid input")
	case errors.Is(err, marketdata.ErrNotFound):
		return errors.New("coin not found")
	case errors.Is(err, marketdata.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return errors.New("market data provider unavailable")
	default:
		return errors.New("market data request failed")
	}
}
