package marketdatahttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cointop-sh/cointop/pkg/marketdata"
)

type Handler struct {
	service *marketdata.Service
	mux     *http.ServeMux
}

type errorBody struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewHandler(service *marketdata.Service) http.Handler {
	h := &Handler{service: service, mux: http.NewServeMux()}
	h.mux.HandleFunc("/v1/health", h.health)
	h.mux.HandleFunc("/v1/prices", h.prices)
	h.mux.HandleFunc("/v1/coins", h.coins)
	h.mux.HandleFunc("/v1/coins/", h.coin)
	h.mux.HandleFunc("/v1/global", h.global)
	h.mux.HandleFunc("/v1/currencies", h.currencies)
	h.mux.HandleFunc("/", h.notFound)
	return h
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
