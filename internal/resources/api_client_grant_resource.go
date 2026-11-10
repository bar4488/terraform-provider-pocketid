package resources

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/Trozz/terraform-provider-pocketid/internal/client"
)

var (
	_ resource.Resource                     = &apiClientGrantResource{}
	_ resource.ResourceWithConfigure        = &apiClientGrantResource{}
	_ resource.ResourceWithImportState      = &apiClientGrantResource{}
	_ resource.ResourceWithConfigValidators = &apiClientGrantResource{}
)

// NewAPIClientGrantResource returns the pocketid_api_client_grant resource.
func NewAPIClientGrantResource() resource.Resource {
	return &apiClientGrantResource{}
}

type apiClientGrantResource struct {
	client *client.Client
}

type apiClientGrantResourceModel struct {
	ID                       types.String `tfsdk:"id"`
	APIID                    types.String `tfsdk:"api_id"`
	ClientID                 types.String `tfsdk:"client_id"`
	UserDelegatedPermissions types.Set    `tfsdk:"user_delegated_permissions"`
	ClientPermissions        types.Set    `tfsdk:"client_permissions"`
}

func (r *apiClientGrantResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_client_grant"
}

func (r *apiClientGrantResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Grants an OIDC client access to one API in Pocket-ID. Requires Pocket ID >= " + client.MinVersionAPIClientGrants + ".",
		MarkdownDescription: "Grants an OIDC client access to one API (`pocketid_api`) in Pocket-ID: which permissions it may " +
			"request on behalf of a logged-in user, and which it may request as itself with the client credentials grant. " +
			"The client's grants on other APIs are left untouched. Requires Pocket ID >= " + client.MinVersionAPIClientGrants + ".",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The ID of the grant, `<api_id>/<client_id>`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"api_id": schema.StringAttribute{
				Description: "The ID of the API (`pocketid_api.id`).",
				Required:    true,
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"client_id": schema.StringAttribute{
				Description: "The ID of the OIDC client (`pocketid_client.id`).",
				Required:    true,
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_delegated_permissions": schema.SetAttribute{
				Description: "Permission keys the client may request on behalf of a logged-in user. Unset: no user-delegated " +
					"access; an empty set: access to the API without any permission.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"client_permissions": schema.SetAttribute{
				Description: "Permission keys the client may request as itself (client credentials grant). Unset: no client " +
					"access; an empty set: access to the API without any permission. Only confidential clients can use this; for a public client the " +
					"provider reports an error, as Pocket ID would not grant it.",
				Optional:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *apiClientGrantResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.AtLeastOneOf(
			path.MatchRoot("user_delegated_permissions"),
			path.MatchRoot("client_permissions"),
		),
	}
}

func (r *apiClientGrantResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *apiClientGrantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan apiClientGrantResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RequireMinVersion("pocketid_api_client_grant", client.MinVersionAPIClientGrants); err != nil {
		resp.Diagnostics.AddError("Unsupported Pocket ID version", err.Error())
		return
	}
	resp.Diagnostics.Append(r.apply(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		// Leave nothing half-applied behind a failed create.
		_ = r.client.DeleteAPIClientGrant(plan.APIID.ValueString(), plan.ClientID.ValueString())
		return
	}
	plan.ID = types.StringValue(grantID(plan.APIID.ValueString(), plan.ClientID.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *apiClientGrantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state apiClientGrantResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiID, clientID := state.APIID.ValueString(), state.ClientID.ValueString()
	grants, err := r.client.ListClientAPIGrants(clientID)
	if client.IsNotFound(err) {
		tflog.Warn(ctx, "OIDC client no longer exists, removing its API grant from state", map[string]any{"client_id": clientID})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading API grant", "Could not list the API grants of client "+clientID+": "+err.Error())
		return
	}

	var found *client.ClientAPIGrant
	for i := range grants {
		if grants[i].API.ID == apiID {
			found = &grants[i]
			break
		}
	}
	if found == nil || (!found.UserDelegatedAccess && !found.ClientAccess) {
		tflog.Warn(ctx, "API grant no longer exists, removing it from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	keyOf := make(map[string]string, len(found.API.Permissions))
	for _, p := range found.API.Permissions {
		keyOf[p.ID] = p.Key
	}
	var diags diag.Diagnostics
	state.UserDelegatedPermissions, diags = grantPermissionsToState(ctx, found.UserDelegatedAccess, found.UserDelegatedPermissionIDs, keyOf)
	resp.Diagnostics.Append(diags...)
	state.ClientPermissions, diags = grantPermissionsToState(ctx, found.ClientAccess, found.ClientPermissionIDs, keyOf)
	resp.Diagnostics.Append(diags...)
	state.ID = types.StringValue(grantID(apiID, clientID))
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *apiClientGrantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state apiClientGrantResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.apply(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *apiClientGrantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state apiClientGrantResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteAPIClientGrant(state.APIID.ValueString(), state.ClientID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting API grant", "Could not delete API grant "+state.ID.ValueString()+": "+err.Error())
	}
}

// ImportState takes `<api_id>/<client_id>`.
func (r *apiClientGrantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	apiID, clientID, ok := strings.Cut(req.ID, "/")
	if !ok || apiID == "" || clientID == "" || strings.Contains(clientID, "/") {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected <api_id>/<client_id>, got %q", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("api_id"), apiID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("client_id"), clientID)...)
}

// apply sends the planned grant, translating permission keys to the API's
// permission IDs.
func (r *apiClientGrantResource) apply(ctx context.Context, plan *apiClientGrantResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	apiID, clientID := plan.APIID.ValueString(), plan.ClientID.ValueString()

	api, err := r.client.GetAPI(apiID)
	if err != nil {
		diags.AddError("Error reading API", "Could not read API "+apiID+" to resolve permission keys: "+err.Error())
		return diags
	}
	idOf := make(map[string]string, len(api.Permissions))
	for _, p := range api.Permissions {
		idOf[p.Key] = p.ID
	}

	grant := &client.APIClientGrant{
		UserDelegatedAccess: !plan.UserDelegatedPermissions.IsNull(),
		ClientAccess:        !plan.ClientPermissions.IsNull(),
	}
	var d diag.Diagnostics
	grant.UserDelegatedPermissionIDs, d = grantPermissionIDs(ctx, path.Root("user_delegated_permissions"), plan.UserDelegatedPermissions, idOf)
	diags.Append(d...)
	grant.ClientPermissionIDs, d = grantPermissionIDs(ctx, path.Root("client_permissions"), plan.ClientPermissions, idOf)
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}

	tflog.Debug(ctx, "Setting API grant", map[string]any{"api_id": apiID, "client_id": clientID})
	applied, err := r.client.SetAPIClientGrant(apiID, clientID, grant)
	if err != nil {
		diags.AddError("Error setting API grant", fmt.Sprintf("Could not grant client %s access to API %s: %s", clientID, apiID, err))
		return diags
	}
	// Pocket ID answers with what it actually stored and silently drops what a
	// client may not have, e.g. client access for a public client.
	if problem := grantMismatch(grant, applied); problem != "" {
		diags.AddError("Pocket ID did not apply the API grant",
			fmt.Sprintf("Client %s on API %s: %s.", clientID, apiID, problem))
	}
	return diags
}

// grantMismatch describes how the applied grant differs from the requested one,
// or returns "" when they match.
func grantMismatch(want, got *client.APIClientGrant) string {
	if want.ClientAccess && !got.ClientAccess {
		return "client access (client_permissions) was not granted; only confidential clients can use the client credentials grant"
	}
	if want.UserDelegatedAccess != got.UserDelegatedAccess || want.ClientAccess != got.ClientAccess {
		return "the stored access differs from the requested access"
	}
	if !sameStrings(want.UserDelegatedPermissionIDs, got.UserDelegatedPermissionIDs) ||
		!sameStrings(want.ClientPermissionIDs, got.ClientPermissionIDs) {
		return "the stored permissions differ from the requested permissions"
	}
	return ""
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		if seen[s] == 0 {
			return false
		}
		seen[s]--
	}
	return true
}

func grantID(apiID, clientID string) string {
	return apiID + "/" + clientID
}

func grantPermissionIDs(ctx context.Context, p path.Path, keys types.Set, idOf map[string]string) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if keys.IsNull() || keys.IsUnknown() {
		return []string{}, diags
	}
	var names []string
	diags.Append(keys.ElementsAs(ctx, &names, false)...)
	ids := make([]string, 0, len(names))
	for _, key := range names {
		id, ok := idOf[key]
		if !ok {
			known := make([]string, 0, len(idOf))
			for k := range idOf {
				known = append(known, k)
			}
			sort.Strings(known)
			diags.AddAttributeError(p, "Unknown permission",
				fmt.Sprintf("The API has no permission %q. Its permissions are: %s.", key, strings.Join(known, ", ")))
			continue
		}
		ids = append(ids, id)
	}
	return ids, diags
}

func grantPermissionsToState(ctx context.Context, access bool, ids []string, keyOf map[string]string) (types.Set, diag.Diagnostics) {
	if !access {
		return types.SetNull(types.StringType), nil
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		if key, ok := keyOf[id]; ok {
			keys = append(keys, key)
		}
	}
	return types.SetValueFrom(ctx, types.StringType, keys)
}
