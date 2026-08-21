package cmd

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cointop-sh/cointop/cointop"
	"github.com/cointop-sh/cointop/pkg/marketdatahttp"
	"github.com/cointop-sh/cointop/pkg/marketdatamcp"
	"github.com/cointop-sh/cointop/pkg/pathutil"
	"github.com/cointop-sh/cointop/pkg/portfolio"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func APICmd() *cobra.Command {
	var listen, configPath, providerName, cacheDir string
	var cmcKey, cgKey, cgProKey string
	var perPage, maxPages uint
	var freshTTL, maxStale time.Duration
	var exposePortfolio bool

	command := &cobra.Command{
		Use:   "api",
		Short: "Run the local read-only market data API",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := marketdatahttp.ValidateListenAddress(listen); err != nil {
				return err
			}
			settings, err := loadDaemonSettings(configPath)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("api") {
				settings.Provider.Name = providerName
			}
			if cmd.Flags().Changed("cache-dir") {
				settings.CacheDir = pathutil.NormalizePath(cacheDir)
			}
			if cmd.Flags().Changed("coinmarketcap-api-key") {
				settings.Provider.CoinMarketCapAPIKey = cmcKey
			}
			if cmd.Flags().Changed("coingecko-api-key") {
				settings.Provider.CoinGeckoAPIKey = cgKey
			}
			if cmd.Flags().Changed("coingecko-pro-api-key") {
				settings.Provider.CoinGeckoProAPIKey = cgProKey
			}
			if cmd.Flags().Changed("per-page") {
				settings.Provider.PerPage = perPage
			}
			if cmd.Flags().Changed("max-pages") {
				settings.Provider.MaxPages = maxPages
			}

			service, err := newMarketService(settings, marketServiceOptions{FreshTTL: freshTTL, MaxStale: maxStale})
			if err != nil {
				return err
			}

			logger := log.New()
			logger.SetOutput(cmd.ErrOrStderr())
			logger.SetFormatter(&log.JSONFormatter{})
			var portfolioReader *portfolio.Service
			if exposePortfolio {
				portfolioReader = portfolio.NewService(service, settings.ConfigPath)
			}
			mux := http.NewServeMux()
			mux.Handle("/mcp", marketdatamcp.NewHTTPHandlerWithOptions(service, cointop.Version(), marketdatamcp.Options{Portfolio: portfolioReader}))
			mux.Handle("/", marketdatahttp.NewHandlerWithOptions(service, marketdatahttp.HandlerOptions{Portfolio: portfolioReader}))
			server := &http.Server{
				Addr: listen, Handler: marketdatahttp.WithRequestLogging(mux, logger),
				ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
				WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			serverErr := make(chan error, 1)
			go func() { serverErr <- server.ListenAndServe() }()
			logger.WithFields(log.Fields{"listen": listen, "provider": service.Provider()}).Info("cointop API started")
			select {
			case err := <-serverErr:
				if err != nil && err != http.ErrServerClosed {
					return err
				}
				return nil
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				return server.Shutdown(shutdownCtx)
			}
		},
	}
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:7070", "Loopback address for the HTTP API")
	command.Flags().StringVar(&configPath, "config", os.Getenv("COINTOP_CONFIG"), "Cointop config filepath")
	command.Flags().StringVar(&providerName, "api", "", "Provider: coingecko or coinmarketcap")
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
