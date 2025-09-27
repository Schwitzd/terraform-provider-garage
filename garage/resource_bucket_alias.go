package garage

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

/*
Resource: garage_bucket_alias

Manages a single alias for a bucket, either:
- GLOBAL alias: global_alias + bucket_id
- LOCAL  alias: access_key_id + local_alias + bucket_id

APIs used:
  - Add:    BucketAliasAPI.AddBucketAlias(ctx).AddBucketAliasRequest(NewAddBucketAliasRequest(...)).Execute()
  - Remove: BucketAliasAPI.RemoveBucketAlias(ctx).RemoveBucketAliasRequest(NewRemoveBucketAliasRequest(...)).Execute()
  - Read:   BucketAPI.GetBucketInfo(ctx).Id(bucket_id).Execute()

ID format:
  - global:<global_alias>
  - local:<access_key_id>:<local_alias>
*/

func resourceBucketAlias() *schema.Resource {
	return &schema.Resource{
		Description: "Manages a Garage bucket alias. An alias is an alternate name for a bucket, either global (cluster-wide) or local (scoped to an access key).",

		Schema: map[string]*schema.Schema{
			"bucket_id": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "ID of the target bucket (UUID). This must be the bucket’s unique identifier, not another alias.",
			},

			// GLOBAL mode
			"global_alias": {
				Type:          schema.TypeString,
				Optional:      true,
				ForceNew:      true,
				ConflictsWith: []string{"local_alias", "access_key_id"},
				Description:   "Cluster-wide alias name. Global aliases are unique across the cluster and can be used by any access key. Conflicts with `local_alias` and `access_key_id`.",
			},

			// LOCAL mode
			"local_alias": {
				Type:          schema.TypeString,
				Optional:      true,
				ForceNew:      true,
				RequiredWith:  []string{"access_key_id"},
				ConflictsWith: []string{"global_alias"},
				Description:   "Local alias name. Local aliases are only valid for the access key given in `access_key_id`. Requires `access_key_id`. Conflicts with `global_alias`.",
			},
			"access_key_id": {
				Type:          schema.TypeString,
				Optional:      true,
				ForceNew:      true,
				RequiredWith:  []string{"local_alias"},
				ConflictsWith: []string{"global_alias"},
				Description:   "Access key ID to which the local alias is bound. Required when `local_alias` is specified.",
			},

			"kind": {
				Type:        schema.TypeString,
				Computed:    true, // "global" or "local"
				Description: "Alias type, either `global` or `local`. Computed from the request.",
			},
		},

		CreateContext: resourceBucketAliasCreate,
		ReadContext:   resourceBucketAliasRead,
		DeleteContext: resourceBucketAliasDelete,

		Importer: &schema.ResourceImporter{
			// Accept import IDs in the form:
			//   global:<alias>
			//   local:<access_key_id>:<alias>
			StateContext: schema.ImportStatePassthroughContext,
		},

		CustomizeDiff: func(ctx context.Context, d *schema.ResourceDiff, _ interface{}) error {
			hasGlobal := d.Get("global_alias") != ""
			hasLocal := d.Get("local_alias") != "" || d.Get("access_key_id") != ""

			if !hasGlobal && !hasLocal {
				return fmt.Errorf("must specify either `global_alias` or (`local_alias` + `access_key_id`)")
			}
			if hasGlobal && hasLocal {
				return fmt.Errorf("`global_alias` conflicts with `local_alias`/`access_key_id`")
			}
			return nil
		},
	}
}

/* --------------------------------- Create -------------------------------- */

func resourceBucketAliasCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	bucketID := d.Get("bucket_id").(string)
	global := d.Get("global_alias").(string)
	local := d.Get("local_alias").(string)
	keyID := d.Get("access_key_id").(string)

	switch {
	case global != "":
		// Add GLOBAL alias
		req := p.client.BucketAliasAPI.
			AddBucketAlias(updateContext(ctx, p)).
			AddBucketAliasRequest(*garage.NewAddBucketAliasRequest(
				global, // globalAlias
				"",     // accessKeyId (unused)
				"",     // localAlias  (unused)
				bucketID,
			))
		_, httpResp, err := req.Execute()
		if err != nil {
			return createDiagnostics(err, httpResp)
		}
		d.SetId(fmt.Sprintf("global:%s", global))
		_ = d.Set("kind", "global")

	case local != "" && keyID != "":
		// Add LOCAL alias
		req := p.client.BucketAliasAPI.
			AddBucketAlias(updateContext(ctx, p)).
			AddBucketAliasRequest(*garage.NewAddBucketAliasRequest(
				"",    // globalAlias (unused)
				keyID, // accessKeyId
				local, // localAlias
				bucketID,
			))
		_, httpResp, err := req.Execute()
		if err != nil {
			return createDiagnostics(err, httpResp)
		}
		d.SetId(fmt.Sprintf("local:%s:%s", keyID, local))
		_ = d.Set("kind", "local")

	default:
		return diag.Diagnostics{diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "invalid alias specification",
			Detail:   "Provide either global_alias or (local_alias + access_key_id).",
		}}
	}

	return resourceBucketAliasRead(ctx, d, m)
}

/* ---------------------------------- Read --------------------------------- */

func resourceBucketAliasRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	bucketID := d.Get("bucket_id").(string)
	id := d.Id()

	kind, alias, keyID := parseAliasID(id, d)

	// Fetch bucket info once; used by both branches
	breq := p.client.BucketAPI.GetBucketInfo(updateContext(ctx, p)).Id(bucketID)
	info, httpResp, err := breq.Execute()
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == 404 {
			d.SetId("")
			return nil
		}
		return createDiagnostics(err, httpResp)
	}

	switch kind {
	case "global":
		// Verify global alias presence on the bucket
		found := false
		for _, ga := range info.GetGlobalAliases() {
			if ga == alias {
				found = true
				break
			}
		}
		if !found {
			d.SetId("")
			return nil
		}
		_ = d.Set("kind", "global")
		_ = d.Set("global_alias", alias)

	case "local":
		if keyID == "" || alias == "" {
			d.SetId("")
			return nil
		}
		// Look for the key within this bucket and confirm the alias is present for that key.
		found := false
		for _, k := range info.GetKeys() {
			if !keyMatchesAccessKeyID(k, keyID) {
				continue
			}
			if keyHasLocalAlias(k, alias) {
				found = true
				break
			}
		}
		if !found {
			d.SetId("")
			return nil
		}
		_ = d.Set("kind", "local")
		_ = d.Set("local_alias", alias)
		_ = d.Set("access_key_id", keyID)

	default:
		d.SetId("")
		return nil
	}

	return nil
}

/* -------------------------------- Delete --------------------------------- */

func resourceBucketAliasDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	bucketID := d.Get("bucket_id").(string)
	kind, alias, keyID := parseAliasID(d.Id(), d)

	switch kind {
	case "global":
		req := p.client.BucketAliasAPI.
			RemoveBucketAlias(updateContext(ctx, p)).
			RemoveBucketAliasRequest(*garage.NewRemoveBucketAliasRequest(
				alias, // globalAlias
				"",    // accessKeyId (unused)
				"",    // localAlias  (unused)
				bucketID,
			))
		_, httpResp, err := req.Execute()
		if err != nil {
			if httpResp != nil && httpResp.StatusCode == 404 {
				return nil
			}
			return createDiagnostics(err, httpResp)
		}

	case "local":
		req := p.client.BucketAliasAPI.
			RemoveBucketAlias(updateContext(ctx, p)).
			RemoveBucketAliasRequest(*garage.NewRemoveBucketAliasRequest(
				"",    // globalAlias (unused)
				keyID, // accessKeyId
				alias, // localAlias
				bucketID,
			))
		_, httpResp, err := req.Execute()
		if err != nil {
			if httpResp != nil && httpResp.StatusCode == 404 {
				return nil
			}
			return createDiagnostics(err, httpResp)
		}
	}

	return nil
}

/* ------------------------------- helpers --------------------------------- */

// parseAliasID extracts kind/alias/keyID from the Terraform ID, with state fallback.
func parseAliasID(id string, d *schema.ResourceData) (kind, alias, keyID string) {
	if strings.HasPrefix(id, "global:") {
		return "global", strings.TrimPrefix(id, "global:"), ""
	}
	if strings.HasPrefix(id, "local:") {
		rest := strings.TrimPrefix(id, "local:")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) == 2 {
			return "local", parts[1], parts[0]
		}
	}
	// Fallback: infer from state
	if ga, ok := d.GetOk("global_alias"); ok && ga.(string) != "" {
		return "global", ga.(string), ""
	}
	return "local", d.Get("local_alias").(string), d.Get("access_key_id").(string)
}

// keyMatchesAccessKeyID returns true if the GetBucketInfoKey has AccessKeyId (or similar) == want.
func keyMatchesAccessKeyID(key interface{}, want string) bool {
	rv := reflect.ValueOf(key)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return false
	}

	for _, name := range []string{"AccessKeyId", "AccessKeyID", "Id", "ID"} {
		f := rv.FieldByName(name)
		if f.IsValid() && f.CanInterface() && f.Kind() == reflect.String {
			if f.Interface().(string) == want {
				return true
			}
		}
	}
	// Getter-based fallbacks (in case your SDK generates methods only)
	if m := rv.MethodByName("GetAccessKeyId"); m.IsValid() && m.Type().NumIn() == 0 && m.Type().NumOut() == 1 {
		out := m.Call(nil)
		if len(out) == 1 && out[0].Kind() == reflect.String && out[0].String() == want {
			return true
		}
	}
	if m := rv.MethodByName("GetId"); m.IsValid() && m.Type().NumIn() == 0 && m.Type().NumOut() == 1 {
		out := m.Call(nil)
		if len(out) == 1 && out[0].Kind() == reflect.String && out[0].String() == want {
			return true
		}
	}
	return false
}

// keyHasLocalAlias returns true if the GetBucketInfoKey contains the given local alias.
// Supports common shapes: []string, map[string]bool/map[string]string, or []struct{ Alias string }.
func keyHasLocalAlias(key interface{}, alias string) bool {
	rv := reflect.ValueOf(key)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return false
	}

	// Try likely field names holding local aliases
	fields := []string{
		"LocalAliases", "Aliases", "LocalAlias", "LocalAliasesList", "LocalAliasesMap",
	}

	for _, name := range fields {
		f := rv.FieldByName(name)
		if !f.IsValid() {
			// Try Get<name>() getter
			getter := rv.MethodByName("Get" + name)
			if getter.IsValid() && getter.Type().NumIn() == 0 && getter.Type().NumOut() == 1 {
				out := getter.Call(nil)
				if len(out) == 1 {
					f = out[0]
				}
			}
		}
		if !f.IsValid() {
			continue
		}

		switch f.Kind() {
		case reflect.Slice:
			for i := 0; i < f.Len(); i++ {
				elem := f.Index(i)
				switch elem.Kind() {
				case reflect.String:
					if elem.String() == alias {
						return true
					}
				case reflect.Struct:
					for _, an := range []string{"Alias", "Name"} {
						af := elem.FieldByName(an)
						if af.IsValid() && af.Kind() == reflect.String && af.String() == alias {
							return true
						}
					}
					// Try getters on element
					if m := elem.Addr().MethodByName("GetAlias"); m.IsValid() && m.Type().NumIn() == 0 && m.Type().NumOut() == 1 {
						if out := m.Call(nil); len(out) == 1 && out[0].Kind() == reflect.String && out[0].String() == alias {
							return true
						}
					}
				}
			}

		case reflect.Map:
			for _, mk := range f.MapKeys() {
				if mk.Kind() == reflect.String && mk.String() == alias {
					return true
				}
			}
		}
	}

	return false
}
