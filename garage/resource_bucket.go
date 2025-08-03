package garage

import (
	"context"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceBucket() *schema.Resource {
	return &schema.Resource{
		Description:   "This resource can be used to manage Garage buckets.",
		Schema:        schemaBucket(),
		CreateContext: resourceBucketCreate,
		ReadContext:   resourceBucketRead,
		UpdateContext: resourceBucketUpdate,
		DeleteContext: resourceBucketDelete,
	}
}

func schemaBucket() map[string]*schema.Schema {
	return map[string]*schema.Schema{
		"website_access_enabled": {
			Type:     schema.TypeBool,
			Optional: true,
			Default:  false,
		},
		"website_config_index_document": {
			Type:     schema.TypeString,
			Optional: true,
			Computed: true,
		},
		"website_config_error_document": {
			Type:     schema.TypeString,
			Optional: true,
			Computed: true,
		},
		"quota_max_size": {
			Type:     schema.TypeInt,
			Optional: true,
			Computed: true,
		},
		"quota_max_objects": {
			Type:     schema.TypeInt,
			Optional: true,
			Computed: true,
		},
		"global_aliases": {
			Type:     schema.TypeList,
			Elem:     &schema.Schema{Type: schema.TypeString},
			Computed: true,
		},
		"objects": {
			Type:     schema.TypeInt,
			Computed: true,
		},
		"bytes": {
			Type:     schema.TypeInt,
			Computed: true,
		},
		"unfinished_uploads": {
			Type:     schema.TypeInt,
			Computed: true,
		},
	}
}

func flattenBucketInfo(bucket *garage.GetBucketInfoResponse) interface{} {
	b := map[string]interface{}{}
	b["global_aliases"] = bucket.GlobalAliases

	b["website_access_enabled"] = bucket.WebsiteAccess

	// Website config
	if bucket.WebsiteConfig.IsSet() {
		websiteConfig := bucket.WebsiteConfig.Get()
		if websiteConfig != nil {
			b["website_config_index_document"] = websiteConfig.IndexDocument
			b["website_config_error_document"] = websiteConfig.ErrorDocument
		}
	}

	// Quotas
	if bucket.Quotas.MaxSize.IsSet() {
		b["quota_max_size"] = bucket.Quotas.MaxSize.Get()
	}
	if bucket.Quotas.MaxObjects.IsSet() {
		b["quota_max_objects"] = bucket.Quotas.MaxObjects.Get()
	}

	// Scalar values: assign directly
	b["objects"] = bucket.Objects
	b["bytes"] = bucket.Bytes
	b["unfinished_uploads"] = bucket.UnfinishedUploads

	return b
}

func resourceBucketCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	// Prepare request body
	createReq := garage.CreateBucketRequest{}
	if v, ok := d.GetOk("global_alias"); ok {
		createReq.SetGlobalAlias(v.(string))
	}
	// (Optionally support local_alias here as well)

	// Create the request builder, set the body, execute
	req := p.client.BucketAPI.CreateBucket(updateContext(ctx, p))
	req = req.CreateBucketRequest(createReq)
	resp, httpResp, err := req.Execute()
	if err != nil {
		diags = append(diags, createDiagnositc(err, httpResp))
		return diags
	}

	// Set Terraform resource ID to Garage bucket's ID
	d.SetId(resp.Id)

	// Optionally, update additional properties after creation
	return resourceBucketUpdate(ctx, d, m)
}

func resourceBucketRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	bucketID := d.Id()

	// Build the request: use .Id(bucketID) builder
	req := p.client.BucketAPI.GetBucketInfo(updateContext(ctx, p)).Id(bucketID)

	bucketInfo, httpResp, err := req.Execute()
	if err != nil {
		diags = append(diags, createDiagnositc(err, httpResp))
		return diags
	}

	// Map the response to Terraform state
	for key, value := range flattenBucketInfo(bucketInfo).(map[string]interface{}) {
		if err := d.Set(key, value); err != nil {
			return diag.FromErr(err)
		}
	}

	return diags
}

func resourceBucketUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	// Website config
	var websiteAccess *garage.UpdateBucketWebsiteAccess
	if webAccessEnabledVal, ok := d.GetOk("website_access_enabled"); ok {
		enabled := webAccessEnabledVal.(bool)
		// Only build this struct if enabled
		if enabled {
			indexDoc := ""
			if v, ok := d.GetOk("website_config_index_document"); ok {
				indexDoc = v.(string)
			}
			// If enabled, indexDocument is required
			if indexDoc == "" {
				return diag.Errorf("website_config_index_document is required when website_access_enabled is true")
			}
			var errorDoc *string
			if v, ok := d.GetOk("website_config_error_document"); ok {
				doc := v.(string)
				errorDoc = &doc
			}
			websiteAccess = &garage.UpdateBucketWebsiteAccess{
				Enabled:       enabled,
				IndexDocument: *garage.NewNullableString(&indexDoc),
				ErrorDocument: *garage.NewNullableString(errorDoc),
			}
		} else {
			// Disabled: don’t send indexDocument/errorDocument
			websiteAccess = &garage.UpdateBucketWebsiteAccess{
				Enabled: enabled,
			}
		}
	}

	// Quotas (both must be present, or both must be null)
	var quotas *garage.ApiBucketQuotas

	// Check for both fields set
	maxSizeSet := d.Get("quota_max_size") != nil
	maxObjectsSet := d.Get("quota_max_objects") != nil

	if maxSizeSet && maxObjectsSet {
		maxSize := int64(d.Get("quota_max_size").(int))
		maxObjects := int64(d.Get("quota_max_objects").(int))
		quotas = &garage.ApiBucketQuotas{
			MaxSize:    *garage.NewNullableInt64(&maxSize),
			MaxObjects: *garage.NewNullableInt64(&maxObjects),
		}
	} else if !maxSizeSet && !maxObjectsSet {
		// quotas = nil, don't send quota changes
	} else {
		return diag.Errorf("Both quota_max_size and quota_max_objects must be set, or neither (to unset quotas)")
	}

	updateReq := garage.UpdateBucketRequestBody{}
	if websiteAccess != nil {
		updateReq.WebsiteAccess = *garage.NewNullableUpdateBucketWebsiteAccess(websiteAccess)
	}
	if quotas != nil {
		updateReq.Quotas = *garage.NewNullableApiBucketQuotas(quotas)
	}

	req := p.client.BucketAPI.UpdateBucket(updateContext(ctx, p), d.Id())
	req = req.UpdateBucketRequestBody(updateReq)
	_, httpResp, err := req.Execute()
	if err != nil {
		diags = append(diags, createDiagnositc(err, httpResp))
		return diags
	}

	return resourceBucketRead(ctx, d, m)
}

func resourceBucketDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	req := p.client.BucketAPI.DeleteBucket(updateContext(ctx, p), d.Id())
	httpResp, err := req.Execute()
	if err != nil {
		diags = append(diags, createDiagnositc(err, httpResp))
		return diags
	}

	return diags
}
