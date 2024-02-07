package garage

import (
	"context"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/thoas/go-funk"
)

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
		// Computed
		"global_aliases": {
			Type: schema.TypeList,
			Elem: &schema.Schema{
				Type: schema.TypeString,
			},
			Computed: true,
		},
		"keys": {
			Type:     schema.TypeSet,
			Computed: true,
			Elem: &schema.Resource{
				Schema: map[string]*schema.Schema{
					"access_key_id": {
						Type:     schema.TypeString,
						Computed: true,
					},
					"name": {
						Type:     schema.TypeString,
						Computed: true,
					},
					"permissions_read": {
						Type:     schema.TypeBool,
						Computed: true,
					},
					"permissions_write": {
						Type:     schema.TypeBool,
						Computed: true,
					},
					"permissions_owner": {
						Type:     schema.TypeBool,
						Computed: true,
					},
					"local_aliases": {
						Type: schema.TypeList,
						Elem: &schema.Schema{
							Type: schema.TypeString,
						},
						Computed: true,
					},
				},
			},
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

func resourceBucket() *schema.Resource {
	return &schema.Resource{
		Description:   "This resource can be used to manage Garage buckets.",
		CreateContext: resourceBucketCreate,
		ReadContext:   resourceBucketRead,
		UpdateContext: resourceBucketUpdate,
		DeleteContext: resourceBucketDelete,
		Schema:        schemaBucket(),
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
	}
}

func flattenBucketKey(bucketKey garage.BucketKeyInfo) interface{} {
	return map[string]interface{}{
		"access_key_id":     *bucketKey.AccessKeyId,
		"name":              *bucketKey.Name,
		"permissions_read":  *bucketKey.Permissions.Read,
		"permissions_write": *bucketKey.Permissions.Write,
		"permissions_owner": *bucketKey.Permissions.Owner,
	}
}

func flattenBucketInfo(bucket *garage.BucketInfo) interface{} {
	b := map[string]interface{}{}
	b["global_aliases"] = bucket.GlobalAliases

	if bucket.HasWebsiteAccess() {
		b["website_access_enabled"] = bucket.GetWebsiteAccess()
	}

	if bucket.HasWebsiteConfig() {
		b["website_config_index_document"] = bucket.GetWebsiteConfig().IndexDocument
		b["website_config_error_document"] = bucket.GetWebsiteConfig().ErrorDocument
	}

	if bucket.HasQuotas() {
		if bucket.Quotas.MaxSize.IsSet() {
			b["quota_max_size"] = bucket.GetQuotas().MaxSize.Get()
		}
		if bucket.Quotas.MaxObjects.IsSet() {
			b["quota_max_objects"] = bucket.GetQuotas().MaxObjects.Get()
		}
	}

	b["keys"] = funk.Map(bucket.GetKeys(), flattenBucketKey)

	b["objects"] = *bucket.Objects
	b["bytes"] = *bucket.Bytes
	b["unfinished_uploads"] = *bucket.UnfinishedUploads

	return b
}

func resourceBucketCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	bucketInfo, http, err := p.client.BucketApi.CreateBucket(updateContext(ctx, p)).CreateBucketRequest(garage.CreateBucketRequest{}).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}

	d.SetId(*bucketInfo.Id)

	diags = resourceBucketUpdate(ctx, d, m)

	return diags
}

func resourceBucketRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	bucketID := d.Id()

	bucketInfo, http, err := p.client.BucketApi.GetBucketInfo(updateContext(ctx, p)).Id(bucketID).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}

	for key, value := range flattenBucketInfo(bucketInfo).(map[string]interface{}) {
		err := d.Set(key, value)
		if err != nil {
			return diag.FromErr(err)
		}
	}

	return diags
}

func resourceBucketUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	webAccessEnabled := false
	var webConfigIndexDoc *string
	webConfigIndexDoc = nil
	var webConfigErrorDoc *string
	webConfigErrorDoc = nil
	if webAccessEnabledVal, ok := d.GetOk("website_access_enabled"); ok {
		webAccessEnabled = webAccessEnabledVal.(bool)
	}
	if webConfigIndexDocVal, ok := d.GetOk("website_config_index_document"); ok {
		webConfigIndexDocVal := webConfigIndexDocVal.(string)
		webConfigIndexDoc = &webConfigIndexDocVal
	}
	if webConfigErrorDocVal, ok := d.GetOk("website_config_error_document"); ok {
		webConfigErrorDocVal := webConfigErrorDocVal.(string)
		webConfigErrorDoc = &webConfigErrorDocVal
	}

	var quotaMaxSize *int64
	quotaMaxSize = nil
	var quotaMaxObjects *int64
	quotaMaxObjects = nil
	if quotaMaxSizeVal, ok := d.GetOk("quota_max_size"); ok {
		quotaMaxSizeVal := int64(quotaMaxSizeVal.(int))
		quotaMaxSize = &quotaMaxSizeVal
	}
	if quotaMaxObjectsVal, ok := d.GetOk("quota_max_objects"); ok {
		quotaMaxObjectsVal := int64(quotaMaxObjectsVal.(int))
		quotaMaxObjects = &quotaMaxObjectsVal
	}

	updateBucketRequest := garage.UpdateBucketRequest{
		WebsiteAccess: &garage.UpdateBucketRequestWebsiteAccess{
			Enabled:       &webAccessEnabled,
			IndexDocument: webConfigIndexDoc,
			ErrorDocument: webConfigErrorDoc,
		},
		Quotas: &garage.UpdateBucketRequestQuotas{
			MaxSize:    *garage.NewNullableInt64(quotaMaxSize),
			MaxObjects: *garage.NewNullableInt64(quotaMaxObjects),
		},
	}

	_, http, err := p.client.BucketApi.UpdateBucket(updateContext(ctx, p)).Id(d.Id()).UpdateBucketRequest(updateBucketRequest).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}

	diags = resourceBucketRead(ctx, d, m)

	return diags
}

func resourceBucketDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	http, err := p.client.BucketApi.DeleteBucket(updateContext(ctx, p)).Id(d.Id()).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}

	return diags
}
