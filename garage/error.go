package garage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
)

type garageAPIError struct {
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func createDiagnostics(err error, resp *http.Response) diag.Diagnostics {
	if resp == nil {
		return diag.FromErr(err)
	}
	defer resp.Body.Close()

	summary := fmt.Sprintf("Garage API error (%d %s)", resp.StatusCode, http.StatusText(resp.StatusCode))

	d := diag.Diagnostic{
		Severity: diag.Error,
		Summary:  summary,
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if len(body) > 0 {
		// Try JSON
		var ge garageAPIError
		if json.Unmarshal(body, &ge) == nil {
			if msg := strings.TrimSpace(firstNonEmpty(ge.Message, ge.Error, ge.Detail)); msg != "" {
				d.Detail = msg
				return diag.Diagnostics{d}
			}
		}
		// Fallback: raw text
		if raw := strings.TrimSpace(string(body)); raw != "" {
			d.Detail = raw
			return diag.Diagnostics{d}
		}
	}

	d.Detail = "empty response body"
	return diag.Diagnostics{d}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
