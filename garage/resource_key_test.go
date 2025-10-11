package garage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	garageapi "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestBuildUpdateKeyRequestBodyValid(t *testing.T) {
	res := resourceKey()
	data := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{
		"name":       "test",
		"expiration": "2030-01-01T00:00:00Z",
		"permissions": []interface{}{
			map[string]interface{}{
				"read":  true,
				"write": true,
				"admin": false,
			},
		},
	})

	body, diags := buildUpdateKeyRequestBody(data)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if body == nil {
		t.Fatalf("expected body to be returned")
	}
	if !body.Name.IsSet() {
		t.Fatalf("expected name to be set on request body")
	}
	if !body.Expiration.IsSet() {
		t.Fatalf("expected expiration to be set on request body")
	}
}

func TestBuildUpdateKeyRequestBodyInvalidExpiration(t *testing.T) {
	res := resourceKey()
	data := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{
		"expiration": "invalid",
	})

	body, diags := buildUpdateKeyRequestBody(data)
	if body != nil {
		t.Fatalf("expected nil body on invalid expiration")
	}
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics for invalid expiration")
	}
}

func TestSafeGetStringPtr(t *testing.T) {
	value := "hello"
	if safeGetStringPtr(&value, true) != "hello" {
		t.Fatalf("expected helper to dereference pointer")
	}
	if safeGetStringPtr(nil, true) != "" {
		t.Fatalf("expected helper to handle nil pointer")
	}
	if safeGetStringPtr(&value, false) != "" {
		t.Fatalf("expected helper to respect ok=false")
	}
}

type stringHolder struct {
	Name string
}

func TestSetStringFieldOrSetter(t *testing.T) {
	holder := &stringHolder{}
	setStringFieldOrSetter(holder, "Name", "value")
	if holder.Name != "value" {
		t.Fatalf("expected Name to be set via setter helper")
	}
}

type boolStruct struct {
	Flag bool
}

type boolGetter struct {
	flag bool
}

func (b *boolGetter) GetFlag() bool { return b.flag }

type timeSetterHolder struct {
	called bool
}

func (h *timeSetterHolder) SetExpiration(t time.Time) {
	h.called = true
}

type timeUnsetHolder struct {
	unsets     int
	Expiration time.Time
}

func (h *timeUnsetHolder) UnsetExpiration() {
	h.unsets++
}

type timeFieldHolder struct {
	Expiration time.Time
}

type structSetterHolder struct {
	config map[string]string
}

func (h *structSetterHolder) SetConfig(v map[string]string) {
	h.config = v
}

type structSetterConvertible struct {
	value float64
}

func (h *structSetterConvertible) SetValue(v float64) {
	h.value = v
}

type structFieldAssignable struct {
	Name string
}

type structFieldConvertible struct {
	Rate float64
}

type boolSetter struct {
	flag bool
}

func (b *boolSetter) SetFlag(v bool) {
	b.flag = v
}

func TestGetBoolFieldOrGetter(t *testing.T) {
	if !getBoolFieldOrGetter(&boolStruct{Flag: true}, "Flag") {
		t.Fatalf("expected bool field to be read")
	}
	bg := &boolGetter{flag: true}
	if !getBoolFieldOrGetter(bg, "Flag") {
		t.Fatalf("expected getter to be invoked")
	}
	if getBoolFieldOrGetter(struct{}{}, "Flag") {
		t.Fatalf("expected missing field to return false")
	}
}

func TestSetTimeFieldOrSetterUsesSetter(t *testing.T) {
	h := &timeSetterHolder{}
	setTimeFieldOrSetter(h, "Expiration", time.Now())
	if !h.called {
		t.Fatalf("expected SetExpiration to be called")
	}
}

func TestSetTimeFieldOrSetterUsesUnsetAndField(t *testing.T) {
	h := &timeUnsetHolder{}
	value := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	setTimeFieldOrSetter(h, "Expiration", value)
	if h.unsets != 1 {
		t.Fatalf("expected UnsetExpiration to be called once, got %d", h.unsets)
	}
	if !h.Expiration.Equal(value) {
		t.Fatalf("expected expiration field to be set, got %v", h.Expiration)
	}
}

func TestSetTimeFieldOrSetterStructField(t *testing.T) {
	var h timeFieldHolder
	value := time.Date(2030, 5, 2, 12, 0, 0, 0, time.UTC)
	setTimeFieldOrSetter(&h, "Expiration", value)
	if !h.Expiration.Equal(value) {
		t.Fatalf("expected expiration field to be set, got %v", h.Expiration)
	}
}

func TestSetStructFieldOrSetterSetterAssignable(t *testing.T) {
	h := &structSetterHolder{}
	val := map[string]string{"a": "b"}
	setStructFieldOrSetter(h, "Config", val)
	if h.config["a"] != "b" {
		t.Fatalf("expected setter to assign map, got %#v", h.config)
	}
}

func TestSetStructFieldOrSetterSetterConvertible(t *testing.T) {
	h := &structSetterConvertible{}
	setStructFieldOrSetter(h, "Value", 42)
	if h.value != 42 {
		t.Fatalf("expected setter to convert value, got %v", h.value)
	}
}

func TestSetStructFieldOrSetterFieldAssignable(t *testing.T) {
	var h structFieldAssignable
	setStructFieldOrSetter(&h, "Name", "john")
	if h.Name != "john" {
		t.Fatalf("expected field to be assigned, got %q", h.Name)
	}
}

func TestSetStructFieldOrSetterFieldConvertible(t *testing.T) {
	var h structFieldConvertible
	setStructFieldOrSetter(&h, "Rate", 3)
	if h.Rate != 3 {
		t.Fatalf("expected field to convert value, got %v", h.Rate)
	}
}

func TestSetBoolFieldOrSetterSetter(t *testing.T) {
	h := &boolSetter{}
	setBoolFieldOrSetter(h, "Flag", true)
	if !h.flag {
		t.Fatalf("expected setter to set flag true")
	}
}

func TestSetBoolFieldOrSetterField(t *testing.T) {
	var h boolStruct
	setBoolFieldOrSetter(&h, "Flag", true)
	if !h.Flag {
		t.Fatalf("expected field to be set true")
	}
}

func TestReflectKeyPerm(t *testing.T) {
	var kp garageapi.KeyPerm
	read, write, admin := reflectKeyPerm(kp)
	if read || write || admin {
		t.Fatalf("expected zero value key perm to report all false")
	}
}

func TestFlattenKeyInfo(t *testing.T) {
	k := garageapi.NewGetKeyInfoResponse("id", nil, true, "name", garageapi.KeyPerm{})
	now := time.Now().UTC().Truncate(time.Second)
	k.SetCreated(now)

	perms := garageapi.KeyPerm{}
	perms.SetCreateBucket(true)
	k.SetPermissions(perms)

	res := resourceKey()
	d := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{})

	flattenKeyInfo(k, d)

	if v := d.Get("expired").(bool); !v {
		t.Fatalf("expected expired to be true")
	}
	if v := d.Get("created").(string); v != now.Format(time.RFC3339) {
		t.Fatalf("expected created timestamp, got %q", v)
	}
	permsList := d.Get("effective_permissions").([]interface{})
	if len(permsList) != 1 {
		t.Fatalf("expected one permission entry, got %d", len(permsList))
	}
	perm := permsList[0].(map[string]interface{})
	if perm["read"].(bool) || perm["write"].(bool) || perm["admin"].(bool) {
		t.Fatalf("expected reflected perms to be false, got %#v", perm)
	}
}

type keyRoundTripper func(*http.Request) (*http.Response, error)

func (f keyRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newTestProvider(handler keyRoundTripper) *garageProvider {
	cfg := garageapi.NewConfiguration()
	cfg.Servers = garageapi.ServerConfigurations{{URL: "https://example.com"}}
	cfg.HTTPClient = &http.Client{Transport: handler}

	return &garageProvider{
		client: garageapi.NewAPIClient(cfg),
		token:  "test-token",
	}
}

func TestResourceKeyDeleteSuccess(t *testing.T) {
	called := false
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.URL.Path != "/v2/DeleteKey" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("missing auth header")
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Status:     "204 No Content",
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{})
	d.SetId("key-id")

	diags := resourceKeyDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if !called {
		t.Fatalf("expected delete endpoint to be called")
	}
}

func TestResourceKeyDeleteNotFound(t *testing.T) {
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{})
	d.SetId("missing")

	diags := resourceKeyDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics on 404, got %#v", diags)
	}
}

func keyResponseJSON(secret string) string {
	json := `{"accessKeyId":"key-123","buckets":[],"expired":false,"name":"key","permissions":{}}`
	if secret != "" {
		json = `{"accessKeyId":"key-123","secretAccessKey":"` + secret + `","buckets":[],"expired":false,"name":"key","permissions":{}}`
	}
	return json
}

func TestResourceKeyCreateSuccess(t *testing.T) {
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/CreateKey" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(keyResponseJSON("secret"))),
		}, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{
		"name": "mykey",
	})

	diags := resourceKeyCreate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if d.Id() != "key-123" {
		t.Fatalf("expected id to be set, got %q", d.Id())
	}
	if d.Get("secret_access_key").(string) != "secret" {
		t.Fatalf("expected secret to be set")
	}
}

func TestResourceKeyCreateError(t *testing.T) {
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Body:       io.NopCloser(strings.NewReader("boom")),
			Header:     make(http.Header),
		}, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{})
	diags := resourceKeyCreate(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on api error")
	}
}

func TestResourceKeyReadSuccess(t *testing.T) {
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/GetKeyInfo" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(keyResponseJSON(""))),
		}, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{})
	d.SetId("key-123")

	diags := resourceKeyRead(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Get("access_key_id").(string) != "key-123" {
		t.Fatalf("expected access key id to be set")
	}
}

func TestResourceKeyReadNotFound(t *testing.T) {
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{})
	d.SetId("key-123")

	idBefore := d.Id()
	diags := resourceKeyRead(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %#v", diags)
	}
	if d.Id() != "" {
		t.Fatalf("expected id to be cleared, remained %q (before %q)", d.Id(), idBefore)
	}
}

func TestResourceKeyReadError(t *testing.T) {
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Body:       io.NopCloser(strings.NewReader("error")),
			Header:     make(http.Header),
		}, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{})
	d.SetId("key-123")

	diags := resourceKeyRead(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on api error")
	}
}

func TestResourceKeyUpdateNoChange(t *testing.T) {
	readCalled := false
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/GetKeyInfo" {
			t.Fatalf("expected read to be called, got %s", r.URL.Path)
		}
		readCalled = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(keyResponseJSON(""))),
		}, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{})
	d.SetId("key-123")

	diags := resourceKeyUpdate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !readCalled {
		t.Fatalf("expected read to be invoked when no change")
	}
}

func TestResourceKeyUpdateChange(t *testing.T) {
	updateCalled := false
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/UpdateKey":
			updateCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(keyResponseJSON("secret"))),
			}, nil
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return nil, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{
		"name": "old",
	})
	d.SetId("key-123")
	if err := d.Set("name", "new"); err != nil {
		t.Fatalf("set name: %v", err)
	}

	diags := resourceKeyUpdate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !updateCalled {
		t.Fatalf("expected update api to be called")
	}
	if d.Get("secret_access_key").(string) != "secret" {
		t.Fatalf("expected secret to be set from update response")
	}
}

func TestResourceKeyUpdateBuildError(t *testing.T) {
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("api should not be called when build errors")
		return nil, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{
		"expiration": "2020-01-01T00:00:00Z",
	})
	d.SetId("key-123")
	if err := d.Set("expiration", "invalid"); err != nil {
		t.Fatalf("set: %v", err)
	}

	diags := resourceKeyUpdate(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics when build fails")
	}
}

func TestResourceKeyUpdateError(t *testing.T) {
	p := newTestProvider(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/UpdateKey" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Body:       io.NopCloser(strings.NewReader("update failed")),
			Header:     make(http.Header),
		}, nil
	})

	d := schema.TestResourceDataRaw(t, resourceKey().Schema, map[string]interface{}{
		"name": "old",
	})
	d.SetId("key-123")
	if err := d.Set("name", "new"); err != nil {
		t.Fatalf("set: %v", err)
	}

	diags := resourceKeyUpdate(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on update error")
	}
}
