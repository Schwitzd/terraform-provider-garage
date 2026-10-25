package garage

import (
	"context"
	"fmt"
	"net/http"
	"slices"
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
			global := d.Get("global_alias").(string)
			local := d.Get("local_alias").(string)
			keyID := d.Get("access_key_id").(string)
			return validateBucketAliasInputs(global, local, keyID)
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
	case global != "" && (local != "" || keyID != ""):
		return diag.Diagnostics{{
			Severity: diag.Error,
			Summary:  "invalid alias specification",
			Detail:   "set either global_alias or (local_alias + access_key_id), not both",
		}}

	case global != "":
		// GLOBAL alias
		req := p.client.BucketAliasAPI.
			AddBucketAlias(p.withToken(ctx)).
			BucketAliasEnum(garage.BucketAliasEnumOneOfAsBucketAliasEnum(
				garage.NewBucketAliasEnumOneOf(bucketID, global)))
		_, httpResp, err := req.Execute()
		if err != nil {
			return createDiagnostics(err, httpResp)
		}
		d.SetId(fmt.Sprintf("global:%s", global))
		_ = d.Set("kind", "global")

	case local != "" && keyID != "":
		// LOCAL alias
		req := p.client.BucketAliasAPI.
			AddBucketAlias(p.withToken(ctx)).
			BucketAliasEnum(garage.BucketAliasEnumOneOf1AsBucketAliasEnum(
				garage.NewBucketAliasEnumOneOf1(keyID, bucketID, local)))
		_, httpResp, err := req.Execute()
		if err != nil {
			return createDiagnostics(err, httpResp)
		}
		d.SetId(fmt.Sprintf("local:%s:%s", keyID, local))
		_ = d.Set("kind", "local")

	default:
		return diag.Diagnostics{{
			Severity: diag.Error,
			Summary:  "invalid alias specification",
			Detail:   "provide either global_alias or (local_alias + access_key_id)",
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

	// Fetch bucket info (use per-op context with token)
	info, httpResp, err := p.client.BucketAPI.
		GetBucketInfo(p.withToken(ctx)).
		Id(bucketID).
		Execute()
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == http.StatusNotFound {
			d.SetId("")
			return nil
		}
		return createDiagnostics(err, httpResp)
	}
	if info == nil {
		d.SetId("")
		return nil
	}

	switch kind {
	case "global":
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
		_, httpResp, err := p.client.BucketAliasAPI.
			RemoveBucketAlias(p.withToken(ctx)).
			BucketAliasEnum(garage.BucketAliasEnumOneOfAsBucketAliasEnum(
				garage.NewBucketAliasEnumOneOf(bucketID, alias))).
			Execute()
		if err != nil {
			if httpResp != nil && httpResp.StatusCode == http.StatusNotFound {
				return nil
			}
			return createDiagnostics(err, httpResp)
		}

	case "local":
		// optional: guard against malformed ID
		if keyID == "" || alias == "" {
			d.SetId("")
			return nil
		}
		_, httpResp, err := p.client.BucketAliasAPI.
			RemoveBucketAlias(p.withToken(ctx)).
			BucketAliasEnum(garage.BucketAliasEnumOneOf1AsBucketAliasEnum(
				garage.NewBucketAliasEnumOneOf1(keyID, bucketID, alias))).
			Execute()
		if err != nil {
			if httpResp != nil && httpResp.StatusCode == http.StatusNotFound {
				return nil
			}
			return createDiagnostics(err, httpResp)
		}

	default:
		// unknown kind -> treat as already gone
		d.SetId("")
		return nil
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

func validateBucketAliasInputs(global, local, keyID string) error {
	hasGlobal := global != ""
	hasLocal := local != "" || keyID != ""

	if !hasGlobal && !hasLocal {
		return fmt.Errorf("must specify either `global_alias` or (`local_alias` + `access_key_id`)")
	}
	if hasGlobal && hasLocal {
		return fmt.Errorf("`global_alias` conflicts with `local_alias`/`access_key_id`")
	}
	return nil
}

// keyMatchesAccessKeyID reports whether the GetBucketInfoKey belongs to the
// given access key ID.
func keyMatchesAccessKeyID(k garage.GetBucketInfoKey, want string) bool {
	return k.AccessKeyId == want
}

// keyHasLocalAlias reports whether the GetBucketInfoKey carries the given
// local alias. The SDK exposes them under BucketLocalAliases ([]string).
func keyHasLocalAlias(k garage.GetBucketInfoKey, alias string) bool {
	return slices.Contains(k.GetBucketLocalAliases(), alias)
}
