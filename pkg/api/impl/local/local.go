// Package local implements the cointop provider interface through its local daemon.
package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cointop-sh/cointop/pkg/api/types"
	"github.com/cointop-sh/cointop/pkg/marketdata"
	"github.com/cointop-sh/cointop/pkg/marketdatahttp"
)

type Client struct {
	endpoint string
	http     *http.Client
}

func New(endpoint string) (*Client, error) {
	if err := marketdatahttp.ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), http: &http.Client{Timeout: 30 * time.Second}}, nil
}

type envelope[T any] struct {
	Data T               `json:"data"`
	Meta marketdata.Meta `json:"meta"`
}
type page struct {
	Items  []types.Coin `json:"items"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
	Total  int          `json:"total"`
}

func (c *Client) get(ctx context.Context, path string, query url.Values, target any) error {
	requestURL := c.endpoint + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("local cointop API: %w", err)
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, 32<<20)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, reader)
		return fmt.Errorf("local cointop API returned %s", response.Status)
	}
	if err := json.NewDecoder(reader).Decode(target); err != nil {
		return fmt.Errorf("decode local cointop API: %w", err)
	}
	return nil
}

func (c *Client) Ping() error {
	var out envelope[marketdata.Health]
	return c.get(context.Background(), "/v1/ready", nil, &out)
}

func (c *Client) GetAllCoinData(convert string, ch chan []types.Coin) error {
	var pages [][]types.Coin
	for result := range c.StreamAllCoinData(context.Background(), convert) {
		if result.Err != nil {
			return result.Err
		}
		pages = append(pages, result.Coins)
	}
	go func() {
		defer close(ch)
		for _, items := range pages {
			ch <- items
		}
	}()
	return nil
}

// StreamAllCoinData fetches local API pages with cancellation and explicit errors.
func (c *Client) StreamAllCoinData(ctx context.Context, convert string) <-chan types.CoinPageResult {
	results := make(chan types.CoinPageResult, 1)
	go func() {
		defer close(results)
		const limit = 500
		for offset := 0; ; offset += limit {
			var out envelope[page]
			if err := c.get(ctx, "/v1/coins", url.Values{"currency": {convert}, "limit": {fmt.Sprint(limit)}, "offset": {fmt.Sprint(offset)}}, &out); err != nil {
				results <- types.CoinPageResult{Err: err}
				return
			}
			if len(out.Data.Items) == 0 {
				return
			}
			select {
			case results <- types.CoinPageResult{Coins: out.Data.Items}:
			case <-ctx.Done():
				results <- types.CoinPageResult{Err: ctx.Err()}
				return
			}
			if offset+len(out.Data.Items) >= out.Data.Total {
				break
			}
		}
	}()
	return results
}

func (c *Client) GetCoinGraphData(convert, symbol, name string, start, end int64) (types.CoinGraph, error) {
	identifier := name
	if strings.TrimSpace(identifier) == "" {
		identifier = symbol
	}
	var out envelope[marketdata.CoinHistory]
	err := c.get(context.Background(), "/v1/charts/coins/"+url.PathEscape(identifier), url.Values{"currency": {convert}, "range": {rangeFor(start, end)}}, &out)
	return out.Data.Series, err
}

func (c *Client) GetGlobalMarketGraphData(convert string, start, end int64) (types.MarketGraph, error) {
	var out envelope[marketdata.GlobalHistory]
	err := c.get(context.Background(), "/v1/charts/global", url.Values{"currency": {convert}, "range": {rangeFor(start, end)}}, &out)
	return out.Data.Series, err
}

func (c *Client) GetGlobalMarketData(convert string) (types.GlobalMarketData, error) {
	var out envelope[types.GlobalMarketData]
	err := c.get(context.Background(), "/v1/global", url.Values{"currency": {convert}}, &out)
	return out.Data, err
}

func (c *Client) GetCoinData(name, convert string) (types.Coin, error) {
	var out envelope[types.Coin]
	err := c.get(context.Background(), "/v1/coins/"+url.PathEscape(name), url.Values{"currency": {convert}}, &out)
	return out.Data, err
}

func (c *Client) GetCoinDataBatch(names []string, convert string) ([]types.Coin, error) {
	coins := make([]types.Coin, 0, len(names))
	for _, name := range names {
		coin, err := c.GetCoinData(name, convert)
		if err != nil {
			return nil, err
		}
		coins = append(coins, coin)
	}
	return coins, nil
}

func (c *Client) CoinLink(slug string) string {
	var out envelope[marketdata.CoinLink]
	if err := c.get(context.Background(), "/v1/links/coins/"+url.PathEscape(slug), nil, &out); err != nil {
		return ""
	}
	return out.Data.URL
}

func (c *Client) SupportedCurrencies() []string {
	var out envelope[[]string]
	if err := c.get(context.Background(), "/v1/currencies", nil, &out); err != nil {
		return nil
	}
	return out.Data
}

func (c *Client) Price(name, convert string) (float64, error) {
	var out envelope[[]marketdata.Price]
	if err := c.get(context.Background(), "/v1/prices", url.Values{"coins": {name}, "currency": {convert}}, &out); err != nil {
		return 0, err
	}
	if len(out.Data) == 0 {
		return 0, fmt.Errorf("coin not found")
	}
	return out.Data[0].Price, nil
}

func (c *Client) GetExchangeRate(from, to string, _ bool) (float64, error) {
	var out envelope[marketdata.ExchangeRate]
	if err := c.get(context.Background(), "/v1/exchange-rate", url.Values{"from": {from}, "to": {to}}, &out); err != nil {
		return 0, err
	}
	return out.Data.Rate, nil
}

func rangeFor(start, end int64) string {
	if start == 0 && end > start {
		return "all"
	}
	if start < 0 || end <= start {
		return "24h"
	}
	startTime, endTime := time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()
	beginningOfYear := time.Date(endTime.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	if startTime.Sub(beginningOfYear) >= -24*time.Hour && startTime.Sub(beginningOfYear) <= 24*time.Hour {
		return "ytd"
	}
	d := time.Duration(end-start) * time.Second
	switch {
	case d <= 24*time.Hour:
		return "24h"
	case d <= 3*24*time.Hour:
		return "3d"
	case d <= 7*24*time.Hour:
		return "7d"
	case d <= 32*24*time.Hour:
		return "1m"
	case d <= 95*24*time.Hour:
		return "3m"
	case d <= 190*24*time.Hour:
		return "6m"
	case d <= 370*24*time.Hour:
		return "1y"
	default:
		return "all"
	}
}
