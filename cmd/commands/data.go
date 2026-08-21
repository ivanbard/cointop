package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cointop-sh/cointop/pkg/marketdatahttp"
	"github.com/spf13/cobra"
)

const defaultDataEndpoint = "http://127.0.0.1:7070"

func DataCmd() *cobra.Command {
	var endpoint string
	var timeout time.Duration
	command := &cobra.Command{Use: "data", Short: "Query the local cointop market data API", SilenceUsage: true}
	command.PersistentFlags().StringVar(&endpoint, "endpoint", defaultDataEndpoint, "Loopback cointop API endpoint")
	command.PersistentFlags().DurationVar(&timeout, "timeout", 15*time.Second, "Request timeout")
	command.AddCommand(
		dataPricesCmd(&endpoint, &timeout), dataCoinsCmd(&endpoint, &timeout),
		dataCoinCmd(&endpoint, &timeout), dataGlobalCmd(&endpoint, &timeout),
		dataCurrenciesCmd(&endpoint, &timeout),
		dataChartCmd(&endpoint, &timeout),
		dataPortfolioCmd(&endpoint, &timeout),
	)
	return command
}

func dataPortfolioCmd(endpoint *string, timeout *time.Duration) *cobra.Command {
	var currency string
	command := &cobra.Command{Use: "portfolio", Short: "Get an explicitly exposed read-only portfolio", RunE: func(cmd *cobra.Command, _ []string) error {
		return requestData(cmd, *endpoint, "/v1/portfolio", url.Values{"currency": {currency}}, *timeout)
	}}
	command.Flags().StringVar(&currency, "currency", "USD", "Portfolio currency")
	return command
}

func dataChartCmd(endpoint *string, timeout *time.Duration) *cobra.Command {
	command := &cobra.Command{Use: "chart", Short: "Get cached historical chart data"}
	command.AddCommand(dataCoinChartCmd(endpoint, timeout), dataGlobalChartCmd(endpoint, timeout))
	return command
}

func dataCoinChartCmd(endpoint *string, timeout *time.Duration) *cobra.Command {
	var currency, chartRange string
	command := &cobra.Command{Use: "coin <identifier>", Short: "Get coin history", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return requestData(cmd, *endpoint, "/v1/charts/coins/"+url.PathEscape(args[0]), url.Values{"currency": {currency}, "range": {chartRange}}, *timeout)
	}}
	command.Flags().StringVar(&currency, "currency", "USD", "Conversion currency")
	command.Flags().StringVar(&chartRange, "range", "24h", "Range: 24h, 3d, 7d, 1m, 3m, 6m, ytd, 1y, or all")
	return command
}

func dataGlobalChartCmd(endpoint *string, timeout *time.Duration) *cobra.Command {
	var currency, chartRange string
	command := &cobra.Command{Use: "global", Short: "Get global market history", RunE: func(cmd *cobra.Command, _ []string) error {
		return requestData(cmd, *endpoint, "/v1/charts/global", url.Values{"currency": {currency}, "range": {chartRange}}, *timeout)
	}}
	command.Flags().StringVar(&currency, "currency", "USD", "Conversion currency")
	command.Flags().StringVar(&chartRange, "range", "24h", "Range: 24h, 3d, 7d, 1m, 3m, 6m, ytd, 1y, or all")
	return command
}

func dataPricesCmd(endpoint *string, timeout *time.Duration) *cobra.Command {
	var coins []string
	var currency string
	command := &cobra.Command{Use: "prices", Short: "Get current coin prices", RunE: func(cmd *cobra.Command, args []string) error {
		if len(coins) == 0 {
			return fmt.Errorf("at least one coin is required")
		}
		return requestData(cmd, *endpoint, "/v1/prices", url.Values{"coins": {strings.Join(coins, ",")}, "currency": {currency}}, *timeout)
	}}
	command.Flags().StringSliceVar(&coins, "coins", nil, "Coin names or symbols, comma separated")
	command.Flags().StringVar(&currency, "currency", "USD", "Conversion currency")
	return command
}

func dataCoinsCmd(endpoint *string, timeout *time.Duration) *cobra.Command {
	var currency string
	var limit, offset int
	command := &cobra.Command{Use: "coins", Short: "List market coins", RunE: func(cmd *cobra.Command, args []string) error {
		return requestData(cmd, *endpoint, "/v1/coins", url.Values{
			"currency": {currency}, "limit": {fmt.Sprint(limit)}, "offset": {fmt.Sprint(offset)},
		}, *timeout)
	}}
	command.Flags().StringVar(&currency, "currency", "USD", "Conversion currency")
	command.Flags().IntVar(&limit, "limit", 100, "Maximum records")
	command.Flags().IntVar(&offset, "offset", 0, "Record offset")
	return command
}

func dataCoinCmd(endpoint *string, timeout *time.Duration) *cobra.Command {
	var currency string
	command := &cobra.Command{Use: "coin <identifier>", Short: "Get one coin", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return requestData(cmd, *endpoint, "/v1/coins/"+url.PathEscape(args[0]), url.Values{"currency": {currency}}, *timeout)
	}}
	command.Flags().StringVar(&currency, "currency", "USD", "Conversion currency")
	return command
}

func dataGlobalCmd(endpoint *string, timeout *time.Duration) *cobra.Command {
	var currency string
	command := &cobra.Command{Use: "global", Short: "Get global market data", RunE: func(cmd *cobra.Command, args []string) error {
		return requestData(cmd, *endpoint, "/v1/global", url.Values{"currency": {currency}}, *timeout)
	}}
	command.Flags().StringVar(&currency, "currency", "USD", "Conversion currency")
	return command
}

func dataCurrenciesCmd(endpoint *string, timeout *time.Duration) *cobra.Command {
	return &cobra.Command{Use: "currencies", Short: "List supported currencies", RunE: func(cmd *cobra.Command, args []string) error {
		return requestData(cmd, *endpoint, "/v1/currencies", nil, *timeout)
	}}
}

func requestData(cmd *cobra.Command, endpoint, path string, query url.Values, timeout time.Duration) error {
	endpoint = strings.TrimRight(endpoint, "/")
	if err := marketdatahttp.ValidateEndpoint(endpoint); err != nil {
		return err
	}
	requestURL := endpoint + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	client := &http.Client{Timeout: timeout}
	response, err := client.Get(requestURL)
	if err != nil {
		return fmt.Errorf("local cointop API request failed: %w", err)
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, 16<<20)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(cmd.ErrOrStderr(), reader)
		return fmt.Errorf("local cointop API returned %s", response.Status)
	}
	if _, err := io.Copy(cmd.OutOrStdout(), reader); err != nil {
		return err
	}
	return nil
}
