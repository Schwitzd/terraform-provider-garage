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

func TestBuildWebsiteAccessRequiresIndex(t *testing.T) {
	res := resourceBucket()
	data := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{
		"website_access_enabled": true,
	})

	wa, diags := buildWebsiteAccess(data)
	if wa != nil {
		t.Fatalf("expected nil website access when missing index document, got %#v", wa)
	}
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics when index document missing")
	}
}

func TestBuildWebsiteAccessDisabled(t *testing.T) {
	res := resourceBucket()
	data := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{
		"website_access_enabled": false,
	})

	wa, diags := buildWebsiteAccess(data)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if wa != nil && wa.Enabled {
		t.Fatalf("expected disabled website access, got %#v", wa)
	}
}

func TestBuildWebsiteAccessEnabled(t *testing.T) {
	res := resourceBucket()
	data := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{
		"website_access_enabled":        true,
		"website_config_index_document": "index.html",
		"website_config_error_document": "error.html",
	})

	wa, diags := buildWebsiteAccess(data)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if wa == nil || !wa.Enabled {
		t.Fatalf("expected enabled website access block, got %#v", wa)
	}
	if idx := wa.IndexDocument.Get(); idx == nil || *idx != "index.html" {
		t.Fatalf("unexpected index document %#v", idx)
	}
	if errDoc := wa.ErrorDocument.Get(); errDoc == nil || *errDoc != "error.html" {
		t.Fatalf("unexpected error document %#v", errDoc)
	}
}

func TestBuildQuotasValidation(t *testing.T) {
	res := resourceBucket()

	// Both values set
	data := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{})
	if err := data.Set("quotas", []interface{}{
		map[string]interface{}{
			"max_size":    10,
			"max_objects": 5,
		},
	}); err != nil {
		t.Fatalf("unexpected error setting quotas: %v", err)
	}
	quotas, diags := buildQuotas(data)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if quotas == nil || !quotas.MaxSize.IsSet() || !quotas.MaxObjects.IsSet() {
		t.Fatalf("expected quotas to be populated, got %#v", quotas)
	}
}

func TestFlattenBucketInfo(t *testing.T) {
	now := time.Now().UTC()
	quotas := garageapi.ApiBucketQuotas{}
	quotas.SetMaxSize(42)
	quotas.SetMaxObjects(7)

	bucket := garageapi.NewGetBucketInfoResponse(
		1234,
		now,
		[]string{"ga"},
		"bucket-id",
		[]garageapi.GetBucketInfoKey{},
		2,
		quotas,
		0, 0, 0, 0,
		true,
	)

	wc := garageapi.NewGetBucketInfoWebsiteResponse("index.html")
	wc.SetErrorDocument("error.html")
	bucket.WebsiteConfig = *garageapi.NewNullableGetBucketInfoWebsiteResponse(wc)

	flat := flattenBucketInfo(bucket)
	if v, ok := flat["website_config_index_document"]; !ok || v.(string) != "index.html" {
		t.Fatalf("expected index document to be flattened, got %#v", v)
	}
	if v, ok := flat["website_config_error_document"]; !ok {
		t.Fatalf("expected error document entry")
	} else {
		ns, ok := v.(garageapi.NullableString)
		if !ok {
			t.Fatalf("expected NullableString, got %#v", v)
		}
		if !ns.IsSet() || ns.Get() == nil || *ns.Get() != "error.html" {
			t.Fatalf("unexpected nullable string content %#v", ns)
		}
	}
	if aliases, ok := flat["global_aliases"]; !ok {
		t.Fatalf("expected global aliases in map")
	} else {
		list, ok := aliases.([]string)
		if !ok || len(list) != 1 || list[0] != "ga" {
			t.Fatalf("unexpected global aliases %#v", aliases)
		}
	}
	quotasList, ok := flat["quotas"].([]interface{})
	if !ok || len(quotasList) != 1 {
		t.Fatalf("expected quotas flattened into list, got %#v", flat["quotas"])
	}
	q := quotasList[0].(map[string]interface{})
	if q["max_size"].(int) != 42 || q["max_objects"].(int) != 7 {
		t.Fatalf("unexpected quotas contents %#v", q)
	}
}

func TestGetOkString(t *testing.T) {
	res := resourceBucket()
	data := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{})

	if val, ok := getOkString(data, "global_alias"); ok || val != "" {
		t.Fatalf("expected empty string when key missing, got %q/%v", val, ok)
	}

	if err := data.Set("global_alias", ""); err != nil {
		t.Fatalf("unexpected error setting empty string: %v", err)
	}
	if val, ok := getOkString(data, "global_alias"); ok || val != "" {
		t.Fatalf("expected empty string to return ok=false, got %q/%v", val, ok)
	}

	if err := data.Set("global_alias", "alias"); err != nil {
		t.Fatalf("unexpected error setting value: %v", err)
	}
	if val, ok := getOkString(data, "global_alias"); !ok || val != "alias" {
		t.Fatalf("expected alias value, got %q/%v", val, ok)
	}
}

func bucketInfoJSON(id string, globals []string, keyCount int) string {
	resp := garageapi.GetBucketInfoResponse{
		Bytes:                          0,
		Created:                        time.Now().UTC(),
		GlobalAliases:                  globals,
		Id:                             id,
		Keys:                           []garageapi.GetBucketInfoKey{},
		Objects:                        0,
		Quotas:                         garageapi.ApiBucketQuotas{},
		UnfinishedMultipartUploadBytes: 0,
		UnfinishedMultipartUploadParts: 0,
		UnfinishedMultipartUploads:     0,
		UnfinishedUploads:              0,
		WebsiteAccess:                  false,
	}
	for i := 0; i < keyCount; i++ {
		resp.Keys = append(resp.Keys, garageapi.GetBucketInfoKey{
			AccessKeyId:        "key",
			BucketLocalAliases: []string{"alias"},
			Name:               "key-name",
			Permissions:        garageapi.ApiBucketKeyPerm{},
		})
	}
	data, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func setResourceDiff(d *schema.ResourceData, attrs map[string]*terraform.ResourceAttrDiff) {
	v := reflect.ValueOf(d).Elem()
	field := v.FieldByName("diff")
	if !field.IsValid() {
		panic("diff field not found")
	}
	ptr := (**terraform.InstanceDiff)(unsafe.Pointer(field.UnsafeAddr()))
	if *ptr == nil {
		*ptr = &terraform.InstanceDiff{Attributes: attrs}
	} else {
		if (*ptr).Attributes == nil {
			(*ptr).Attributes = attrs
		} else {
			for k, v := range attrs {
				(*ptr).Attributes[k] = v
			}
		}
	}
}

func prepareBucketData(t *testing.T, bucketID, oldAlias, newAlias string) *schema.ResourceData {
	raw := map[string]interface{}{
		"global_alias": newAlias,
	}
	d := schema.TestResourceDataRaw(t, resourceBucket().Schema, raw)
	d.SetId(bucketID)
	stateField := reflect.ValueOf(d).Elem().FieldByName("state")
	statePtr := (**terraform.InstanceState)(unsafe.Pointer(stateField.UnsafeAddr()))
	*statePtr = &terraform.InstanceState{
		ID: bucketID,
		Attributes: map[string]string{
			"id":           bucketID,
			"global_alias": oldAlias,
		},
	}
	setResourceDiff(d, map[string]*terraform.ResourceAttrDiff{
		"global_alias": {Old: oldAlias, New: newAlias},
	})
	diffField := reflect.ValueOf(d).Elem().FieldByName("diff")
	diffPtr := *(**terraform.InstanceDiff)(unsafe.Pointer(diffField.UnsafeAddr()))
	if diffPtr == nil {
		t.Fatalf("diff not set")
	}
	if attr, ok := diffPtr.Attributes["global_alias"]; ok {
		if attr.Old != oldAlias || attr.New != newAlias {
			t.Fatalf("diff mismatch old=%s new=%s", attr.Old, attr.New)
		}
	} else {
		t.Fatalf("diff attribute missing")
	}
	rebuildResourceData(d)
	return d
}

func rebuildResourceData(d *schema.ResourceData) {
	v := reflect.ValueOf(d).Elem()
	schemaField := v.FieldByName("schema")
	schemaMap := *(*map[string]*schema.Schema)(unsafe.Pointer(schemaField.UnsafeAddr()))
	readers := make(map[string]schema.FieldReader)

	statePtr := *(**terraform.InstanceState)(unsafe.Pointer(v.FieldByName("state").UnsafeAddr()))
	if statePtr != nil {
		readers["state"] = &schema.MapFieldReader{
			Schema: schemaMap,
			Map:    schema.BasicMapReader(statePtr.Attributes),
		}
	}

	configPtr := *(**terraform.ResourceConfig)(unsafe.Pointer(v.FieldByName("config").UnsafeAddr()))
	if configPtr != nil {
		readers["config"] = &schema.ConfigFieldReader{
			Schema: schemaMap,
			Config: configPtr,
		}
	}

	diffPtr := *(**terraform.InstanceDiff)(unsafe.Pointer(v.FieldByName("diff").UnsafeAddr()))
	if diffPtr != nil {
		readers["diff"] = &schema.DiffFieldReader{
			Schema: schemaMap,
			Diff:   diffPtr,
			Source: &schema.MultiLevelFieldReader{
				Levels:  []string{"state", "config"},
				Readers: readers,
			},
		}
	}

	setWriterPtr := *(**schema.MapFieldWriter)(unsafe.Pointer(v.FieldByName("setWriter").UnsafeAddr()))
	var setMap map[string]string
	if setWriterPtr != nil {
		setMap = setWriterPtr.Map()
	}
	readers["set"] = &schema.MapFieldReader{
		Schema: schemaMap,
		Map:    schema.BasicMapReader(setMap),
	}

	multi := &schema.MultiLevelFieldReader{
		Levels:  []string{"state", "config", "diff", "set"},
		Readers: readers,
	}
	reflect.NewAt(reflect.TypeOf((*schema.MultiLevelFieldReader)(nil)), unsafe.Pointer(v.FieldByName("multiReader").UnsafeAddr())).Elem().Set(reflect.ValueOf(multi))
}

func TestResourceBucketCreateSuccess(t *testing.T) {
	t.Helper()
	bucketID := "bucket-id"
	globalAlias := "global"
	localAlias := "local"
	accessKey := "key-123"
	step := 0
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch step {
		case 0:
			step++
			if r.URL.Path != "/v2/CreateBucket" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
				t.Fatalf("missing auth header, got %q", auth)
			}
			body, _ := io.ReadAll(r.Body)
			r.Body.Close()
			bodyStr := string(body)
			if !strings.Contains(bodyStr, `"globalAlias"`) || !strings.Contains(bodyStr, globalAlias) {
				t.Fatalf("expected global alias in body %s", bodyStr)
			}
			if !strings.Contains(bodyStr, accessKey) || !strings.Contains(bodyStr, localAlias) {
				t.Fatalf("expected local alias payload in body %s", bodyStr)
			}
			resp := bucketInfoJSON(bucketID, []string{globalAlias}, 0)
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(resp)),
			}, nil
		case 1:
			step++
			if r.URL.Path != "/v2/GetBucketInfo" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			resp := bucketInfoJSON(bucketID, []string{globalAlias}, 0)
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(resp)),
			}, nil
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return nil, nil
	}))

	raw := map[string]interface{}{
		"global_alias": globalAlias,
		"local_alias": []interface{}{
			map[string]interface{}{
				"alias":         localAlias,
				"access_key_id": accessKey,
			},
		},
	}
	d := schema.TestResourceDataRaw(t, resourceBucket().Schema, raw)

	diags := resourceBucketCreate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Id() != bucketID {
		t.Fatalf("expected bucket id %s, got %s", bucketID, d.Id())
	}
	if step != 2 {
		t.Fatalf("expected two API calls, got %d", step)
	}
	localState := d.Get("local_alias").([]interface{})
	if len(localState) != 1 {
		t.Fatalf("expected local alias preserved in state %#v", localState)
	}
	block := localState[0].(map[string]interface{})
	if block["alias"].(string) != localAlias {
		t.Fatalf("unexpected alias in state %#v", block)
	}
	if block["access_key_id"].(string) != accessKey {
		t.Fatalf("unexpected access key in state %#v", block)
	}
}

func TestResourceBucketCreateError(t *testing.T) {
	step := 0
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if step > 0 {
			t.Fatalf("unexpected extra request %s", r.URL.Path)
		}
		step++
		if r.URL.Path != "/v2/CreateBucket" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Body:       io.NopCloser(strings.NewReader("boom")),
			Header:     make(http.Header),
		}, nil
	}))

	raw := map[string]interface{}{
		"global_alias": "alias",
	}
	d := schema.TestResourceDataRaw(t, resourceBucket().Schema, raw)

	diags := resourceBucketCreate(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on create error")
	}
	if d.Id() != "" {
		t.Fatalf("expected resource ID to remain unset, got %s", d.Id())
	}
	if step != 1 {
		t.Fatalf("expected single API call, got %d", step)
	}
}

func TestResourceBucketUpdateRenameGlobalAlias(t *testing.T) {
	bucketID := "bucket"
	oldAlias := "old"
	newAlias := "new"
	step := 0
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch step {
		case 0:
			step++
			if r.URL.Path != "/v2/AddBucketAlias" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			body, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if !strings.Contains(string(body), newAlias) {
				t.Fatalf("expected new alias in body %s", body)
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader("null"))}, nil
		case 1:
			step++
			if r.URL.Path != "/v2/RemoveBucketAlias" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			body, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if !strings.Contains(string(body), oldAlias) {
				t.Fatalf("expected old alias in body %s", body)
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader("null"))}, nil
		case 2:
			step++
			if r.URL.Path != "/v2/UpdateBucket" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader("null"))}, nil
		case 3:
			if r.URL.Path != "/v2/GetBucketInfo" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoJSON(bucketID, []string{newAlias}, 0)))}, nil
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return nil, nil
	}))

	d := prepareBucketData(t, bucketID, oldAlias, newAlias)
	if o, n := d.GetChange("global_alias"); o.(string) != oldAlias || n.(string) != newAlias {
		t.Fatalf("unexpected change old=%v new=%v", o, n)
	}

	diags := resourceBucketUpdate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if d.Get("global_alias").(string) != newAlias {
		t.Fatalf("expected alias %s, got %s", newAlias, d.Get("global_alias"))
	}
}

func TestResourceBucketUpdateWebsiteAndQuotas(t *testing.T) {
	bucketID := "bucket"
	step := 0
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch step {
		case 0:
			step++
			if r.URL.Path != "/v2/UpdateBucket" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			body, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if !strings.Contains(string(body), "index.html") {
				t.Fatalf("expected index document in body %s", body)
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader("null"))}, nil
		case 1:
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoJSON(bucketID, []string{}, 0)))}, nil
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return nil, nil
	}))

	raw := map[string]interface{}{
		"website_access_enabled":        true,
		"website_config_index_document": "index.html",
		"website_config_error_document": "error.html",
		"quotas": []interface{}{
			map[string]interface{}{
				"max_size":    1,
				"max_objects": 2,
			},
		},
	}
	d := schema.TestResourceDataRaw(t, resourceBucket().Schema, raw)
	d.SetId(bucketID)

	diags := resourceBucketUpdate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
}

func TestResourceBucketUpdateNoChange(t *testing.T) {
	step := 0
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if step > 0 {
			t.Fatalf("unexpected extra request %s", r.URL.Path)
		}
		step++
		if r.URL.Path != "/v2/GetBucketInfo" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoJSON("bucket", []string{}, 0)))}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucket().Schema, map[string]interface{}{})
	d.SetId("bucket")

	diags := resourceBucketUpdate(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
	if step != 1 {
		t.Fatalf("expected single read call, got %d", step)
	}
}

func TestResourceBucketUpdateError(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v2/GetBucketInfo" {
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(bucketInfoJSON("bucket", []string{}, 0)))}, nil
		}
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader("error")), Header: make(http.Header)}, nil
	}))

	raw := map[string]interface{}{
		"website_access_enabled":        true,
		"website_config_index_document": "index.html",
	}
	d := schema.TestResourceDataRaw(t, resourceBucket().Schema, raw)
	d.SetId("bucket")

	diags := resourceBucketUpdate(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on update error")
	}
}

func TestResourceBucketDeleteSuccess(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/DeleteBucket" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucket().Schema, map[string]interface{}{})
	d.SetId("bucket")

	diags := resourceBucketDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
}

func TestResourceBucketDeleteNotFound(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucket().Schema, map[string]interface{}{})
	d.SetId("bucket")

	diags := resourceBucketDelete(context.Background(), d, p)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics %#v", diags)
	}
}

func TestResourceBucketDeleteError(t *testing.T) {
	p := newTestProvider(keyRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader("boom")), Header: make(http.Header)}, nil
	}))

	d := schema.TestResourceDataRaw(t, resourceBucket().Schema, map[string]interface{}{})
	d.SetId("bucket")

	diags := resourceBucketDelete(context.Background(), d, p)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on delete error")
	}
}
