package garage

import (
	"encoding/json"
	"net/http"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
)

type GarageAPIError struct {
	Message string `json:"message"`
}

func CreateDiagnositc(err error, http *http.Response) diag.Diagnostic {
	diagnostic := diag.Diagnostic{
		Severity: diag.Error,
		Summary:  err.Error(),
	}

	apiError := new(GarageAPIError)

	decodeErr := json.NewDecoder(http.Body).Decode(apiError)
	if decodeErr == nil {
		diagnostic.Detail = apiError.Message
	}

	return diagnostic
}
