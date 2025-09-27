package garage

import (
	"context"
	"fmt"

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
		b["website_config_error_document"] = wc.ErrorDocument
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

	// Create-time only: local alias bound to an access key (SDK: AccessKeyId + Alias)
	if raw, ok := d.GetOk("local_alias"); ok {
		items := raw.([]interface{})
		if len(items) == 1 && items[0] != nil {
			lm := items[0].(map[string]interface{})
			la := lm["alias"].(string)
			ak := lm["access_key_id"].(string)

			localAlias := garage.NewCreateBucketLocalAlias(ak, la)

			// IMPORTANT: Set the struct directly (not the Nullable wrapper)
			reqBody.SetLocalAlias(*localAlias)
		}
	}

	req := p.client.BucketAPI.CreateBucket(updateContext(ctx, p)).CreateBucketRequest(reqBody)
	resp, httpResp, err := req.Execute()
	if err != nil {
		return createDiagnostics(err, httpResp)
	}

	d.SetId(resp.Id)

	// Preserve configured local_alias in state (not readable via GetBucketInfo)
	if v, ok := d.GetOk("local_alias"); ok {
		_ = d.Set("local_alias", v)
	}

	return resourceBucketRead(ctx, d, m)
}

func resourceBucketRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	req := p.client.BucketAPI.GetBucketInfo(updateContext(ctx, p)).Id(d.Id())
	bucket, httpResp, err := req.Execute()
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == 404 {
			d.SetId("")
			return nil
		}
		return createDiagnostics(err, httpResp)
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
		enabled := v.(bool)
		if enabled {
			indexDoc, _ := getOkString(d, "website_config_index_document")
			if indexDoc == "" {
				return nil, diag.Diagnostics{diag.Diagnostic{
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

	return nil, diag.Diagnostics{diag.Diagnostic{
		Severity: diag.Error,
		Summary:  "invalid quotas configuration",
		Detail:   "Both max_size and max_objects must be set together, or neither.",
	}}
}

func resourceBucketUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	// --- handle global_alias change by add->remove (rename semantics) ---
	if d.HasChange("global_alias") {
		oldRaw, newRaw := d.GetChange("global_alias")
		oldAlias := oldRaw.(string)
		newAlias := newRaw.(string)

		// 1) add the new alias first, so we never drop access if add fails
		if newAlias != "" {
			req := p.client.BucketAliasAPI.
				AddBucketAlias(updateContext(ctx, p)).
				AddBucketAliasRequest(*garage.NewAddBucketAliasRequest(
					newAlias, // globalAlias
					"",       // accessKeyId (unused)
					"",       // localAlias (unused)
					d.Id(),   // bucketId
				))
			_, httpResp, err := req.Execute()
			if err != nil {
				return createDiagnostics(err, httpResp)
			}
		}

		// 2) remove the old alias if there was one
		if oldAlias != "" && oldAlias != newAlias {
			req := p.client.BucketAliasAPI.
				RemoveBucketAlias(updateContext(ctx, p)).
				RemoveBucketAliasRequest(*garage.NewRemoveBucketAliasRequest(
					oldAlias, // globalAlias
					"",       // accessKeyId (unused)
					"",       // localAlias (unused)
					d.Id(),   // bucketId
				))
			_, httpResp, err := req.Execute()
			if err != nil {
				return createDiagnostics(err, httpResp)
			}
		}
	}

	// --- your existing update logic ---
	websiteAccess, diags := buildWebsiteAccess(d)
	if len(diags) > 0 {
		return diags
	}
	quotas, diags := buildQuotas(d)
	if len(diags) > 0 {
		return diags
	}

	// If nothing else to update, just refresh state
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

	req := p.client.BucketAPI.UpdateBucket(updateContext(ctx, p)).Id(d.Id()).UpdateBucketRequestBody(updateReq)
	_, httpResp, err := req.Execute()
	if err != nil {
		return createDiagnostics(err, httpResp)
	}

	return resourceBucketRead(ctx, d, m)
}

func resourceBucketDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	req := p.client.BucketAPI.DeleteBucket(updateContext(ctx, p)).Id(d.Id())
	httpResp, err := req.Execute()
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == 404 {
			return nil
		}
		return createDiagnostics(err, httpResp)
	}
	return nil
}
