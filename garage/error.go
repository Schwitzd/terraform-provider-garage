package garage

import (
	"encoding/json"
	"net/http"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
)

type garageAPIError struct {
	Message string `json:"message"`
}

func createDiagnositc(err error, http *http.Response) diag.Diagnostic {
	diagnostic := diag.Diagnostic{
		Severity: diag.Error,
		Summary:  err.Error(),
	}

	if http != nil && http.Body != nil {
		defer http.Body.Close()

		apiError := new(garageAPIError)

		if decodeErr := json.NewDecoder(http.Body).Decode(apiError); decodeErr == nil {
			diagnostic.Detail = apiError.Message
		}
	}

	return diagnostic
}
