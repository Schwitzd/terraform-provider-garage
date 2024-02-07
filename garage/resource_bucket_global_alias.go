package garage

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func schemaBucketGlobalAlias() map[string]*schema.Schema {
	return map[string]*schema.Schema{
		"bucket_id": {
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

func resourceBucketGlobalAlias() *schema.Resource {
	return &schema.Resource{
		Description:   "This resource can be used to manage Garage bucket global aliases.",
		CreateContext: resourceBucketGlobalAliasCreate,
		ReadContext:   resourceBucketGlobalAliasRead,
		DeleteContext: resourceBucketGlobalAliasDelete,
		Schema:        schemaBucketGlobalAlias(),
	}
}

func resourceBucketGlobalAliasCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	bucketID := d.Get("bucket_id").(string)
	alias := d.Get("alias").(string)

	_, http, err := p.client.BucketApi.PutBucketGlobalAlias(updateContext(ctx, p)).Id(bucketID).Alias(alias).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}

	d.SetId(fmt.Sprintf("%s/%s", bucketID, alias))

	return diags
}

func resourceBucketGlobalAliasRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	// Noop
	return diags
}

func resourceBucketGlobalAliasDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	bucketID := d.Get("bucket_id").(string)
	alias := d.Get("alias").(string)

	_, http, err := p.client.BucketApi.DeleteBucketGlobalAlias(updateContext(ctx, p)).Id(bucketID).Alias(alias).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}

	return diags
}
