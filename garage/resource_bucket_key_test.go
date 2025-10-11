package garage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	garageapi "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestBucketKeyPermissionsAny(t *testing.T) {
	perms := bucketKeyPermissions{}
	if perms.any() {
		t.Fatalf("expected empty permissions to report false")
	}
	perms.Read = true
	if !perms.any() {
		t.Fatalf("expected read=true to report true")
	}
}

func TestHasAnyBucketKeyPerm(t *testing.T) {
	if hasAnyBucketKeyPerm(nil) {
		t.Fatalf("nil permissions should return false")
	}
	perm := garageapi.NewApiBucketKeyPerm()
	if hasAnyBucketKeyPerm(perm) {
		t.Fatalf("empty permission struct should return false")
	}
	perm.SetWrite(true)
	if !hasAnyBucketKeyPerm(perm) {
		t.Fatalf("write=true should return true")
	}
}

func TestDesiredBucketKeyPermissions(t *testing.T) {
	res := resourceBucketKey()
	data := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{
		"bucket_id":     "bucket-1",
		"access_key_id": "key-1",
		"read":          true,
		"write":         false,
		"owner":         true,
	})

	perms := desiredBucketKeyPermissions(data)
	if !perms.Read || perms.Write || !perms.Owner {
		t.Fatalf("unexpected permissions %#v", perms)
	}
}

func TestResourceBucketKeyCustomizeDiffValid(t *testing.T) {
	resource := resourceBucketKey()
	conf := terraform.NewResourceConfigRaw(map[string]interface{}{
		"bucket_id":     "bucket",
		"access_key_id": "key",
		"read":          true,
	})
	if _, err := resource.Diff(context.Background(), nil, conf, nil); err != nil {
		t.Fatalf("expected diff to succeed, got %v", err)
	}
}

func TestResourceBucketKeyCustomizeDiffError(t *testing.T) {
	resource := resourceBucketKey()
	conf := terraform.NewResourceConfigRaw(map[string]interface{}{
		"bucket_id":     "bucket",
		"access_key_id": "key",
	})
	if _, err := resource.Diff(context.Background(), nil, conf, nil); err == nil {
		t.Fatalf("expected diff to fail when permissions absent")
	}
}

func bucketInfoPayload(bucketID, keyID, keyName string, perms bucketKeyPermissions) string {
	perm := garageapi.ApiBucketKeyPerm{}
	if perms.Read {
		perm.SetRead(true)
	}
	if perms.Write {
		perm.SetWrite(true)
	}
	if perms.Owner {
		perm.SetOwner(true)
	}

	resp := garageapi.GetBucketInfoResponse{
		Bytes:                          0,
		Created:                        time.Now().UTC(),
		GlobalAliases:                  []string{},
		Id:                             bucketID,
		Keys:                           []garageapi.GetBucketInfoKey{{AccessKeyId: keyID, BucketLocalAliases: []string{}, Name: keyName, Permissions: perm}},
		Objects:                        0,
		Quotas:                         garageapi.ApiBucketQuotas{},
		UnfinishedMultipartUploadBytes: 0,
		UnfinishedMultipartUploadParts: 0,
		UnfinishedMultipartUploads:     0,
		UnfinishedUploads:              0,
		WebsiteAccess:                  false,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func setResourceDataDiff(d *schema.ResourceData, attrs map[string]*terraform.ResourceAttrDiff) {
	v := reflect.ValueOf(d).Elem()
	diffField := v.FieldByName("diff")
	ptr := (**terraform.InstanceDiff)(unsafe.Pointer(diffField.UnsafeAddr()))
	if *ptr == nil {
		*ptr = &terraform.InstanceDiff{Attributes: attrs}
		return
	}
	(*ptr).Attributes = attrs
}

func prepareBucketKeyData(t *testing.T, bucketID, keyID string, oldPerms, newPerms bucketKeyPermissions) *schema.ResourceData {
	raw := map[string]interface{}{
		"bucket_id":     bucketID,
		"access_key_id": keyID,
		"read":          newPerms.Read,
		"write":         newPerms.Write,
		"owner":         newPerms.Owner,
	}
	d := schema.TestResourceDataRaw(t, resourceBucketKey().Schema, raw)
	d.SetId(bucketID + ":" + keyID)

	if d.State() != nil {
		if d.State().Attributes == nil {
			d.State().Attributes = map[string]string{}
		}
		d.State().Attributes["bucket_id"] = bucketID
		d.State().Attributes["access_key_id"] = keyID
		d.State().Attributes["read"] = boolString(oldPerms.Read)
		d.State().Attributes["write"] = boolString(oldPerms.Write)
		d.State().Attributes["owner"] = boolString(oldPerms.Owner)
	}

	_ = d.Set("read", newPerms.Read)
	_ = d.Set("write", newPerms.Write)
	_ = d.Set("owner", newPerms.Owner)

	attrs := map[string]*terraform.ResourceAttrDiff{}
	if oldPerms.Read != newPerms.Read {
		attrs["read"] = &terraform.ResourceAttrDiff{Old: boolString(oldPerms.Read), New: boolString(newPerms.Read)}
	}
	if oldPerms.Write != newPerms.Write {
		attrs["write"] = &terraform.ResourceAttrDiff{Old: boolString(oldPerms.Write), New: boolString(newPerms.Write)}
	}
	if oldPerms.Owner != newPerms.Owner {
		attrs["owner"] = &terraform.ResourceAttrDiff{Old: boolString(oldPerms.Owner), New: boolString(newPerms.Owner)}
	}
	if len(attrs) > 0 {
		setResourceDataDiff(d, attrs)
	}

	return d
}

func TestFetchBucketKeyStateFound(t *testing.T) {
	bucketID := "bucket"
	keyID := "key"
	name := "name"

	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/GetBucketInfo" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, name, bucketKeyPermissions{Read: true, Write: true}))),
		}, nil
	}))

	state, keyName, found, diags := fetchBucketKeyState(context.Background(), p, bucketID, keyID)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !found {
		t.Fatalf("expected key to be found")
	}
	if keyName != name {
		t.Fatalf("expected key name %q got %q", name, keyName)
	}
	if !state.Read || !state.Write || state.Owner {
		t.Fatalf("unexpected permissions %+v", state)
	}
}

func TestFetchBucketKeyStateNotFound(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(bucketInfoPayload("bucket", "other", "name", bucketKeyPermissions{}))),
		}, nil
	}))
	state, name, found, diags := fetchBucketKeyState(context.Background(), p, "bucket", "key")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if found || state.any() || name != "" {
		t.Fatalf("expected not found state")
	}
}

func TestApplyBucketKeyAllowNoFlags(t *testing.T) {
	diags := applyBucketKeyAllow(context.Background(), newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("should not call api when no flags set")
		return nil, nil
	})), "bucket", "key", garageapi.NewApiBucketKeyPerm())
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics")
	}
}

func TestApplyBucketKeyDenyNoFlags(t *testing.T) {
	diags := applyBucketKeyDeny(context.Background(), newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("should not call api when no flags set")
		return nil, nil
	})), "bucket", "key", garageapi.NewApiBucketKeyPerm())
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics")
	}
}

func TestEnsureBucketKeyPermissionsAllow(t *testing.T) {
	bucketID, keyID := "bucket", "key"
	idx := 0
	var body string
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch idx {
		case 0:
			idx++
			if r.URL.Path != "/v2/GetBucketInfo" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, "name", bucketKeyPermissions{}))),
			}, nil
		case 1:
			idx++
			if r.URL.Path != "/v2/AllowBucketKey" {
				t.Fatalf("expected allow call, got %s", r.URL.Path)
			}
			data, _ := io.ReadAll(r.Body)
			r.Body.Close()
			body = string(data)
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, "name", bucketKeyPermissions{Read: true}))),
			}, nil
		default:
			t.Fatalf("unexpected extra request %s", r.URL.Path)
		}
		return nil, nil
	}))

	desired := bucketKeyPermissions{Read: true}
	diags := ensureBucketKeyPermissions(context.Background(), p, bucketID, keyID, desired)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !strings.Contains(body, `"read":true`) {
		t.Fatalf("expected allow request to contain read=true: %s", body)
	}
}

func TestEnsureBucketKeyPermissionsDeny(t *testing.T) {
	bucketID, keyID := "bucket", "key"
	idx := 0
	var body string
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch idx {
		case 0:
			idx++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, "name", bucketKeyPermissions{Read: true}))),
			}, nil
		case 1:
			idx++
			if r.URL.Path != "/v2/DenyBucketKey" {
				t.Fatalf("expected deny call, got %s", r.URL.Path)
			}
			data, _ := io.ReadAll(r.Body)
			r.Body.Close()
			body = string(data)
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, "name", bucketKeyPermissions{}))),
			}, nil
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return nil, nil
	}))

	desired := bucketKeyPermissions{}
	diags := ensureBucketKeyPermissions(context.Background(), p, bucketID, keyID, desired)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !strings.Contains(body, `"read":true`) {
		t.Fatalf("expected deny to include read flag: %s", body)
	}
}

func TestEnsureBucketKeyPermissionsError(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v2/GetBucketInfo" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(bucketInfoPayload("bucket", "key", "name", bucketKeyPermissions{}))),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Body:       io.NopCloser(strings.NewReader("fail")),
			Header:     make(http.Header),
		}, nil
	}))

	desired := bucketKeyPermissions{Read: true}
	diags := ensureBucketKeyPermissions(context.Background(), p, "bucket", "key", desired)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics when allow fails")
	}
}

func TestResourceBucketKeyCreateSuccess(t *testing.T) {
	bucketID, keyID := "bucket", "key"
	idx := 0
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch idx {
		case 0:
			idx++
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, "name", bucketKeyPermissions{})))}, nil
		case 1:
			idx++
			if r.URL.Path != "/v2/AllowBucketKey" {
				t.Fatalf("expected allow call got %s", r.URL.Path)
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, "name", bucketKeyPermissions{Read: true})))}, nil
		case 2:
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, "name", bucketKeyPermissions{Read: true})))}, nil
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return nil, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketKey().Schema, map[string]interface{}{
		"bucket_id":     bucketID,
		"access_key_id": keyID,
		"read":          true,
	})

	diags := resourceBucketKeyCreate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Id() != bucketID+":"+keyID {
		t.Fatalf("expected id to be set got %q", d.Id())
	}
	if !d.Get("read").(bool) || d.Get("write").(bool) {
		t.Fatalf("unexpected state read=%v write=%v", d.Get("read"), d.Get("write"))
	}
}

func TestResourceBucketKeyCreateError(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v2/GetBucketInfo" {
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload("bucket", "key", "name", bucketKeyPermissions{})))}, nil
		}
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader("boom")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketKey().Schema, map[string]interface{}{
		"bucket_id":     "bucket",
		"access_key_id": "key",
		"read":          true,
	})
	diags := resourceBucketKeyCreate(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on allow failure")
	}
}

func TestResourceBucketKeyReadSuccess(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload("bucket", "key", "name", bucketKeyPermissions{Write: true})))}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketKey().Schema, map[string]interface{}{
		"bucket_id":     "bucket",
		"access_key_id": "key",
	})
	d.SetId("bucket:key")

	diags := resourceBucketKeyRead(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !d.Get("write").(bool) || d.Get("key_name").(string) != "name" {
		t.Fatalf("expected state to be populated")
	}
}

func TestResourceBucketKeyReadNotFound(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketKey().Schema, map[string]interface{}{
		"bucket_id":     "bucket",
		"access_key_id": "key",
	})
	d.SetId("bucket:key")

	diags := resourceBucketKeyRead(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Id() != "" {
		t.Fatalf("expected id to be cleared")
	}
}

func TestResourceBucketKeyReadError(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader("error")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketKey().Schema, map[string]interface{}{
		"bucket_id":     "bucket",
		"access_key_id": "key",
	})
	d.SetId("bucket:key")

	diags := resourceBucketKeyRead(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on error")
	}
}

func TestResourceBucketKeyUpdateNoChange(t *testing.T) {
	readCalled := false
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		readCalled = true
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload("bucket", "key", "name", bucketKeyPermissions{Read: true})))}, nil
	}))

	d := prepareBucketKeyData(t, "bucket", "key", bucketKeyPermissions{Read: true}, bucketKeyPermissions{Read: true})

	diags := resourceBucketKeyUpdate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !readCalled {
		t.Fatalf("expected read fallback to be invoked")
	}
}

func TestResourceBucketKeyUpdateChange(t *testing.T) {
	bucketID, keyID := "bucket", "key"
	idx := 0
	var allowBody string
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch idx {
		case 0:
			idx++
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, "name", bucketKeyPermissions{})))}, nil
		case 1:
			idx++
			if r.URL.Path != "/v2/AllowBucketKey" {
				t.Fatalf("expected allow call got %s", r.URL.Path)
			}
			data, _ := io.ReadAll(r.Body)
			r.Body.Close()
			allowBody = string(data)
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, "name", bucketKeyPermissions{Owner: true})))}, nil
		case 2:
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload(bucketID, keyID, "name", bucketKeyPermissions{Owner: true})))}, nil
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return nil, nil
	}))

	d := prepareBucketKeyData(t, bucketID, keyID, bucketKeyPermissions{Owner: false}, bucketKeyPermissions{Owner: true})

	diags := resourceBucketKeyUpdate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if !d.Get("owner").(bool) {
		t.Fatalf("expected owner to be true (idx=%d body=%s)", idx, allowBody)
	}
}

func TestResourceBucketKeyUpdateError(t *testing.T) {
	allowCalled := false
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v2/GetBucketInfo" {
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload("bucket", "key", "name", bucketKeyPermissions{})))}, nil
		}
		allowCalled = true
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader("error")), Header: make(http.Header)}, nil
	}))

	d := prepareBucketKeyData(t, "bucket", "key", bucketKeyPermissions{Write: false}, bucketKeyPermissions{Write: true})

	diags := resourceBucketKeyUpdate(context.Background(), d, p)
	if len(diags) == 0 || !allowCalled {
		t.Fatalf("expected diagnostics on update error (allowCalled=%v)", allowCalled)
	}
}

func TestResourceBucketKeyDeleteSuccess(t *testing.T) {
	idx := 0
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if idx == 0 {
			idx++
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload("bucket", "key", "name", bucketKeyPermissions{Read: true})))}, nil
		}
		if r.URL.Path != "/v2/DenyBucketKey" {
			t.Fatalf("expected deny call, got %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload("bucket", "key", "name", bucketKeyPermissions{})))}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketKey().Schema, map[string]interface{}{
		"bucket_id":     "bucket",
		"access_key_id": "key",
	})
	d.SetId("bucket:key")

	diags := resourceBucketKeyDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Id() != "" {
		t.Fatalf("expected id to be cleared")
	}
}

func TestResourceBucketKeyDeleteNotFound(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketKey().Schema, map[string]interface{}{
		"bucket_id":     "bucket",
		"access_key_id": "key",
	})
	d.SetId("bucket:key")

	diags := resourceBucketKeyDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Id() != "" {
		t.Fatalf("expected id cleared")
	}
}

func TestResourceBucketKeyDeleteError(t *testing.T) {
	idx := 0
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if idx == 0 {
			idx++
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoPayload("bucket", "key", "name", bucketKeyPermissions{Write: true})))}, nil
		}
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader("error")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucketKey().Schema, map[string]interface{}{
		"bucket_id":     "bucket",
		"access_key_id": "key",
	})
	d.SetId("bucket:key")

	diags := resourceBucketKeyDelete(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on deny failure")
	}
}
