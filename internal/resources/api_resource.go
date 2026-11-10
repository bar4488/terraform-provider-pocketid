package resources

import (
	"context"
	"fmt"
	"net/url"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	_ resource.Resource                = &apiResource{}
	_ resource.ResourceWithConfigure   = &apiResource{}
	_ resource.ResourceWithImportState = &apiResource{}
)

// NewAPIResource returns the pocketid_api resource.
func NewAPIResource() resource.Resource {
	return &apiResource{}
}

type apiResource struct {
	client *client.Client
}

type apiResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Resource    types.String `tfsdk:"resource"`
	Permissions types.Map    `tfsdk:"permissions"`
}

type apiPermissionModel struct {
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

var apiPermissionAttrTypes = map[string]attr.Type{
	"name":        types.StringType,
	"description": types.StringType,
}

func (r *apiResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api"
}

func (r *apiResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an OAuth API (resource server) in Pocket-ID. Requires Pocket ID >= " + client.MinVersionAPIs + ".",
		MarkdownDescription: "Manages an OAuth API (resource server) in Pocket-ID. Access tokens issued for the API carry its " +
			"`resource` as the audience and the granted permission keys as scopes. Which clients may request which " +
			"permissions is managed with `pocketid_api_client_grant`. Requires Pocket ID >= " + client.MinVersionAPIs + ".",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The ID of the API.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The display name of the API.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 50),
				},
			},
			"resource": schema.StringAttribute{
				Description: "The resource identifier (RFC 8707): an absolute URI that clients pass as `resource` and tokens " +
					"carry as `aud`. Pocket ID cannot change it, so changing it replaces the API.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 350),
					absoluteURIValidator{},
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"permissions": schema.MapNestedAttribute{
				Description: "The API's permissions, keyed by permission key (the OAuth scope, e.g. `notes.read`). " +
					"Pocket ID matches them by key, so renaming or describing a permission keeps its grants; " +
					"removing a key removes it from every grant.",
				Optional: true,
				Validators: []validator.Map{
					mapvalidator.KeysAre(stringvalidator.LengthBetween(1, 128)),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Description: "The display name of the permission.",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.LengthBetween(1, 50),
							},
						},
						"description": schema.StringAttribute{
							Description: "What the permission allows, shown on the consent screen.",
							Optional:    true,
							Validators: []validator.String{
								stringvalidator.LengthBetween(1, 200),
							},
						},
					},
				},
			},
		},
	}
}

func (r *apiResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *apiResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan apiResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.RequireMinVersion("pocketid_api", client.MinVersionAPIs); err != nil {
		resp.Diagnostics.AddError("Unsupported Pocket ID version", err.Error())
		return
	}

	permissions, diags := apiPermissionsToAPI(ctx, plan.Permissions)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Creating API", map[string]any{"name": plan.Name.ValueString(), "resource": plan.Resource.ValueString()})
	created, err := r.client.CreateAPI(&client.APICreateRequest{
		Name:     plan.Name.ValueString(),
		Resource: plan.Resource.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating API", "Could not create API: "+err.Error())
		return
	}

	api := created
	if len(permissions) > 0 {
		api, err = r.client.UpdateAPIPermissions(created.ID, permissions)
		if err != nil {
			_ = r.client.DeleteAPI(created.ID)
			resp.Diagnostics.AddError("Error setting API permissions",
				"Could not set the permissions of the new API, so it was deleted again: "+err.Error())
			return
		}
	}

	resp.Diagnostics.Append(r.setState(ctx, api, plan.Permissions, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *apiResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state apiResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	api, err := r.client.GetAPI(state.ID.ValueString())
	if client.IsNotFound(err) {
		tflog.Warn(ctx, "API no longer exists, removing it from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading API", "Could not read API "+state.ID.ValueString()+": "+err.Error())
		return
	}

	resp.Diagnostics.Append(r.setState(ctx, api, state.Permissions, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *apiResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state apiResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	api, err := r.client.GetAPI(id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading API", "Could not read API "+id+": "+err.Error())
		return
	}

	if !plan.Name.Equal(state.Name) {
		api, err = r.client.UpdateAPI(id, &client.APIUpdateRequest{Name: plan.Name.ValueString()})
		if err != nil {
			resp.Diagnostics.AddError("Error updating API", "Could not rename API "+id+": "+err.Error())
			return
		}
	}

	if !plan.Permissions.Equal(state.Permissions) {
		permissions, diags := apiPermissionsToAPI(ctx, plan.Permissions)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		api, err = r.client.UpdateAPIPermissions(id, permissions)
		if err != nil {
			resp.Diagnostics.AddError("Error updating API permissions", "Could not update the permissions of API "+id+": "+err.Error())
			return
		}
	}

	resp.Diagnostics.Append(r.setState(ctx, api, plan.Permissions, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *apiResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state apiResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteAPI(state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting API", "Could not delete API "+state.ID.ValueString()+": "+err.Error())
	}
}

func (r *apiResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// setState copies the API into the model. An API without permissions keeps
// whatever the configuration said (null or an empty map), so neither shows a diff.
func (r *apiResource) setState(ctx context.Context, api *client.API, prior types.Map, m *apiResourceModel) diag.Diagnostics {
	m.ID = types.StringValue(api.ID)
	m.Name = types.StringValue(api.Name)
	m.Resource = types.StringValue(api.Resource)
	if len(api.Permissions) == 0 && (prior.IsNull() || len(prior.Elements()) == 0) && !prior.IsUnknown() {
		m.Permissions = prior
		return nil
	}
	permissions, diags := apiPermissionsToState(ctx, api.Permissions)
	m.Permissions = permissions
	return diags
}

func apiPermissionsToAPI(ctx context.Context, m types.Map) ([]client.APIPermissionInput, diag.Diagnostics) {
	if m.IsNull() || m.IsUnknown() {
		return []client.APIPermissionInput{}, nil
	}
	var permissions map[string]apiPermissionModel
	diags := m.ElementsAs(ctx, &permissions, false)
	if diags.HasError() {
		return nil, diags
	}
	keys := make([]string, 0, len(permissions))
	for key := range permissions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]client.APIPermissionInput, 0, len(keys))
	for _, key := range keys {
		p := permissions[key]
		in := client.APIPermissionInput{Key: key, Name: p.Name.ValueString()}
		if !p.Description.IsNull() && !p.Description.IsUnknown() {
			d := p.Description.ValueString()
			in.Description = &d
		}
		out = append(out, in)
	}
	return out, diags
}

func apiPermissionsToState(ctx context.Context, permissions []client.APIPermission) (types.Map, diag.Diagnostics) {
	objectType := types.ObjectType{AttrTypes: apiPermissionAttrTypes}
	values := make(map[string]apiPermissionModel, len(permissions))
	for _, p := range permissions {
		description := types.StringNull()
		if p.Description != nil && *p.Description != "" {
			description = types.StringValue(*p.Description)
		}
		values[p.Key] = apiPermissionModel{Name: types.StringValue(p.Name), Description: description}
	}
	return types.MapValueFrom(ctx, objectType, values)
}

// absoluteURIValidator checks that a string is an absolute URI (scheme and
// host or opaque part), as RFC 8707 requires for resource indicators.
type absoluteURIValidator struct{}

func (v absoluteURIValidator) Description(_ context.Context) string {
	return "value must be an absolute URI without a fragment"
}

func (v absoluteURIValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v absoluteURIValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	u, err := url.Parse(req.ConfigValue.ValueString())
	if err != nil || !u.IsAbs() || u.Fragment != "" || (u.Host == "" && u.Opaque == "") {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid resource identifier",
			fmt.Sprintf("%q must be an absolute URI without a fragment, e.g. https://api.example.com", req.ConfigValue.ValueString()))
	}
}
