package garage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/Masterminds/semver/v3"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// providerVersion can be injected at build time with:
//
//	-ldflags "-X your/module/path/garage.providerVersion=v0.1.0"
var providerVersion = "dev"

// garageProvider holds shared clients and auth material
type garageProvider struct {
	client     *garage.APIClient
	token      string
	httpClient *http.Client
}

// withToken attaches the bearer token to a context
func (p *garageProvider) withToken(ctx context.Context) context.Context {
	return context.WithValue(ctx, garage.ContextAccessToken, p.token)
}

// Provider defines the Terraform provider schema and resources
func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"host": {
				Type:     schema.TypeString,
				Optional: true,
				// Accepts "garage.example.com:3903" or a full URL like "https://garage.example.com:3903"
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
	hostRaw := d.Get("host").(string)
	scheme := d.Get("scheme").(string)
	token := d.Get("token").(string)

	if hostRaw == "" || token == "" {
		return nil, diag.Diagnostics{{
			Severity: diag.Error,
			Summary:  "unable to configure provider",
			Detail:   "both 'host' and 'token' must be set or provided via GARAGE_HOST and GARAGE_TOKEN",
		}}
	}

	host, inferredScheme, err := sanitizeHost(hostRaw)
	if err != nil {
		return nil, diag.FromErr(err)
	}
	if inferredScheme != "" {
		scheme = inferredScheme
	}

	cfg := garage.NewConfiguration()
	cfg.Host = host
	cfg.Scheme = scheme
	cfg.UserAgent = fmt.Sprintf("terraform-provider-garage/%s", providerVersion)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	cfg.HTTPClient = httpClient

	client := garage.NewAPIClient(cfg)

	// temporary context with token only for detection during configure
	ctxTok := context.WithValue(ctx, garage.ContextAccessToken, token)

	// detect and enforce minimum supported version
	ver, src, derr := detectGarageVersion(ctxTok, client, httpClient, scheme, host, token)
	if derr != nil {
		return nil, diag.FromErr(derr)
	}
	if err := enforceV2(ver); err != nil {
		return nil, diag.FromErr(err)
	}

	tflog.Debug(ctxTok, "garage version ok", map[string]interface{}{
		"version": ver.Original(),
		"source":  src,
		"host":    host,
		"scheme":  scheme,
	})

	return &garageProvider{
		client:     client,
		token:      token,
		httpClient: httpClient,
	}, nil
}

// sanitizeHost accepts either "host:port" or a full URL and returns "host[:port]" and scheme
func sanitizeHost(raw string) (host string, scheme string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("host cannot be empty")
	}

	// full URL form
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", fmt.Errorf("invalid host url: %w", err)
		}
		if u.Host == "" {
			return "", "", fmt.Errorf("missing host in url %q", raw)
		}
		if u.Path != "" && u.Path != "/" {
			return "", "", fmt.Errorf("host url must not contain a path, got %q", raw)
		}
		return u.Host, u.Scheme, nil
	}

	// host[:port] form
	raw = strings.TrimPrefix(raw, "//")
	raw = strings.TrimSuffix(raw, "/")
	if strings.Contains(raw, "/") {
		return "", "", fmt.Errorf("host must be hostname[:port] without a path, got %q", raw)
	}
	return raw, "", nil
}

// detectGarageVersion tries v2 (SDK) first, then v1 (/v1/status via raw HTTP)
// returns detected version and source ("v2" | "v1")
func detectGarageVersion(
	ctx context.Context,
	client *garage.APIClient,
	httpClient *http.Client,
	scheme, host, token string,
) (*semver.Version, string, error) {
	// v2 via SDK
	status, resp, err := client.ClusterAPI.GetClusterStatus(ctx).Execute()
	if err == nil && status != nil && len(status.Nodes) > 0 {
		v, serr := minClusterSemverFromV2(status)
		if serr == nil {
			return v, "v2", nil
		}
		return nil, "", fmt.Errorf("v2 payload invalid: %w", serr)
	}
	v2Err := enrichV2HTTP(err, resp)

	// v1 via raw HTTP
	v1Str, v1Err := probeV1Version(ctx, httpClient, scheme, host, token)
	if v1Err == nil {
		norm, nerr := normalizeVersion(v1Str)
		if nerr != nil {
			return nil, "", fmt.Errorf("v1 status returned bad version: %w", nerr)
		}
		v, _ := semver.NewVersion(norm)
		return v, "v1", nil
	}

	// both failed
	return nil, "", fmt.Errorf("failed to determine garage version; v2: %v; v1: %v", v2Err, v1Err)
}

// enforceV2 ensures detected version >= 2.0.0
func enforceV2(v *semver.Version) error {
	c, _ := semver.NewConstraint(">= 2.0.0")
	if !c.Check(v) {
		return fmt.Errorf("unsupported garage version %s this provider supports only v2+ please upgrade", v.Original())
	}
	return nil
}

// minClusterSemverFromV2 parses the cluster status and returns the minimum node version as semver
func minClusterSemverFromV2(status *garage.GetClusterStatusResponse) (*semver.Version, error) {
	c, _ := semver.NewConstraint(">= 2.0.0")
	var minSeen *semver.Version

	for _, n := range status.Nodes {
		if !n.GarageVersion.IsSet() || n.GarageVersion.Get() == nil {
			return nil, fmt.Errorf("node %s reports no garageVersion", n.Id)
		}
		norm, err := normalizeVersion(*n.GarageVersion.Get())
		if err != nil {
			return nil, fmt.Errorf("node %s has invalid version: %w", n.Id, err)
		}
		v, _ := semver.NewVersion(norm)
		if !c.Check(v) {
			return nil, fmt.Errorf("node %s is on %s this provider supports only v2+ please upgrade", n.Id, v.Original())
		}
		if minSeen == nil || v.LessThan(minSeen) {
			minSeen = v
		}
	}
	return minSeen, nil
}

// probeV1Version calls /v1/status and extracts the GarageVersion
func probeV1Version(ctx context.Context, httpClient *http.Client, scheme, host, token string) (string, error) {
	urlStr := fmt.Sprintf("%s://%s/v1/status", scheme, host)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("GET %s -> %s", urlStr, res.Status)
	}

	var payload struct {
		GarageVersion string `json:"garageVersion"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.GarageVersion == "" {
		return "", fmt.Errorf("no garageVersion in /v1/status response")
	}
	return payload.GarageVersion, nil
}

// normalizeVersion trims whitespace, optional leading 'v', and validates semver
func normalizeVersion(s string) (string, error) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "v"))
	if s == "" {
		return "", fmt.Errorf("empty version string")
	}
	if _, err := semver.NewVersion(s); err != nil {
		return "", fmt.Errorf("invalid semver %q: %w", s, err)
	}
	return s, nil
}

// enrichV2HTTP builds a helpful error string from SDK response context
func enrichV2HTTP(err error, resp *http.Response) error {
	if err == nil {
		return nil
	}
	if resp == nil {
		return err
	}
	var reqURL, respStatus, errBody string
	respStatus = resp.Status
	if resp.Request != nil && resp.Request.URL != nil {
		reqURL = resp.Request.URL.String()
	}
	var apiErr *garage.GenericOpenAPIError
	if errors.As(err, &apiErr) && apiErr.Body() != nil {
		body := string(apiErr.Body())
		if len(body) > 600 {
			body = body[:600] + "…"
		}
		errBody = strings.TrimSpace(body)
	}
	return fmt.Errorf("GET %s -> %s: %v %s", reqURL, respStatus, err, errBody)
}
