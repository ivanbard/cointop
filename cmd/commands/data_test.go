package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDataPricesWritesServerJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/prices" || r.URL.Query().Get("coins") != "btc,eth" || r.URL.Query().Get("currency") != "USD" {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"symbol":"BTC","price":42}],"meta":{"provider":"fake"}}`))
	}))
	defer server.Close()

	command := DataCmd()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{"prices", "--coins", "btc,eth", "--endpoint", server.URL})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"symbol":"BTC"`) {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestDataCommandReportsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"upstream_unavailable","message":"offline"}}`))
	}))
	defer server.Close()

	command := DataCmd()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{"global", "--endpoint", server.URL})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "upstream_unavailable") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestDataCommandRejectsRemoteEndpoint(t *testing.T) {
	command := DataCmd()
	command.SetArgs([]string{"currencies", "--endpoint", "http://192.168.1.10:7070"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("unexpected error: %v", err)
	}
}
