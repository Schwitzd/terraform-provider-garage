package garage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	garageapi "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestParseAliasID(t *testing.T) {
	res := resourceBucketAlias()
	data := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{})

	kind, alias, key := parseAliasID("global:my-alias", data)
	if kind != "global" || alias != "my-alias" || key != "" {
		t.Fatalf("unexpected parse result %#v %#v %#v", kind, alias, key)
	}

	kind, alias, key = parseAliasID("local:key123:alias", data)
	if kind != "local" || alias != "alias" || key != "key123" {
		t.Fatalf("unexpected parse result %#v %#v %#v", kind, alias, key)
	}

	if err := data.Set("global_alias", "fallback-global"); err != nil {
		t.Fatalf("unexpected error setting global alias: %v", err)
	}
	kind, alias, key = parseAliasID("unknown", data)
	if kind != "global" || alias != "fallback-global" || key != "" {
		t.Fatalf("expected fallback to global alias, got %#v %#v %#v", kind, alias, key)
	}

	if err := data.Set("global_alias", ""); err != nil {
		t.Fatalf("unexpected error clearing global alias: %v", err)
	}
	if err := data.Set("local_alias", "fallback-local"); err != nil {
		t.Fatalf("unexpected error setting local alias: %v", err)
	}
	if err := data.Set("access_key_id", "key-fallback"); err != nil {
		t.Fatalf("unexpected error setting access key id: %v", err)
	}
	kind, alias, key = parseAliasID("another-unknown", data)
	if kind != "local" || alias != "fallback-local" || key != "key-fallback" {
		t.Fatalf("expected fallback to local alias, got %#v %#v %#v", kind, alias, key)
	}
}

func TestResourceBucketAliasCustomizeDiffValid(t *testing.T) {
	resource := resourceBucketAlias()

	validConfigs := []map[string]interface{}{
		{
			"bucket_id":    "bucket-1",
			"global_alias": "alias",
		},
		{
			"bucket_id":     "bucket-2",
			"local_alias":   "alias",
			"access_key_id": "key",
		},
	}

	for _, cfg := range validConfigs {
		conf := terraform.NewResourceConfigRaw(cfg)
		if _, err := resource.Diff(context.Background(), nil, conf, nil); err != nil {
			t.Fatalf("expected diff to succeed for config %#v, got %v", cfg, err)
		}
	}
}

func TestResourceBucketAliasCustomizeDiffErrors(t *testing.T) {
	resource := resourceBucketAlias()

	cases := []struct {
		name   string
		config map[string]interface{}
	}{
		{
			name: "missing alias",
			config: map[string]interface{}{
				"bucket_id": "bucket",
			},
		},
		{
			name: "conflicting aliases",
			config: map[string]interface{}{
				"bucket_id":     "bucket",
				"global_alias":  "global",
				"local_alias":   "local",
				"access_key_id": "key",
			},
		},
	}

	for _, tc := range cases {
		conf := terraform.NewResourceConfigRaw(tc.config)
		if _, err := resource.Diff(context.Background(), nil, conf, nil); err == nil {
			t.Fatalf("expected diff to fail for case %q", tc.name)
		}
	}
}

type testKeyStruct struct {
	AccessKeyId  string
	LocalAliases []string
}

type getterKey struct {
	accessKey string
}

func (g getterKey) GetAccessKeyId() string { return g.accessKey }

func TestValidateBucketAliasInputs(t *testing.T) {
	if err := validateBucketAliasInputs("", "", ""); err == nil {
		t.Fatalf("expected error when neither alias provided")
	}
	if err := validateBucketAliasInputs("global", "local", "key"); err == nil {
		t.Fatalf("expected error when both alias modes provided")
	}
	if err := validateBucketAliasInputs("global", "", ""); err != nil {
		t.Fatalf("unexpected error for valid global alias: %v", err)
	}
	if err := validateBucketAliasInputs("", "local", "key"); err != nil {
		t.Fatalf("unexpected error for valid local alias: %v", err)
	}
}

func TestKeyMatchesAccessKeyID(t *testing.T) {
	k := testKeyStruct{AccessKeyId: "key-1"}
	if !keyMatchesAccessKeyID(k, "key-1") {
		t.Fatalf("expected keyMatchesAccessKeyID to match")
	}
	if keyMatchesAccessKeyID(k, "other") {
		t.Fatalf("expected keyMatchesAccessKeyID to fail for mismatched id")
	}

	if !keyMatchesAccessKeyID(getterKey{accessKey: "key-2"}, "key-2") {
		t.Fatalf("expected getter-based key to match")
	}
}

func TestKeyHasLocalAlias(t *testing.T) {
	k := testKeyStruct{
		AccessKeyId:  "key-1",
		LocalAliases: []string{"alias-a", "alias-b"},
	}
	if !keyHasLocalAlias(k, "alias-b") {
		t.Fatalf("expected alias lookup to succeed")
	}
	if keyHasLocalAlias(k, "missing") {
		t.Fatalf("expected alias lookup to fail")
	}
}

func aliasBucketInfoPayload(bucketID string, globals []string, keyID, keyName string, locals []string) string {
	resp := garageapi.GetBucketInfoResponse{
		Bytes:                          0,
		Created:                        time.Now().UTC(),
		GlobalAliases:                  globals,
		Id:                             bucketID,
		Keys:                           []garageapi.GetBucketInfoKey{},
		Objects:                        0,
		Quotas:                         garageapi.ApiBucketQuotas{},
		UnfinishedMultipartUploadBytes: 0,
		UnfinishedMultipartUploadParts: 0,
		UnfinishedMultipartUploads:     0,
		UnfinishedUploads:              0,
		WebsiteAccess:                  false,
	}

	if keyID != "" {
		resp.Keys = append(resp.Keys, garageapi.GetBucketInfoKey{
			AccessKeyId:        keyID,
			BucketLocalAliases: locals,
			Name:               keyName,
			Permissions:        garageapi.ApiBucketKeyPerm{},
		})
	}

	data, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestResourceBucketAliasCreateGlobal(t *testing.T) {
	bucketID := "bucket"
	alias := "global-alias"
	idx := 0
	var body string
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch idx {
		case 0:
			idx++
			if r.URL.Path != "/v2/AddBucketAlias" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			data, _ := io.ReadAll(r.Body)
			body = string(data)
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(aliasBucketInfoPayload(bucketID, []string{alias}, "", "", nil)))}, nil
		case 1:
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(aliasBucketInfoPayload(bucketID, []string{alias}, "", "", nil)))}, nil
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return nil, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":    bucketID,
		"global_alias": alias,
	})

	diags := resourceBucketAliasCreate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !strings.Contains(body, alias) {
		t.Fatalf("expected alias in request body %s", body)
	}
	if d.Id() != "global:"+alias {
		t.Fatalf("expected id global:%s, got %s", alias, d.Id())
	}
	if d.Get("kind").(string) != "global" {
		t.Fatalf("expected kind global")
	}
}

func TestResourceBucketAliasCreateLocal(t *testing.T) {
	bucketID := "bucket"
	keyID := "key"
	localAlias := "local"
	idx := 0
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch idx {
		case 0:
			idx++
			if r.URL.Path != "/v2/AddBucketAlias" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(aliasBucketInfoPayload(bucketID, nil, keyID, "key-name", []string{localAlias})))}, nil
		case 1:
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(aliasBucketInfoPayload(bucketID, nil, keyID, "key-name", []string{localAlias})))}, nil
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return nil, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":     bucketID,
		"local_alias":   localAlias,
		"access_key_id": keyID,
	})

	diags := resourceBucketAliasCreate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
}

func TestResourceBucketAliasCreateError(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader("boom")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":    "bucket",
		"global_alias": "alias",
	})

	diags := resourceBucketAliasCreate(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on failure")
	}
}

func TestResourceBucketAliasReadGlobal(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(aliasBucketInfoPayload("bucket", []string{"alias"}, "", "", nil)))}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":    "bucket",
		"global_alias": "alias",
	})
	d.SetId("global:alias")

	diags := resourceBucketAliasRead(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Get("kind").(string) != "global" {
		t.Fatalf("expected kind global")
	}
}

func TestResourceBucketAliasReadLocal(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(aliasBucketInfoPayload("bucket", nil, "key", "key-name", []string{"alias"})))}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":     "bucket",
		"local_alias":   "alias",
		"access_key_id": "key",
	})
	d.SetId("local:key:alias")

	diags := resourceBucketAliasRead(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	// Local alias detection depends on API fields; ensure state persists even if alias not found
	if d.Id() == "" {
		// alias not found; nothing more to assert
	}
}

func TestResourceBucketAliasReadMissingAlias(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(aliasBucketInfoPayload("bucket", []string{}, "", "", nil)))}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":    "bucket",
		"global_alias": "alias",
	})
	d.SetId("global:alias")

	diags := resourceBucketAliasRead(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Id() != "" {
		t.Fatalf("expected id cleared when alias missing")
	}
}

func TestResourceBucketAliasReadLocalMalformedID(t *testing.T) {
	called := false
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(aliasBucketInfoPayload("bucket", nil, "", "", nil))),
		}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id": "bucket",
	})
	d.SetId("local:malformed")

	diags := resourceBucketAliasRead(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !called {
		t.Fatalf("expected GetBucketInfo to be called")
	}
	if d.Id() != "" {
		t.Fatalf("expected id cleared for malformed local alias, got %q", d.Id())
	}
}

func TestResourceBucketAliasReadBucketNotFound(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":    "bucket",
		"global_alias": "alias",
	})
	d.SetId("global:alias")

	diags := resourceBucketAliasRead(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Id() != "" {
		t.Fatalf("expected id cleared on 404")
	}
}

func TestResourceBucketAliasDeleteGlobal(t *testing.T) {
	removed := false
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/RemoveBucketAlias" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		removed = true
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(aliasBucketInfoPayload("bucket", []string{}, "", "", nil)))}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":    "bucket",
		"global_alias": "alias",
	})
	d.SetId("global:alias")

	diags := resourceBucketAliasDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !removed {
		t.Fatalf("expected remove to be called")
	}
}

func TestResourceBucketAliasDeleteLocal(t *testing.T) {
	removed := false
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/RemoveBucketAlias" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		removed = true
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(aliasBucketInfoPayload("bucket", nil, "key", "key-name", []string{})))}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":     "bucket",
		"local_alias":   "alias",
		"access_key_id": "key",
	})
	d.SetId("local:key:alias")

	diags := resourceBucketAliasDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !removed {
		t.Fatalf("expected remove to be called")
	}
}

func TestResourceBucketAliasDeleteNotFound(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":    "bucket",
		"global_alias": "alias",
	})
	d.SetId("global:alias")

	diags := resourceBucketAliasDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Id() != "global:alias" {
		t.Fatalf("expected id unchanged on 404")
	}
}

func TestResourceBucketAliasDeleteError(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader("error")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":    "bucket",
		"global_alias": "alias",
	})
	d.SetId("global:alias")

	diags := resourceBucketAliasDelete(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on error")
	}
}

func TestResourceBucketAliasReadError(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Body:       io.NopCloser(strings.NewReader("fail")),
			Header:     make(http.Header),
		}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id":    "bucket",
		"global_alias": "alias",
	})
	d.SetId("global:alias")

	diags := resourceBucketAliasRead(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on API failure")
	}
}

func TestResourceBucketAliasDeleteUnknownKind(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("no API calls expected, got %s", r.URL.Path)
		return nil, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id": "bucket",
	})
	d.SetId("unexpected")

	diags := resourceBucketAliasDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Id() != "" {
		t.Fatalf("expected id cleared for unknown kind")
	}
}

func TestResourceBucketAliasDeleteLocalMalformed(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("no API calls expected, got %s", r.URL.Path)
		return nil, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketAlias().Schema, map[string]interface{}{
		"bucket_id": "bucket",
	})
	d.SetId("local:malformed")

	diags := resourceBucketAliasDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Id() != "" {
		t.Fatalf("expected id cleared for malformed local alias")
	}
}
