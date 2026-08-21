package cmd

import (
	"context"
	"os"
	"time"

	"github.com/cointop-sh/cointop/cointop"
	"github.com/cointop-sh/cointop/pkg/marketdatamcp"
	"github.com/cointop-sh/cointop/pkg/pathutil"
	"github.com/cointop-sh/cointop/pkg/portfolio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// MCPCmd runs the local agent-facing MCP server over stdio.
func MCPCmd() *cobra.Command {
	var configPath, providerName, fallbackProvider, cacheDir string
	var cmcKey, cgKey, cgProKey string
	var perPage, maxPages uint
	var freshTTL, maxStale time.Duration
	var exposePortfolio bool
	command := &cobra.Command{
		Use: "mcp", Short: "Run the read-only market data MCP server over stdio",
		SilenceUsage: true,
		RunE: func(command *cobra.Command, _ []string) error {
			settings, err := loadDaemonSettings(configPath)
			if err != nil {
				return err
			}
			if command.Flags().Changed("api") {
				settings.Provider.Name = providerName
			}
			if command.Flags().Changed("cache-dir") {
				settings.CacheDir = pathutil.NormalizePath(cacheDir)
			}
			if command.Flags().Changed("coinmarketcap-api-key") {
				settings.Provider.CoinMarketCapAPIKey = cmcKey
			}
			if command.Flags().Changed("coingecko-api-key") {
				settings.Provider.CoinGeckoAPIKey = cgKey
			}
			if command.Flags().Changed("coingecko-pro-api-key") {
				settings.Provider.CoinGeckoProAPIKey = cgProKey
			}
			if command.Flags().Changed("per-page") {
				settings.Provider.PerPage = perPage
			}
			if command.Flags().Changed("max-pages") {
				settings.Provider.MaxPages = maxPages
			}
			service, err := newMarketService(settings, marketServiceOptions{FreshTTL: freshTTL, MaxStale: maxStale, FallbackProvider: fallbackProvider})
			if err != nil {
				return err
			}
			var portfolioReader *portfolio.Service
			if exposePortfolio {
				portfolioReader = portfolio.NewService(service, settings.ConfigPath)
			}
			server := marketdatamcp.NewServerWithOptions(service, cointop.Version(), marketdatamcp.Options{Portfolio: portfolioReader})
			return server.Run(context.Background(), &mcp.StdioTransport{})
		},
	}
	command.SetOut(os.Stderr)
	command.Flags().StringVar(&configPath, "config", os.Getenv("COINTOP_CONFIG"), "Cointop config filepath")
	command.Flags().StringVar(&providerName, "api", "", "Provider: coingecko or coinmarketcap")
	command.Flags().StringVar(&fallbackProvider, "fallback-api", "", "Secondary provider used only during primary outages")
	command.Flags().StringVar(&cacheDir, "cache-dir", "", "Cache directory")
	command.Flags().StringVar(&cmcKey, "coinmarketcap-api-key", "", "CoinMarketCap Pro API key")
	command.Flags().StringVar(&cgKey, "coingecko-api-key", "", "CoinGecko Demo API key")
	command.Flags().StringVar(&cgProKey, "coingecko-pro-api-key", "", "CoinGecko Pro API key")
	command.Flags().UintVar(&perPage, "per-page", 100, "Coins fetched per provider page")
	command.Flags().UintVar(&maxPages, "max-pages", 10, "Maximum provider pages fetched")
	command.Flags().DurationVar(&freshTTL, "cache-ttl", time.Minute, "Fresh-data cache duration")
	command.Flags().DurationVar(&maxStale, "max-stale", 24*time.Hour, "Maximum stale fallback duration")
	command.Flags().BoolVar(&exposePortfolio, "expose-portfolio", false, "Expose read-only portfolio data for this process")
	return command
}
