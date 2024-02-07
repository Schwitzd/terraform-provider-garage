package garage

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func schemaBucketLocalAlias() map[string]*schema.Schema {
	return map[string]*schema.Schema{
		"bucket_id": {
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
		"access_key_id": {
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
		"alias": {
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
	}
}

func resourceBucketLocalAlias() *schema.Resource {
	return &schema.Resource{
		Description:   "This resource can be used to manage Garage bucket global aliases.",
		CreateContext: resourceBucketLocalAliasCreate,
		ReadContext:   resourceBucketLocalAliasRead,
		DeleteContext: resourceBucketLocalAliasDelete,
		Schema:        schemaBucketLocalAlias(),
	}
}

func resourceBucketLocalAliasCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	bucketID := d.Get("bucket_id").(string)
	accessKeyID := d.Get("access_key_id").(string)
	alias := d.Get("alias").(string)

	_, http, err := p.client.BucketApi.PutBucketLocalAlias(updateContext(ctx, p)).Id(bucketID).AccessKeyId(accessKeyID).Alias(alias).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}

	d.SetId(fmt.Sprintf("%s/%s/%s", bucketID, accessKeyID, alias))

	return diags
}

func resourceBucketLocalAliasRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	// Noop
	return diags
}

func resourceBucketLocalAliasDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	bucketID := d.Get("bucket_id").(string)
	accessKeyID := d.Get("access_key_id").(string)
	alias := d.Get("alias").(string)

	_, http, err := p.client.BucketApi.DeleteBucketLocalAlias(updateContext(ctx, p)).Id(bucketID).AccessKeyId(accessKeyID).Alias(alias).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}

	return diags
}
