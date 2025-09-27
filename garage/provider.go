package garage

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type garageProvider struct {
	client *garage.APIClient
	ctx    context.Context
}

func updateContext(tfCtx context.Context, p *garageProvider) context.Context {
	return context.WithValue(tfCtx, garage.ContextAccessToken, p.ctx.Value(garage.ContextAccessToken))
}

func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"host": {
				Type:     schema.TypeString,
				Optional: true,
				// e.g. GARAGE_HOST="garage.example.com:3900" or "https://garage.example.com:3900"
				DefaultFunc: schema.EnvDefaultFunc("GARAGE_HOST", nil),
			},
			"scheme": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("GARAGE_SCHEME", "https"),
				ValidateFunc: func(v interface{}, k string) (ws []string, es []error) {
					s := v.(string)
					if s != "http" && s != "https" {
						es = append(es, fmt.Errorf("%q must be one of [http https], got %q", k, s))
					}
					return
				},
			},
			"token": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("GARAGE_TOKEN", nil),
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"garage_bucket":       resourceBucket(),
			"garage_bucket_alias": resourceBucketAlias(),
			"garage_key":          resourceKey(),
		},
		DataSourcesMap:       map[string]*schema.Resource{},
		ConfigureContextFunc: providerConfigure,
	}
}

func providerConfigure(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
	rawHost := d.Get("host").(string)
	scheme := d.Get("scheme").(string)
	token := d.Get("token").(string)

	var diags diag.Diagnostics

	if rawHost == "" || token == "" {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Unable to configure provider",
			Detail:   "Both 'host' and 'token' must be set (or provided via GARAGE_HOST / GARAGE_TOKEN).",
		})
		return nil, diags
	}

	host, err := sanitizeHost(rawHost)
	if err != nil {
		return nil, diag.FromErr(err)
	}

	configuration := garage.NewConfiguration()
	configuration.Host = host     // hostname[:port]
	configuration.Scheme = scheme // http or https
	configuration.UserAgent = "terraform-provider-garage/0.1.0"

	client := garage.NewAPIClient(configuration)

	// Set Bearer token on context (the SDK reads it from here)
	ctx = context.WithValue(ctx, garage.ContextAccessToken, token)

	tflog.Debug(ctx, "Configured Garage client",
		map[string]interface{}{"host": host, "scheme": scheme})

	// TODO: In a later step we’ll add a lightweight call here to assert Garage >= 2.0.
	// Example idea (we’ll wire it when we know the exact SDK method):
	// if err := ensureV2(ctx, client); err != nil { return nil, diag.FromErr(err) }

	return &garageProvider{
		client: client,
		ctx:    ctx,
	}, diags
}

// sanitizeHost accepts either "host:port" or a full URL and returns "host[:port]"
func sanitizeHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("host cannot be empty")
	}

	// If user passed a full URL, parse and extract host:port
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("invalid host URL: %w", err)
		}
		if u.Host == "" {
			return "", fmt.Errorf("missing host in URL %q", raw)
		}
		return u.Host, nil
	}

	// If they gave just host[:port], make sure there is no path
	raw = strings.TrimPrefix(raw, "//")
	raw = strings.TrimSuffix(raw, "/")
	if strings.Contains(raw, "/") {
		return "", fmt.Errorf("host must be hostname[:port] without a path, got %q", raw)
	}
	return raw, nil
}
