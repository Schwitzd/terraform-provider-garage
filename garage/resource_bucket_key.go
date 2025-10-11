package garage

import (
	"context"
	"fmt"
	"net/http"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type bucketKeyPermissions struct {
	Owner bool
	Read  bool
	Write bool
}

func (p bucketKeyPermissions) any() bool {
	return p.Owner || p.Read || p.Write
}

// resourceBucketKey manages permissions granted to an access key on a bucket.
func resourceBucketKey() *schema.Resource {
	return &schema.Resource{
		Description:   "Manage permissions granted to an access key on a Garage bucket.",
		CreateContext: resourceBucketKeyCreate,
		ReadContext:   resourceBucketKeyRead,
		UpdateContext: resourceBucketKeyUpdate,
		DeleteContext: resourceBucketKeyDelete,
		Schema: map[string]*schema.Schema{
			"bucket_id": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "ID of the target bucket (UUID).",
			},
			"access_key_id": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Access key ID that should receive the permissions.",
			},
			"read": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Allow the key to read objects from the bucket.",
			},
			"write": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Allow the key to write (create/update/delete) objects in the bucket.",
			},
			"owner": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Grant owner permissions on the bucket (full administrative control).",
			},
			"key_name": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Human-friendly name of the access key, if available.",
			},
		},
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		CustomizeDiff: func(ctx context.Context, d *schema.ResourceDiff, _ interface{}) error {
			perms := bucketKeyPermissions{
				Read:  d.Get("read").(bool),
				Write: d.Get("write").(bool),
				Owner: d.Get("owner").(bool),
			}
			if !perms.any() {
				return fmt.Errorf("at least one of read, write, or owner must be true")
			}
			return nil
		},
	}
}

func resourceBucketKeyCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	desired := desiredBucketKeyPermissions(d)
	if !desired.any() {
		return diag.Diagnostics{{
			Severity: diag.Error,
			Summary:  "invalid bucket-key permissions",
			Detail:   "at least one of read, write, or owner must be set to true",
		}}
	}
	bucketID := d.Get("bucket_id").(string)
	keyID := d.Get("access_key_id").(string)

	if diags := ensureBucketKeyPermissions(ctx, p, bucketID, keyID, desired); len(diags) > 0 {
		return diags
	}

	d.SetId(fmt.Sprintf("%s:%s", bucketID, keyID))
	return resourceBucketKeyRead(ctx, d, m)
}

func resourceBucketKeyRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	bucketID := d.Get("bucket_id").(string)
	keyID := d.Get("access_key_id").(string)

	state, keyName, found, diags := fetchBucketKeyState(ctx, p, bucketID, keyID)
	if len(diags) > 0 {
		return diags
	}

	if !found {
		d.SetId("")
		return nil
	}

	_ = d.Set("bucket_id", bucketID)
	_ = d.Set("access_key_id", keyID)
	_ = d.Set("read", state.Read)
	_ = d.Set("write", state.Write)
	_ = d.Set("owner", state.Owner)
	_ = d.Set("key_name", keyName)

	return nil
}

func resourceBucketKeyUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	if !(d.HasChange("read") || d.HasChange("write") || d.HasChange("owner")) {
		return resourceBucketKeyRead(ctx, d, m)
	}

	bucketID := d.Get("bucket_id").(string)
	keyID := d.Get("access_key_id").(string)
	desired := desiredBucketKeyPermissions(d)
	if !desired.any() {
		return diag.Diagnostics{{
			Severity: diag.Error,
			Summary:  "invalid bucket-key permissions",
			Detail:   "at least one of read, write, or owner must remain true; remove the resource to revoke all permissions",
		}}
	}

	if diags := ensureBucketKeyPermissions(ctx, p, bucketID, keyID, desired); len(diags) > 0 {
		return diags
	}

	return resourceBucketKeyRead(ctx, d, m)
}

func resourceBucketKeyDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	p := m.(*garageProvider)

	bucketID := d.Get("bucket_id").(string)
	keyID := d.Get("access_key_id").(string)

	current, _, found, diags := fetchBucketKeyState(ctx, p, bucketID, keyID)
	if len(diags) > 0 {
		return diags
	}
	if !found {
		d.SetId("")
		return nil
	}

	deny := garage.NewApiBucketKeyPerm()
	if current.Read {
		deny.SetRead(true)
	}
	if current.Write {
		deny.SetWrite(true)
	}
	if current.Owner {
		deny.SetOwner(true)
	}

	if hasAnyBucketKeyPerm(deny) {
		if diags := applyBucketKeyDeny(ctx, p, bucketID, keyID, deny); len(diags) > 0 {
			return diags
		}
	}

	d.SetId("")
	return nil
}

func desiredBucketKeyPermissions(d *schema.ResourceData) bucketKeyPermissions {
	return bucketKeyPermissions{
		Read:  d.Get("read").(bool),
		Write: d.Get("write").(bool),
		Owner: d.Get("owner").(bool),
	}
}

func ensureBucketKeyPermissions(ctx context.Context, p *garageProvider, bucketID, keyID string, desired bucketKeyPermissions) diag.Diagnostics {
	current, _, _, diags := fetchBucketKeyState(ctx, p, bucketID, keyID)
	if len(diags) > 0 {
		return diags
	}

	allow := garage.NewApiBucketKeyPerm()
	deny := garage.NewApiBucketKeyPerm()

	if desired.Read && !current.Read {
		allow.SetRead(true)
	}
	if !desired.Read && current.Read {
		deny.SetRead(true)
	}

	if desired.Write && !current.Write {
		allow.SetWrite(true)
	}
	if !desired.Write && current.Write {
		deny.SetWrite(true)
	}

	if desired.Owner && !current.Owner {
		allow.SetOwner(true)
	}
	if !desired.Owner && current.Owner {
		deny.SetOwner(true)
	}

	if hasAnyBucketKeyPerm(allow) {
		if diags := applyBucketKeyAllow(ctx, p, bucketID, keyID, allow); len(diags) > 0 {
			return diags
		}
	}

	if hasAnyBucketKeyPerm(deny) {
		if diags := applyBucketKeyDeny(ctx, p, bucketID, keyID, deny); len(diags) > 0 {
			return diags
		}
	}

	return nil
}

func fetchBucketKeyState(ctx context.Context, p *garageProvider, bucketID, keyID string) (bucketKeyPermissions, string, bool, diag.Diagnostics) {
	req := p.client.BucketAPI.
		GetBucketInfo(p.withToken(ctx)).
		Id(bucketID)

	info, httpResp, err := req.Execute()
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == http.StatusNotFound {
			return bucketKeyPermissions{}, "", false, nil
		}
		return bucketKeyPermissions{}, "", false, createDiagnostics(err, httpResp)
	}
	if info == nil {
		return bucketKeyPermissions{}, "", false, nil
	}

	for i := range info.Keys {
		key := info.Keys[i]
		if key.GetAccessKeyId() != keyID {
			continue
		}

		perms := key.GetPermissions()
		state := bucketKeyPermissions{
			Read:  perms.GetRead(),
			Write: perms.GetWrite(),
			Owner: perms.GetOwner(),
		}
		return state, key.GetName(), true, nil
	}

	return bucketKeyPermissions{}, "", false, nil
}

func applyBucketKeyAllow(ctx context.Context, p *garageProvider, bucketID, keyID string, perm *garage.ApiBucketKeyPerm) diag.Diagnostics {
	if !hasAnyBucketKeyPerm(perm) {
		return nil
	}

	body := garage.NewBucketKeyPermChangeRequest(keyID, bucketID, *perm)
	_, httpResp, err := p.client.PermissionAPI.
		AllowBucketKey(p.withToken(ctx)).
		Body(*body).
		Execute()
	if err != nil {
		return createDiagnostics(err, httpResp)
	}
	return nil
}

func applyBucketKeyDeny(ctx context.Context, p *garageProvider, bucketID, keyID string, perm *garage.ApiBucketKeyPerm) diag.Diagnostics {
	if !hasAnyBucketKeyPerm(perm) {
		return nil
	}

	body := garage.NewBucketKeyPermChangeRequest(keyID, bucketID, *perm)
	_, httpResp, err := p.client.PermissionAPI.
		DenyBucketKey(p.withToken(ctx)).
		Body(*body).
		Execute()
	if err != nil {
		return createDiagnostics(err, httpResp)
	}
	return nil
}

func hasAnyBucketKeyPerm(perm *garage.ApiBucketKeyPerm) bool {
	if perm == nil {
		return false
	}
	return (perm.Read != nil && *perm.Read) ||
		(perm.Write != nil && *perm.Write) ||
		(perm.Owner != nil && *perm.Owner)
}
