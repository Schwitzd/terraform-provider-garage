package garage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestProviderDefinition(t *testing.T) {
	p := Provider()
	if p == nil {
		t.Fatalf("Provider returned nil")
	}
	if p.ConfigureContextFunc == nil {
		t.Fatalf("expected ConfigureContextFunc to be set")
	}

	for _, key := range []string{"host", "scheme", "token"} {
		if _, ok := p.Schema[key]; !ok {
			t.Fatalf("provider schema missing %q attribute", key)
		}
	}

	for _, resource := range []string{
		"garage_bucket",
		"garage_bucket_alias",
		"garage_bucket_key",
		"garage_key",
	} {
		if _, ok := p.ResourcesMap[resource]; !ok {
			t.Fatalf("provider missing resource %q", resource)
		}
	}
}

func TestProviderConfigureSuccess(t *testing.T) {
	token := "token-123"
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/GetClusterStatus" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"layoutVersion":1,"nodes":[{"draining":false,"id":"node-1","isUp":true,"garageVersion":"2.2.0"}]}`)
	}))
	defer server.Close()

	p := Provider()
	data := schema.TestResourceDataRaw(t, p.Schema, map[string]interface{}{
		"host":   server.URL,
		"scheme": "http",
		"token":  token,
	})

	cfg, diags := providerConfigure(context.Background(), data)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if gotAuth != "Bearer "+token {
		t.Fatalf("expected auth header, got %q", gotAuth)
	}

	provider, ok := cfg.(*garageProvider)
	if !ok {
		t.Fatalf("expected *garageProvider, got %#v", cfg)
	}
	if provider.token != token {
		t.Fatalf("expected token %q, got %q", token, provider.token)
	}
	if provider.client == nil || provider.httpClient == nil {
		t.Fatalf("expected client and http client to be initialized")
	}
	expectedHost := strings.TrimPrefix(server.URL, "http://")
	if provider.client.GetConfig().Host != expectedHost {
		t.Fatalf("expected host %q, got %q", expectedHost, provider.client.GetConfig().Host)
	}
	if provider.client.GetConfig().Scheme != "http" {
		t.Fatalf("expected scheme http, got %q", provider.client.GetConfig().Scheme)
	}
}

func TestProviderConfigureRequiresHostAndToken(t *testing.T) {
	p := Provider()
	data := schema.TestResourceDataRaw(t, p.Schema, map[string]interface{}{})

	cfg, diags := providerConfigure(context.Background(), data)
	if cfg != nil {
		t.Fatalf("expected configure to fail")
	}
	if len(diags) != 1 {
		t.Fatalf("expected single diagnostic, got %#v", diags)
	}
	if diags[0].Severity != diag.Error {
		t.Fatalf("expected error diagnostic, got %#v", diags[0])
	}
}

func TestDetectGarageVersionV2Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/GetClusterStatus" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"layoutVersion":1,"nodes":[{"draining":false,"id":"node-1","isUp":true,"garageVersion":"2.3.0"}]}`)
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src != "v2" {
		t.Fatalf("expected source v2, got %q", src)
	}
	if ver.Original() != "2.3.0" {
		t.Fatalf("expected version 2.3.0, got %s", ver.Original())
	}
}

func TestDetectGarageVersionV2Invalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/GetClusterStatus" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"layoutVersion":1,"nodes":[{"draining":false,"id":"node-1","isUp":true}]}`)
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, "token")
	if err == nil {
		t.Fatalf("expected error for invalid v2 payload")
	}
	if ver != nil || src != "" {
		t.Fatalf("expected nil version and empty source, got %v %q", ver, src)
	}
	if !strings.Contains(err.Error(), "v2 payload invalid") {
		t.Fatalf("expected v2 payload invalid error, got %v", err)
	}
}

func TestDetectGarageVersionFallbackToV1(t *testing.T) {
	var gotV1Auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/GetClusterStatus":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"code":"InvalidRequest","message":"Bad request: Unknown API endpoint: GET /v2/GetClusterStatus","path":"/v2/GetClusterStatus"}`)
		case "/v1/status":
			gotV1Auth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"garageVersion":"v2.4.0"}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")
	token := "token-xyz"

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src != "v1" {
		t.Fatalf("expected source v1, got %q", src)
	}
	if ver.Original() != "2.4.0" {
		t.Fatalf("expected version 2.4.0, got %s", ver.Original())
	}
	if gotV1Auth != "Bearer "+token {
		t.Fatalf("expected v1 auth header, got %q", gotV1Auth)
	}
}

func TestDetectGarageVersionMissingV2UnauthorizedV1(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/GetClusterStatus":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"code":"InvalidRequest","message":"Bad request: Unknown API endpoint: GET /v2/GetClusterStatus","path":"/v2/GetClusterStatus"}`)
		case "/v1/status":
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, "token")
	if err == nil {
		t.Fatalf("expected error when v2 missing and v1 unauthorized")
	}
	if ver != nil || src != "" {
		t.Fatalf("expected nil version and empty source, got %v %q", ver, src)
	}
	msg := err.Error()
	if !strings.Contains(msg, "requires Garage 2.0.0 or newer") || !strings.Contains(msg, "Unknown API endpoint") {
		t.Fatalf("unexpected error message %q", msg)
	}
}

func TestDetectGarageVersionAuthFailureStopsFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/GetClusterStatus" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"code":"AccessDenied","message":"Forbidden: Invalid bearer token"}`)
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, "token")
	if err == nil {
		t.Fatalf("expected error on auth failure")
	}
	if ver != nil || src != "" {
		t.Fatalf("expected nil version and empty source, got %v %q", ver, src)
	}
	msg := err.Error()
	if !strings.Contains(msg, "403") || !strings.Contains(msg, "AccessDenied") {
		t.Fatalf("unexpected error message %q", msg)
	}
}

func TestDetectGarageVersionBadRequestStopsFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/GetClusterStatus" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"code":"InvalidRequest","message":"Bad request: Missing parameters"}`)
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, "token")
	if err == nil {
		t.Fatalf("expected error on v2 bad request")
	}
	if ver != nil || src != "" {
		t.Fatalf("expected nil version and empty source, got %v %q", ver, src)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("unexpected error message %q", err)
	}
}

func TestDetectGarageVersionServerErrorStopsFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/GetClusterStatus" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, "token")
	if err == nil {
		t.Fatalf("expected error on server failure")
	}
	if ver != nil || src != "" {
		t.Fatalf("expected nil version and empty source, got %v %q", ver, src)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("unexpected error message %q", err)
	}
}

func TestDetectGarageVersionBothFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/GetClusterStatus":
			http.NotFound(w, r)
		case "/v1/status":
			http.Error(w, "nope", http.StatusInternalServerError)
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, "token")
	if err == nil {
		t.Fatalf("expected error when both version probes fail")
	}
	if ver != nil || src != "" {
		t.Fatalf("expected nil version and empty source, got %v %q", ver, src)
	}
	if !strings.Contains(err.Error(), "this provider requires Garage 2.0.0 or newer") ||
		!strings.Contains(err.Error(), "404 Not Found") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestSanitizeHost(t *testing.T) {
	host, scheme, err := sanitizeHost("https://garage.example.com:3903")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "garage.example.com:3903" || scheme != "https" {
		t.Fatalf("unexpected sanitize result %q %q", host, scheme)
	}

	if _, _, err = sanitizeHost(""); err == nil {
		t.Fatalf("expected error on empty host")
	}
	if _, _, err = sanitizeHost("http://example.com/path"); err == nil {
		t.Fatalf("expected error when path present")
	}
}

func TestNormalizeVersion(t *testing.T) {
	v, err := normalizeVersion("v2.1.0")
	if err != nil || v != "2.1.0" {
		t.Fatalf("unexpected normalize result %q (%v)", v, err)
	}
	v, err = normalizeVersion(" 2.1.0 ")
	if err != nil || v != "2.1.0" {
		t.Fatalf("expected whitespace to be trimmed, got %q (%v)", v, err)
	}
	if _, err = normalizeVersion("not-semver"); err == nil {
		t.Fatalf("expected error for invalid semver")
	}
}

func TestEnforceV2(t *testing.T) {
	v, _ := semver.NewVersion("2.1.0")
	if err := enforceV2(v); err != nil {
		t.Fatalf("enforceV2 failed for valid version: %v", err)
	}
	v, _ = semver.NewVersion("1.9.0")
	if err := enforceV2(v); err == nil {
		t.Fatalf("enforceV2 should fail for old version")
	}
}

func TestMinClusterSemverFromProbe(t *testing.T) {
	body := []byte(`{"layoutVersion":1,"nodes":[{"id":"node-1","garageVersion":"v2.2.0"}]}`)

	v, err := minClusterSemverFromProbe(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Original() != "2.2.0" {
		t.Fatalf("unexpected version %q", v.Original())
	}

	if _, err := minClusterSemverFromProbe([]byte(`{"layoutVersion":1,"nodes":[{"id":"node-1"}]}`)); err == nil {
		t.Fatalf("expected error when node lacks version")
	}

	if _, err := minClusterSemverFromProbe([]byte(`{"layoutVersion":1,"nodes":[]}`)); err == nil {
		t.Fatalf("expected error when payload has no nodes")
	}

	if _, err := minClusterSemverFromProbe([]byte(`not-json`)); err == nil {
		t.Fatalf("expected error for malformed payload")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProbeV1Version(t *testing.T) {
	var gotAuth string
	client := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			gotAuth = r.Header.Get("Authorization")
			return &http.Response{
				StatusCode: 200,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"garageVersion":"2.5.0"}`)),
			}, nil
		}),
	}

	version, err := probeV1Version(context.Background(), client, "http", "localhost:3903", "token123")
	if err != nil {
		t.Fatalf("probeV1Version failed: %v", err)
	}
	if version != "2.5.0" {
		t.Fatalf("unexpected version %q", version)
	}
	if gotAuth != "Bearer token123" {
		t.Fatalf("expected auth header to be set, got %q", gotAuth)
	}
}

func TestEnrichV2HTTP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com/v2/GetClusterStatus", nil)
	resp := &http.Response{
		Status:     "500 Internal Server Error",
		StatusCode: 500,
		Request:    req,
	}

	err := enrichV2HTTP(fmt.Errorf("boom"), resp)
	if err == nil {
		t.Fatalf("expected enriched error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "/v2/GetClusterStatus") || !strings.Contains(msg, "boom") {
		t.Fatalf("unexpected error message %q", msg)
	}
}

/* The fixtures in this section mirror real Garage admin API responses. Garage
v2.5.0 added per-node additive properties (e.g. resyncQueueLen); the provider
keeps working with future minor releases that add more of them. */

const v25ClusterStatusBody = `{"layoutVersion":1,"nodes":[
	{"id":"6a063ab65f1648bc","garageVersion":"v2.5.0","addr":"[::]:3901","hostname":"garage-01","isUp":true,"lastSeenSecsAgo":null,"role":null,"draining":false,"dataPartition":{"available":19129200640,"total":20957446144},"metadataPartition":{"available":19129200640,"total":20957446144},"resyncQueueLen":0},
	{"id":"bb1207f9aa8a3f68","garageVersion":"v2.5.0","addr":"[::]:3901","hostname":"garage-02","isUp":true,"lastSeenSecsAgo":null,"role":null,"draining":false,"dataPartition":{"available":19129200640,"total":20957446144},"metadataPartition":{"available":19129200640,"total":20957446144},"resyncQueueLen":3}]}`

func TestDetectGarageVersionV25AdditiveResyncQueueLen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/GetClusterStatus" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, v25ClusterStatusBody)
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, "token")
	if err != nil {
		t.Fatalf("expected detection to tolerate Garage v2.5.0 additive fields, got %v", err)
	}
	if src != "v2" {
		t.Fatalf("expected source v2, got %q", src)
	}
	if ver.Original() != "2.5.0" {
		t.Fatalf("expected minimum version 2.5.0, got %s", ver.Original())
	}
}

func TestDetectGarageVersionV2ToleratesUnknownFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/GetClusterStatus" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"layoutVersion":1,"nodes":[{"draining":false,"id":"node-1","isUp":true,"garageVersion":"v2.6.0","someFutureField":{"unknown":true}}],"futureClusterKey":1}`)
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, "token")
	if err != nil {
		t.Fatalf("expected detection to tolerate unknown additive fields, got %v", err)
	}
	if src != "v2" {
		t.Fatalf("expected source v2, got %q", src)
	}
	if ver.Original() != "2.6.0" {
		t.Fatalf("expected minimum version 2.6.0, got %s", ver.Original())
	}
}

func TestDetectGarageVersionV2EmptyNodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/GetClusterStatus" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"layoutVersion":1,"nodes":[]}`)
	}))
	defer server.Close()

	httpClient := server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	host = strings.TrimPrefix(host, "https://")

	ver, src, err := detectGarageVersion(context.Background(), httpClient, "http", host, "token")
	if err == nil {
		t.Fatalf("expected error for v2 payload without nodes")
	}
	if ver != nil || src != "" {
		t.Fatalf("expected nil version and empty source, got %v %q", ver, src)
	}
	if !strings.Contains(err.Error(), "v2 payload invalid") {
		t.Fatalf("expected v2 payload invalid error, got %v", err)
	}
}
