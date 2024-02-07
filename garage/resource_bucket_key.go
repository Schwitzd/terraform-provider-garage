package garage

import (
	"context"
	"fmt"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func schemaBucketKey() map[string]*schema.Schema {
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
		"read": {
			Type:     schema.TypeBool,
			Optional: true,
			Default:  false,
		},
		"write": {
			Type:     schema.TypeBool,
			Optional: true,
			Default:  false,
		},
		"owner": {
			Type:     schema.TypeBool,
			Optional: true,
			Default:  false,
		},
	}
}

func resourceBucketKey() *schema.Resource {
	return &schema.Resource{
		Description:   "This resource can be used to manage Garage bucket global aliases.",
		CreateContext: resourceBucketKeyCreateOrUpdate,
		ReadContext:   resourceBucketKeyRead,
		UpdateContext: resourceBucketKeyCreateOrUpdate,
		DeleteContext: resourceBucketKeyDelete,
		Schema:        schemaBucketKey(),
	}
}

func resourceBucketKeyCreateOrUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	bucketID := d.Get("bucket_id").(string)
	accessKeyID := d.Get("access_key_id").(string)
	read := d.Get("read").(bool)
	write := d.Get("write").(bool)
	owner := d.Get("owner").(bool)

	allowBucketKeyRequest := garage.AllowBucketKeyRequest{
		BucketId:    bucketID,
		AccessKeyId: accessKeyID,
		Permissions: garage.AllowBucketKeyRequestPermissions{
			Read:  read,
			Write: write,
			Owner: owner,
		},
	}
	denyBucketKeyRequest := garage.AllowBucketKeyRequest{
		BucketId:    bucketID,
		AccessKeyId: accessKeyID,
		Permissions: garage.AllowBucketKeyRequestPermissions{
			Read:  !read,
			Write: !write,
			Owner: !owner,
		},
	}

	_, http, err := p.client.BucketApi.AllowBucketKey(updateContext(ctx, p)).AllowBucketKeyRequest(allowBucketKeyRequest).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}
	_, http, err = p.client.BucketApi.DenyBucketKey(updateContext(ctx, p)).AllowBucketKeyRequest(denyBucketKeyRequest).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}

	d.SetId(fmt.Sprintf("%s/%s", bucketID, accessKeyID))

	return diags
}

func resourceBucketKeyRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	// Noop
	return diags
}

func resourceBucketKeyDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	bucketID := d.Get("bucket_id").(string)
	accessKeyID := d.Get("access_key_id").(string)

	denyBucketKeyRequest := garage.AllowBucketKeyRequest{
		BucketId:    bucketID,
		AccessKeyId: accessKeyID,
		Permissions: garage.AllowBucketKeyRequestPermissions{
			Read:  true,
			Write: true,
			Owner: true,
		},
	}

	_, http, err := p.client.BucketApi.DenyBucketKey(updateContext(ctx, p)).AllowBucketKeyRequest(denyBucketKeyRequest).Execute()
	if err != nil {
		diags = append(diags, CreateDiagnositc(err, http))
		return diags
	}

	return diags
}
