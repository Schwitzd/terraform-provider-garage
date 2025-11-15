package garage

import (
	"context"
	"fmt"
	"net/http"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func getOkString(d *schema.ResourceData, key string) (string, bool) {
	v, ok := d.GetOk(key)
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, s != ""
}

func resourceBucket() *schema.Resource {
	return &schema.Resource{
		Description:   "This resource manages Garage buckets (global alias optional; create-time local alias optional).",
		Schema:        schemaBucket(),
		CreateContext: resourceBucketCreate,
		ReadContext:   resourceBucketRead,
		UpdateContext: resourceBucketUpdate,
		DeleteContext: resourceBucketDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		CustomizeDiff: func(ctx context.Context, d *schema.ResourceDiff, m interface{}) error {
			if d.Get("website_access_enabled").(bool) {
				if v, ok := d.GetOk("website_config_index_document"); !ok || v.(string) == "" {
					return fmt.Errorf("website_config_index_document is required when website_access_enabled is true")
				}
			}
			return nil
		},
	}
}

func schemaBucket() map[string]*schema.Schema {
	return map[string]*schema.Schema{
		/* ------------------------------ Inputs ------------------------------ */

		"global_alias": {
			Type:        schema.TypeString,
			Optional:    true,
			Description: "Creates a global alias for the bucket. A global alias is unique cluster-wide (e.g. `my-bucket`). You can add or remove additional aliases later using the `garage_bucket_alias` resource.",
		},

		"local_alias": {
			Type:        schema.TypeList,
			Optional:    true,
			MaxItems:    1,
			Description: "Creates a local alias bound to a specific access key at bucket creation time. Only one block is allowed here.",
			Elem: &schema.Resource{
				Schema: map[string]*schema.Schema{
					"alias": {
						Type:        schema.TypeString,
						Required:    true,
						ForceNew:    true,
						Description: "Local alias name. Acts as a shortcut for the bucket but only in the context of the given access key.",
					},
					"access_key_id": {
						Type:        schema.TypeString,
						Required:    true,
						ForceNew:    true,
						Description: "The access key ID that this local alias is bound to.",
					},
				},
			},
		},

		"website_access_enabled": {
			Type:        schema.TypeBool,
			Optional:    true,
			Default:     false,
			Description: "Enable static website hosting for the bucket. Defaults to `false`. When enabled, `website_config_index_document` is required.",
		},
		"website_config_index_document": {
			Type:        schema.TypeString,
			Optional:    true,
			Computed:    true,
			Description: "Name of the index document (e.g. `index.html`). Required if `website_access_enabled` is `true`.",
		},
		"website_config_error_document": {
			Type:        schema.TypeString,
			Optional:    true,
			Computed:    true,
			Description: "Name of the error document (e.g. `404.html`). Optional, used when website hosting is enabled.",
		},

		"quotas": {
			Type:        schema.TypeList,
			Optional:    true,
			MaxItems:    1,
			Description: "Optional storage quotas for this bucket. If omitted or set to zero, the bucket has no limits.",
			Elem: &schema.Resource{
				Schema: map[string]*schema.Schema{
					"max_size": {
						Type:        schema.TypeInt,
						Optional:    true,
						Description: "Maximum total size in bytes allowed for this bucket. `0` means unlimited.",
					},
					"max_objects": {
						Type:        schema.TypeInt,
						Optional:    true,
						Description: "Maximum number of objects allowed in this bucket. `0` means unlimited.",
					},
				},
			},
		},

		/* ------------------------------ Outputs ----------------------------- */

		"global_aliases": {
			Type:        schema.TypeList,
			Elem:        &schema.Schema{Type: schema.TypeString},
			Computed:    true,
			Description: "List of all global aliases currently bound to the bucket.",
		},
		"objects": {
			Type:        schema.TypeInt,
			Computed:    true,
			Description: "Number of objects stored in the bucket.",
		},
		"bytes": {
			Type:        schema.TypeInt,
			Computed:    true,
			Description: "Total bytes used by objects in the bucket.",
		},
		"unfinished_uploads": {
			Type:        schema.TypeInt,
			Computed:    true,
			Description: "Number of unfinished uploads currently tracked for the bucket.",
		},
	}
}

func flattenBucketInfo(bucket *garage.GetBucketInfoResponse) map[string]interface{} {
	b := map[string]interface{}{
		"global_aliases":         bucket.GlobalAliases,
		"website_access_enabled": bucket.WebsiteAccess,
		"objects":                bucket.Objects,
		"bytes":                  bucket.Bytes,
		"unfinished_uploads":     bucket.UnfinishedUploads,
	}

	// Website config
	if bucket.WebsiteConfig.IsSet() && bucket.WebsiteConfig.Get() != nil {
		wc := bucket.WebsiteConfig.Get()
		b["website_config_index_document"] = wc.IndexDocument

		if wc.ErrorDocument.IsSet() {
			if v := wc.ErrorDocument.Get(); v != nil {
				b["website_config_error_document"] = *v
			} else {
				b["website_config_error_document"] = nil
			}
		} else {
			b["website_config_error_document"] = nil
		}
	}

	// Quotas
	if bucket.Quotas.MaxSize.IsSet() || bucket.Quotas.MaxObjects.IsSet() {
		q := map[string]interface{}{}
		hasAny := false

		if bucket.Quotas.MaxSize.IsSet() {
			if v := bucket.Quotas.MaxSize.Get(); v != nil && *v > 0 {
				q["max_size"] = int(*v)
				hasAny = true
			}
		}

		if bucket.Quotas.MaxObjects.IsSet() {
			if v := bucket.Quotas.MaxObjects.Get(); v != nil && *v > 0 {
				q["max_objects"] = int(*v)
				hasAny = true
			}
		}

		if hasAny {
			b["quotas"] = []interface{}{q}
		}
	}

	return b
}

func resourceBucketCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	reqBody := garage.CreateBucketRequest{}
	if alias, ok := getOkString(d, "global_alias"); ok {
		reqBody.SetGlobalAlias(alias)
	}

	// optional local_alias at create time
	if raw, ok := d.GetOk("local_alias"); ok {
		items := raw.([]interface{})
		if len(items) == 1 && items[0] != nil {
			lm := items[0].(map[string]interface{})
			la := lm["alias"].(string)
			ak := lm["access_key_id"].(string)

			localAlias := garage.NewCreateBucketLocalAlias(ak, la)
			reqBody.SetLocalAlias(*localAlias)
		}
	}

	resp, httpResp, err := p.client.BucketAPI.
		CreateBucket(p.withToken(ctx)).
		CreateBucketRequest(reqBody).
		Execute()
	if err != nil {
		return createDiagnostics(err, httpResp)
	}

	d.SetId(resp.Id)

	// keep local_alias in state; not exposed by GetBucketInfo
	if v, ok := d.GetOk("local_alias"); ok {
		_ = d.Set("local_alias", v)
	}

	return resourceBucketRead(ctx, d, m)
}

func resourceBucketRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	bucket, httpResp, err := p.client.BucketAPI.
		GetBucketInfo(p.withToken(ctx)).
		Id(d.Id()).
		Execute()
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == http.StatusNotFound {
			d.SetId("")
			return nil
		}
		return createDiagnostics(err, httpResp)
	}
	if bucket == nil {
		d.SetId("")
		return nil
	}

	for k, v := range flattenBucketInfo(bucket) {
		if err := d.Set(k, v); err != nil {
			return diag.FromErr(err)
		}
	}

	return nil
}

func buildWebsiteAccess(d *schema.ResourceData) (*garage.UpdateBucketWebsiteAccess, diag.Diagnostics) {
	if v, ok := d.GetOk("website_access_enabled"); ok {
		if v.(bool) {
			indexDoc, _ := getOkString(d, "website_config_index_document")
			if indexDoc == "" {
				return nil, diag.Diagnostics{{
					Severity: diag.Error,
					Summary:  "website access enabled but index document missing",
					Detail:   "website_config_index_document is required when website_access_enabled is true",
				}}
			}
			var errDocPtr *string
			if s, ok := getOkString(d, "website_config_error_document"); ok {
				errDocPtr = &s
			}
			return &garage.UpdateBucketWebsiteAccess{
				Enabled:       true,
				IndexDocument: *garage.NewNullableString(&indexDoc),
				ErrorDocument: *garage.NewNullableString(errDocPtr),
			}, nil
		}
		return &garage.UpdateBucketWebsiteAccess{Enabled: false}, nil
	}
	return nil, nil
}

func buildQuotas(d *schema.ResourceData) (*garage.ApiBucketQuotas, diag.Diagnostics) {
	raw := d.Get("quotas").([]interface{})
	if len(raw) == 0 {
		return nil, nil
	}

	qm := raw[0].(map[string]interface{})
	sizeRaw, sizeSet := qm["max_size"]
	objsRaw, objsSet := qm["max_objects"]

	if !sizeSet && !objsSet {
		return nil, nil
	}
	if sizeSet && objsSet {
		maxSize := int64(sizeRaw.(int))
		maxObjects := int64(objsRaw.(int))
		return &garage.ApiBucketQuotas{
			MaxSize:    *garage.NewNullableInt64(&maxSize),
			MaxObjects: *garage.NewNullableInt64(&maxObjects),
		}, nil
	}

	return nil, diag.Diagnostics{{
		Severity: diag.Error,
		Summary:  "invalid quotas configuration",
		Detail:   "both max_size and max_objects must be set together, or neither",
	}}
}

func resourceBucketUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	// rename semantics for global_alias
	if d.HasChange("global_alias") {
		oldRaw, newRaw := d.GetChange("global_alias")
		oldAlias := oldRaw.(string)
		newAlias := newRaw.(string)

		// add new first
		if newAlias != "" {
			_, httpResp, err := p.client.BucketAliasAPI.
				AddBucketAlias(p.withToken(ctx)).
				AddBucketAliasRequest(*garage.NewAddBucketAliasRequest(
					newAlias, "", "", d.Id(),
				)).
				Execute()
			if err != nil {
				return createDiagnostics(err, httpResp)
			}
		}

		// then remove old (if different)
		if oldAlias != "" && oldAlias != newAlias {
			_, httpResp, err := p.client.BucketAliasAPI.
				RemoveBucketAlias(p.withToken(ctx)).
				RemoveBucketAliasRequest(*garage.NewRemoveBucketAliasRequest(
					oldAlias, "", "", d.Id(),
				)).
				Execute()
			if err != nil {
				return createDiagnostics(err, httpResp)
			}
		}
	}

	websiteAccess, diags := buildWebsiteAccess(d)
	if len(diags) > 0 {
		return diags
	}
	quotas, diags := buildQuotas(d)
	if len(diags) > 0 {
		return diags
	}

	// nothing else to update
	if websiteAccess == nil && quotas == nil && !d.HasChange("global_alias") {
		return resourceBucketRead(ctx, d, m)
	}

	updateReq := garage.UpdateBucketRequestBody{}
	if websiteAccess != nil {
		updateReq.WebsiteAccess = *garage.NewNullableUpdateBucketWebsiteAccess(websiteAccess)
	}
	if quotas != nil {
		updateReq.Quotas = *garage.NewNullableApiBucketQuotas(quotas)
	}

	_, httpResp, err := p.client.BucketAPI.
		UpdateBucket(p.withToken(ctx)).
		Id(d.Id()).
		UpdateBucketRequestBody(updateReq).
		Execute()
	if err != nil {
		return createDiagnostics(err, httpResp)
	}

	return resourceBucketRead(ctx, d, m)
}

func resourceBucketDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	httpResp, err := p.client.BucketAPI.
		DeleteBucket(p.withToken(ctx)).
		Id(d.Id()).
		Execute()
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == http.StatusNotFound {
			return nil
		}
		return createDiagnostics(err, httpResp)
	}
	return nil
}
