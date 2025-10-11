package garage

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFirstNonEmpty(t *testing.T) {
	if firstNonEmpty("", " ", "\t", "value") != "value" {
		t.Fatalf("expected firstNonEmpty to return first non whitespace entry")
	}
	if firstNonEmpty() != "" {
		t.Fatalf("expected empty result when no values provided")
	}
}

func TestCreateDiagnosticsJSON(t *testing.T) {
	body := `{"message":"something went wrong"}`
	resp := &http.Response{
		StatusCode: 400,
		Status:     "400 Bad Request",
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	diags := createDiagnostics(io.EOF, resp)
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %#v", diags)
	}
	if !strings.Contains(diags[0].Detail, "something went wrong") {
		t.Fatalf("expected JSON message in detail, got %#v", diags[0].Detail)
	}
}

func TestCreateDiagnosticsPlainText(t *testing.T) {
	resp := &http.Response{
		StatusCode: 500,
		Status:     "500 Internal Server Error",
		Body:       io.NopCloser(bytes.NewBufferString("boom")),
	}

	diags := createDiagnostics(io.EOF, resp)
	if len(diags) != 1 || diags[0].Detail != "boom" {
		t.Fatalf("expected raw body to be propagated, got %#v", diags)
	}
}
