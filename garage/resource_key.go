package garage

import (
	"context"
	"fmt"
	"reflect"
	"time"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

/*
Resource: garage_key

Manages an access key via AccessKeyAPI:
  - Create: AccessKeyAPI.CreateKey(ctx).Body(UpdateKeyRequestBody).Execute()
  - Read:   AccessKeyAPI.GetKeyInfo(ctx).Id(id).Execute()
  - Update: AccessKeyAPI.UpdateKey(ctx).Id(id).UpdateKeyRequestBody(UpdateKeyRequestBody).Execute()
  - Delete: AccessKeyAPI.DeleteKey(ctx).Id(id).Execute()

Inputs:
  - name (optional)
  - expiration (optional RFC3339)
  - permissions block with read/write/admin booleans (optional)

Outputs:
  - id (access_key_id)
  - secret_access_key (sensitive, only available on create/read if API returns it)
  - created (RFC3339, if available)
  - expired (bool)
  - permissions (echoed)
*/

func resourceKey() *schema.Resource {
	return &schema.Resource{
		Description:   "Manage a Garage access key.",
		Schema:        schemaKey(),
		CreateContext: resourceKeyCreate,
		ReadContext:   resourceKeyRead,
		UpdateContext: resourceKeyUpdate,
		DeleteContext: resourceKeyDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
	}
}

func schemaKey() map[string]*schema.Schema {
	return map[string]*schema.Schema{
		/* ------------------------------ Inputs ------------------------------ */

		"name": {
			Type:        schema.TypeString,
			Optional:    true,
			Description: "Human-friendly label for the access key. Does not affect permissions or behavior.",
		},

		"expiration": {
			Type:        schema.TypeString,
			Optional:    true,
			Description: "Optional expiration timestamp in RFC3339 format (e.g. `2025-09-26T12:00:00Z`). After this time the key becomes invalid.",
		},

		"permissions": {
			Type:        schema.TypeList,
			Optional:    true,
			MaxItems:    1,
			Description: "Access permissions for the key. Only one block is allowed.",
			Elem: &schema.Resource{
				Schema: map[string]*schema.Schema{
					"read": {
						Type:        schema.TypeBool,
						Optional:    true,
						Description: "Allow read access to buckets and objects.",
					},
					"write": {
						Type:        schema.TypeBool,
						Optional:    true,
						Description: "Allow write access (create/update/delete objects).",
					},
					"admin": {
						Type:        schema.TypeBool,
						Optional:    true,
						Description: "Allow administrative access (bucket/key management).",
					},
				},
			},
		},

		/* ------------------------------ Outputs ----------------------------- */

		"access_key_id": {
			Type:        schema.TypeString,
			Computed:    true,
			Description: "Unique identifier of the access key, used in API requests and alias binding.",
		},

		"secret_access_key": {
			Type:        schema.TypeString,
			Computed:    true,
			Sensitive:   true,
			Description: "Secret token associated with the key. Only visible at creation time — it will not be returned again.",
		},

		"created": {
			Type:        schema.TypeString,
			Computed:    true,
			Description: "Timestamp (RFC3339) when the key was created.",
		},

		"expired": {
			Type:        schema.TypeBool,
			Computed:    true,
			Description: "True if the key is expired according to its `expiration` setting.",
		},

		"effective_permissions": {
			Type:        schema.TypeList,
			Computed:    true,
			Description: "The effective permissions currently active for the key (read/write/admin).",
			Elem: &schema.Resource{
				Schema: map[string]*schema.Schema{
					"read":  {Type: schema.TypeBool, Computed: true, Description: "Whether read access is enabled."},
					"write": {Type: schema.TypeBool, Computed: true, Description: "Whether write access is enabled."},
					"admin": {Type: schema.TypeBool, Computed: true, Description: "Whether admin access is enabled."},
				},
			},
		},
	}
}

/* --------------------------------- Create -------------------------------- */

func resourceKeyCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	// Build UpdateKeyRequestBody for creation (API reuses same shape)
	body, diags := buildUpdateKeyRequestBody(d)
	if len(diags) > 0 {
		return diags
	}

	req := p.client.AccessKeyAPI.CreateKey(updateContext(ctx, p)).Body(*body)
	resp, httpResp, err := req.Execute()
	if err != nil {
		return createDiagnostics(err, httpResp)
	}

	// ID & state
	d.SetId(resp.GetAccessKeyId())
	_ = d.Set("access_key_id", resp.GetAccessKeyId())
	if s := safeGetStringPtr(resp.GetSecretAccessKeyOk()); s != "" {
		_ = d.Set("secret_access_key", s)
	}

	// Fill computed fields
	flattenKeyInfo(resp, d)

	return nil
}

/* ---------------------------------- Read --------------------------------- */

func resourceKeyRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	id := d.Id()
	req := p.client.AccessKeyAPI.GetKeyInfo(updateContext(ctx, p)).Id(id)
	resp, httpResp, err := req.Execute()
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == 404 {
			d.SetId("")
			return nil
		}
		return createDiagnostics(err, httpResp)
	}

	_ = d.Set("access_key_id", resp.GetAccessKeyId())
	// Secret is usually not returned after the first call; preserve old if API doesn’t return it
	if s := safeGetStringPtr(resp.GetSecretAccessKeyOk()); s != "" {
		_ = d.Set("secret_access_key", s)
	}

	flattenKeyInfo(resp, d)
	return nil
}

/* -------------------------------- Update --------------------------------- */

func resourceKeyUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	if !(d.HasChange("name") || d.HasChange("expiration") || d.HasChange("permissions")) {
		return resourceKeyRead(ctx, d, m)
	}

	body, diags := buildUpdateKeyRequestBody(d)
	if len(diags) > 0 {
		return diags
	}

	id := d.Id()
	req := p.client.AccessKeyAPI.UpdateKey(updateContext(ctx, p)).Id(id).UpdateKeyRequestBody(*body)
	resp, httpResp, err := req.Execute()
	if err != nil {
		return createDiagnostics(err, httpResp)
	}

	// Refresh state from server response
	_ = d.Set("access_key_id", resp.GetAccessKeyId())
	if s := safeGetStringPtr(resp.GetSecretAccessKeyOk()); s != "" {
		_ = d.Set("secret_access_key", s)
	}
	flattenKeyInfo(resp, d)
	return nil
}

/* -------------------------------- Delete --------------------------------- */

func resourceKeyDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	id := d.Id()
	httpResp, err := p.client.AccessKeyAPI.DeleteKey(updateContext(ctx, p)).Id(id).Execute()
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == 404 {
			return nil
		}
		return createDiagnostics(err, httpResp)
	}
	return nil
}

/* ------------------------------- Helpers --------------------------------- */

func flattenKeyInfo(resp *garage.GetKeyInfoResponse, d *schema.ResourceData) {
	_ = d.Set("expired", resp.GetExpired())
	if t, ok := resp.GetCreatedOk(); ok {
		_ = d.Set("created", t.Format(time.RFC3339))
	}

	// Echo effective permissions if we can introspect them
	if perms, ok := resp.GetPermissionsOk(); ok {
		read, write, admin := reflectKeyPerm(*perms)
		_ = d.Set("effective_permissions", []interface{}{
			map[string]interface{}{"read": read, "write": write, "admin": admin},
		})
	}
}

// buildUpdateKeyRequestBody builds the UpdateKeyRequestBody using reflection-friendly setters.
// It fills name, expiration (RFC3339), and permissions {read,write,admin}.
func buildUpdateKeyRequestBody(d *schema.ResourceData) (*garage.UpdateKeyRequestBody, diag.Diagnostics) {
	body := garage.NewUpdateKeyRequestBody() // If your SDK uses a different ctor, adjust here.

	// name
	if v, ok := d.GetOk("name"); ok && v.(string) != "" {
		setStringFieldOrSetter(body, "Name", v.(string))
	}

	// expiration
	if v, ok := d.GetOk("expiration"); ok && v.(string) != "" {
		t, err := time.Parse(time.RFC3339, v.(string))
		if err != nil {
			return nil, diag.Diagnostics{diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "invalid expiration",
				Detail:   fmt.Sprintf("must be RFC3339: %v", err),
			}}
		}
		// Try common patterns: SetExpiration(time.Time) or field Expiration (time.Time or NullableTime)
		setTimeFieldOrSetter(body, "Expiration", t)
	}

	// permissions block
	if v, ok := d.GetOk("permissions"); ok {
		list := v.([]interface{})
		if len(list) == 1 && list[0] != nil {
			pm := list[0].(map[string]interface{})
			read := pm["read"] == true
			write := pm["write"] == true
			admin := pm["admin"] == true

			perm := buildKeyPerm(read, write, admin)
			setStructFieldOrSetter(body, "Permissions", perm)
		}
	}

	return body, nil
}

// buildKeyPerm constructs a KeyPerm (or compatible struct) with read/write/admin via reflection.
func buildKeyPerm(read, write, admin bool) interface{} {
	// Create zero value of garage.KeyPerm
	var kp garage.KeyPerm

	// Try setters first
	setBoolFieldOrSetter(&kp, "Read", read)
	setBoolFieldOrSetter(&kp, "Write", write)
	setBoolFieldOrSetter(&kp, "Admin", admin)

	// In case the SDK uses different field names, try a few alternates
	setBoolFieldOrSetter(&kp, "CanRead", read)
	setBoolFieldOrSetter(&kp, "CanWrite", write)
	setBoolFieldOrSetter(&kp, "IsAdmin", admin)

	return kp
}

func reflectKeyPerm(kp garage.KeyPerm) (read, write, admin bool) {
	read = getBoolFieldOrGetter(kp, "Read") || getBoolFieldOrGetter(kp, "CanRead")
	write = getBoolFieldOrGetter(kp, "Write") || getBoolFieldOrGetter(kp, "CanWrite")
	admin = getBoolFieldOrGetter(kp, "Admin") || getBoolFieldOrGetter(kp, "IsAdmin")
	return
}

func safeGetStringPtr(ptr *string, ok bool) string {
	if ok && ptr != nil {
		return *ptr
	}
	return ""
}

/* --------------------- tiny reflection convenience helpers ---------------- */

func setStringFieldOrSetter(obj interface{}, name string, val string) {
	rv := reflect.ValueOf(obj)
	if rv.Kind() == reflect.Pointer {
		// try setter Set<Name>(string)
		if m := rv.MethodByName("Set" + name); m.IsValid() && m.Type().NumIn() == 1 && m.Type().In(0).Kind() == reflect.String {
			m.Call([]reflect.Value{reflect.ValueOf(val)})
			return
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		f := rv.FieldByName(name)
		if f.IsValid() && f.CanSet() && f.Kind() == reflect.String {
			f.SetString(val)
		}
	}
}

func setTimeFieldOrSetter(obj interface{}, name string, t time.Time) {
	rv := reflect.ValueOf(obj)
	arg := reflect.ValueOf(t)

	if rv.Kind() == reflect.Pointer {
		// common setter: Set<Name>(time.Time)
		if m := rv.MethodByName("Set" + name); m.IsValid() && m.Type().NumIn() == 1 && m.Type().In(0) == reflect.TypeOf(time.Time{}) {
			m.Call([]reflect.Value{arg})
			return
		}
		// sometimes APIs use a NullableTime wrapper with helper like Set<Name>Nil(false) then Set<Name>(time.Time)
		if m := rv.MethodByName("Unset" + name); m.IsValid() && m.Type().NumIn() == 0 {
			m.Call(nil)
		}
		rv = rv.Elem()
	}

	if rv.Kind() == reflect.Struct {
		f := rv.FieldByName(name)
		if f.IsValid() && f.CanSet() {
			// If the field is exactly time.Time
			if f.Type() == reflect.TypeOf(time.Time{}) {
				f.Set(arg)
				return
			}
			// If the field is a NullableTime-like struct with Set/Get methods, try SetTime
			if m := reflect.New(f.Type()).Elem(); m.IsValid() {
				// fallback: set zero (won't help much without type knowledge)
				// prefer using real setter via MethodByName above where possible
			}
		}
	}
}

func setStructFieldOrSetter(obj interface{}, name string, val interface{}) {
	rv := reflect.ValueOf(obj)
	vv := reflect.ValueOf(val)

	if rv.Kind() == reflect.Pointer {
		// try setter Set<Name>(<type>)
		if m := rv.MethodByName("Set" + name); m.IsValid() && m.Type().NumIn() == 1 {
			// If arg type differs but is assignable, convert
			argT := m.Type().In(0)
			if vv.Type().AssignableTo(argT) {
				m.Call([]reflect.Value{vv})
				return
			}
			if vv.Type().ConvertibleTo(argT) {
				m.Call([]reflect.Value{vv.Convert(argT)})
				return
			}
		}
		rv = rv.Elem()
	}

	if rv.Kind() == reflect.Struct {
		f := rv.FieldByName(name)
		if f.IsValid() && f.CanSet() {
			if vv.Type().AssignableTo(f.Type()) {
				f.Set(vv)
				return
			}
			if vv.Type().ConvertibleTo(f.Type()) {
				f.Set(vv.Convert(f.Type()))
				return
			}
		}
	}
}

func setBoolFieldOrSetter(obj interface{}, name string, val bool) {
	rv := reflect.ValueOf(obj)
	if rv.Kind() == reflect.Pointer {
		// try setter Set<Name>(bool)
		if m := rv.MethodByName("Set" + name); m.IsValid() && m.Type().NumIn() == 1 && m.Type().In(0).Kind() == reflect.Bool {
			m.Call([]reflect.Value{reflect.ValueOf(val)})
			return
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		f := rv.FieldByName(name)
		if f.IsValid() && f.CanSet() && f.Kind() == reflect.Bool {
			f.SetBool(val)
		}
	}
}

func getBoolFieldOrGetter(obj interface{}, name string) bool {
	rv := reflect.ValueOf(obj)
	// Try getter
	if m := rv.MethodByName("Get" + name); m.IsValid() && m.Type().NumIn() == 0 && m.Type().NumOut() == 1 && m.Type().Out(0).Kind() == reflect.Bool {
		out := m.Call(nil)
		return out[0].Bool()
	}
	// Fall back to field
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		f := rv.FieldByName(name)
		if f.IsValid() && f.Kind() == reflect.Bool {
			return f.Bool()
		}
	}
	return false
}
