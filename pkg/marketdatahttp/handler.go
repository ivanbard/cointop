package marketdatahttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cointop-sh/cointop/pkg/marketdata"
)

type Handler struct {
	service   *marketdata.Service
	portfolio PortfolioReader
	mux       *http.ServeMux
}

type PortfolioReader interface {
	Load(context.Context, string) (marketdata.Result, error)
}
type HandlerOptions struct{ Portfolio PortfolioReader }

type errorBody struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewHandler(service *marketdata.Service) http.Handler {
	return NewHandlerWithOptions(service, HandlerOptions{})
}

func NewHandlerWithOptions(service *marketdata.Service, options HandlerOptions) http.Handler {
	h := &Handler{service: service, portfolio: options.Portfolio, mux: http.NewServeMux()}
	h.mux.HandleFunc("/v1/health", h.health)
	h.mux.HandleFunc("/v1/ready", h.ready)
	h.mux.HandleFunc("/v1/prices", h.prices)
	h.mux.HandleFunc("/v1/coins", h.coins)
	h.mux.HandleFunc("/v1/coins/", h.coin)
	h.mux.HandleFunc("/v1/global", h.global)
	h.mux.HandleFunc("/v1/currencies", h.currencies)
	h.mux.HandleFunc("/v1/charts/coins/", h.coinHistory)
	h.mux.HandleFunc("/v1/charts/global", h.globalHistory)
	h.mux.HandleFunc("/v1/exchange-rate", h.exchangeRate)
	h.mux.HandleFunc("/v1/links/coins/", h.coinLink)
	h.mux.HandleFunc("/v1/stream/prices", h.streamPrices)
	if h.portfolio != nil {
		h.mux.HandleFunc("/v1/portfolio", h.portfolioSnapshot)
	}
	h.mux.HandleFunc("/", h.notFound)
	return h
}

func (h *Handler) portfolioSnapshot(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	result, err := h.portfolio.Load(r.Context(), r.URL.Query().Get("currency"))
	writeResult(w, result, err)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "route not found")
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, h.service.Health())
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	result, err := h.service.Ready(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) prices(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	coins := splitCSV(r.URL.Query().Get("coins"))
	result, err := h.service.Prices(r.Context(), coins, r.URL.Query().Get("currency"))
	writeResult(w, result, err)
}

func (h *Handler) coins(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	limit, err := boundedInt(r.URL.Query(), "limit", 100, 1, 500)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	offset, err := boundedInt(r.URL.Query(), "offset", 0, 0, 1000000)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	result, err := h.service.Coins(r.Context(), r.URL.Query().Get("currency"))
	if err != nil {
		writeResult(w, result, err)
		return
	}
	items, ok := result.Data.([]interface{})
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected cached coin data")
		return
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	result.Data = map[string]interface{}{
		"items": items[offset:end], "limit": limit, "offset": offset, "total": len(items),
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) coin(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	identifier, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/coins/"))
	if err != nil || strings.TrimSpace(identifier) == "" {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	result, serviceErr := h.service.Coin(r.Context(), identifier, r.URL.Query().Get("currency"))
	writeResult(w, result, serviceErr)
}

func (h *Handler) global(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	result, err := h.service.Global(r.Context(), r.URL.Query().Get("currency"))
	writeResult(w, result, err)
}

func (h *Handler) currencies(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	result, err := h.service.Currencies(r.Context())
	writeResult(w, result, err)
}

func (h *Handler) coinHistory(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	identifier, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/charts/coins/"))
	if err != nil || strings.TrimSpace(identifier) == "" {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	result, serviceErr := h.service.CoinHistory(r.Context(), identifier, r.URL.Query().Get("currency"), r.URL.Query().Get("range"))
	writeResult(w, result, serviceErr)
}

func (h *Handler) globalHistory(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	result, err := h.service.GlobalHistory(r.Context(), r.URL.Query().Get("currency"), r.URL.Query().Get("range"))
	writeResult(w, result, err)
}

func (h *Handler) exchangeRate(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	result, err := h.service.ExchangeRate(r.Context(), r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	writeResult(w, result, err)
}

func (h *Handler) coinLink(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	identifier, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/links/coins/"))
	if err != nil || strings.TrimSpace(identifier) == "" {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	result, serviceErr := h.service.CoinLink(r.Context(), identifier)
	writeResult(w, result, serviceErr)
}

func (h *Handler) streamPrices(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	coins := splitCSV(r.URL.Query().Get("coins"))
	if len(coins) == 0 || len(coins) > 100 {
		writeError(w, http.StatusBadRequest, "invalid_input", "coins must contain between 1 and 100 identifiers")
		return
	}
	interval, err := streamInterval(r.URL.Query().Get("interval"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	writePrices := func() bool {
		result, requestErr := h.service.Prices(r.Context(), coins, r.URL.Query().Get("currency"))
		var event string
		var payload any
		if requestErr != nil {
			event = "error"
			streamErr := apiError{Code: "internal_error", Message: "market data request failed"}
			switch {
			case errors.Is(requestErr, marketdata.ErrInvalidInput):
				streamErr = apiError{Code: "invalid_input", Message: "invalid input"}
			case errors.Is(requestErr, marketdata.ErrNotFound):
				streamErr = apiError{Code: "coin_not_found", Message: "coin was not found"}
			case errors.Is(requestErr, marketdata.ErrUnavailable):
				streamErr = apiError{Code: "upstream_unavailable", Message: "market data provider is unavailable"}
			}
			payload = errorBody{Error: streamErr}
		} else {
			event = "prices"
			payload = result
		}
		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return false
		}
		if _, writeErr := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); writeErr != nil {
			return false
		}
		return controller.Flush() == nil
	}
	if !writePrices() {
		return
	}
	pricesTicker := time.NewTicker(interval)
	defer pricesTicker.Stop()
	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeatTicker.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
		case <-pricesTicker.C:
			if !writePrices() {
				return
			}
		}
	}
}

func streamInterval(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return time.Minute, nil
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval < 15*time.Second || interval > time.Hour {
		return 0, errors.New("interval must be between 15s and 1h")
	}
	return interval, nil
}

func getOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
	return false
}

func writeResult(w http.ResponseWriter, result marketdata.Result, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, result)
		return
	}
	switch {
	case errors.Is(err, marketdata.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
	case errors.Is(err, marketdata.ErrNotFound):
		writeError(w, http.StatusNotFound, "coin_not_found", err.Error())
	case errors.Is(err, marketdata.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "upstream_unavailable", "market data provider is unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: apiError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func boundedInt(values url.Values, name string, fallback, min, max int) (int, error) {
	raw := values.Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, errors.New(name + " must be between " + strconv.Itoa(min) + " and " + strconv.Itoa(max))
	}
	return value, nil
}
