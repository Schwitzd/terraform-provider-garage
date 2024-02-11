package garage

import (
	"context"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func schemaKey() map[string]*schema.Schema {
	return map[string]*schema.Schema{
		// Properties
		"name": {
			Description: "The name of the key.",
			Type:        schema.TypeString,
			Computed:    true,
			Optional:    true,
		},
		"access_key_id": {
			Type:     schema.TypeString,
			Computed: true,
			Optional: true,
			ForceNew: true,
		},
		"secret_access_key": {
			Type:      schema.TypeString,
			Computed:  true,
			Optional:  true,
			Sensitive: true,
			ForceNew:  true,
		},
		"permissions": {
			Type:     schema.TypeMap,
			Optional: true,
			Elem: &schema.Schema{
				Type: schema.TypeBool,
			},
			Default: map[string]bool{
				"create_bucket": false,
			},
		},
		// Computed
		// TODO: buckets
	}
}

func resourceKey() *schema.Resource {
	return &schema.Resource{
		Description:   "This resource can be used to manage Garage keys.",
		CreateContext: resourceKeyCreate,
		ReadContext:   resourceKeyRead,
		UpdateContext: resourceKeyUpdate,
		DeleteContext: resourceKeyDelete,
		Schema:        schemaKey(),
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
	}
}

func flattenKeyInfo(keyInfo *garage.KeyInfo) interface{} {
	return map[string]interface{}{
		"name":              keyInfo.Name,
		"access_key_id":     keyInfo.AccessKeyId,
		"secret_access_key": keyInfo.SecretAccessKey.Get(),
		"permissions": map[string]interface{}{
			"create_bucket": keyInfo.Permissions.CreateBucket,
		},
	}
}

func resourceKeyCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	var name *string
	name = nil
	accessKeyID := ""
	secretAccessKey := ""

	if nameVal, ok := d.GetOk("name"); ok {
		nameVal := nameVal.(string)
		name = &nameVal
	}

	if accessKeyIDVal, ok := d.GetOk("access_key_id"); ok {
		accessKeyID = accessKeyIDVal.(string)
	}

	if secretAccessKeyVal, ok := d.GetOk("access_key_id"); ok {
		secretAccessKey = secretAccessKeyVal.(string)
	}

	var keyInfo *garage.KeyInfo

	if accessKeyID != "" || secretAccessKey != "" {
		importKeyRequest := *garage.NewImportKeyRequest(*garage.NewNullableString(name), accessKeyID, secretAccessKey)
		resp, http, err := p.client.KeyApi.ImportKey(updateContext(ctx, p)).ImportKeyRequest(importKeyRequest).Execute()
		if err != nil {
			diags = append(diags, createDiagnositc(err, http))
			return diags
		}
		keyInfo = resp
	} else {
		addKeyRequest := *garage.NewAddKeyRequest()
		addKeyRequest.Name = *garage.NewNullableString(name)
		resp, http, err := p.client.KeyApi.AddKey(updateContext(ctx, p)).AddKeyRequest(addKeyRequest).Execute()
		if err != nil {
			diags = append(diags, createDiagnositc(err, http))
			return diags
		}
		keyInfo = resp
	}

	d.SetId(*keyInfo.AccessKeyId)

	if permissions, ok := d.GetOk("permissions"); ok {
		permissions := permissions.(map[string]interface{})

		allowCreateBucket := permissions["create_bucket"].(bool)
		denyCreateBucket := !permissions["create_bucket"].(bool)

		allow := garage.UpdateKeyRequestAllow{
			CreateBucket: &allowCreateBucket,
		}
		deny := garage.UpdateKeyRequestDeny{
			CreateBucket: &denyCreateBucket,
		}

		updateKeyRequest := garage.UpdateKeyRequest{
			Allow: &allow,
			Deny:  &deny,
		}

		_, http, err := p.client.KeyApi.UpdateKey(updateContext(ctx, p)).Id(d.Id()).UpdateKeyRequest(updateKeyRequest).Execute()
		if err != nil {
			diags = append(diags, createDiagnositc(err, http))
			return diags
		}
	}

	diags = resourceKeyRead(ctx, d, m)

	return diags
}

func resourceKeyRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	accessKeyID := d.Id()

	keyInfo, http, err := p.client.KeyApi.GetKey(updateContext(ctx, p)).Id(accessKeyID).ShowSecretKey("true").Execute()
	if err != nil {
		diags = append(diags, createDiagnositc(err, http))
		return diags
	}

	for key, value := range flattenKeyInfo(keyInfo).(map[string]interface{}) {
		err := d.Set(key, value)
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  err.Error(),
			})
			return diags
		}
	}

	return diags
}

func resourceKeyUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	var name *string
	name = nil
	var allow *garage.UpdateKeyRequestAllow
	allow = nil
	var deny *garage.UpdateKeyRequestDeny
	deny = nil

	if nameVal, ok := d.GetOk("name"); ok {
		nameVal := nameVal.(string)
		name = &nameVal
	}

	if permissions, ok := d.GetOk("permissions"); ok {
		permissions := permissions.(map[string]interface{})

		allowCreateBucket := permissions["create_bucket"].(bool)
		denyCreateBucket := !permissions["create_bucket"].(bool)

		allow = &garage.UpdateKeyRequestAllow{
			CreateBucket: &allowCreateBucket,
		}
		deny = &garage.UpdateKeyRequestDeny{
			CreateBucket: &denyCreateBucket,
		}
	}

	updateKeyRequest := garage.UpdateKeyRequest{
		Name:  name,
		Allow: allow,
		Deny:  deny,
	}

	_, http, err := p.client.KeyApi.UpdateKey(updateContext(ctx, p)).Id(d.Id()).UpdateKeyRequest(updateKeyRequest).Execute()
	if err != nil {
		diags = append(diags, createDiagnositc(err, http))
		return diags
	}

	diags = resourceKeyRead(ctx, d, m)

	return diags
}

func resourceKeyDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)
	var diags diag.Diagnostics

	accessKeyID := d.Id()

	http, err := p.client.KeyApi.DeleteKey(updateContext(ctx, p)).Id(accessKeyID).Execute()
	if err != nil {
		diags = append(diags, createDiagnositc(err, http))
		return diags
	}

	return diags
}
