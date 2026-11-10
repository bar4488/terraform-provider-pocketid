package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Trozz/terraform-provider-pocketid/internal/client"
)

func strp(s string) *string { return &s }

func TestAPIPermissions_RoundTrip(t *testing.T) {
	ctx := context.Background()
	state, diags := apiPermissionsToState(ctx, []client.APIPermission{
		{ID: "p2", Key: "notes.write", Name: "Write notes"},
		{ID: "p1", Key: "notes.read", Name: "Read notes", Description: strp("See all notes")},
	})
	require.False(t, diags.HasError())

	inputs, diags := apiPermissionsToAPI(ctx, state)
	require.False(t, diags.HasError())
	assert.Equal(t, []client.APIPermissionInput{
		{Key: "notes.read", Name: "Read notes", Description: strp("See all notes")},
		{Key: "notes.write", Name: "Write notes"},
	}, inputs, "sorted by key, description only when set")
}

func TestAPIPermissionsToState_EmptyDescriptionIsNull(t *testing.T) {
	ctx := context.Background()
	state, diags := apiPermissionsToState(ctx, []client.APIPermission{{ID: "p1", Key: "a", Name: "A", Description: strp("")}})
	require.False(t, diags.HasError())
	var m map[string]apiPermissionModel
	require.False(t, state.ElementsAs(ctx, &m, false).HasError())
	assert.True(t, m["a"].Description.IsNull())
}

func TestAPIPermissionsToAPI_NullIsEmptySet(t *testing.T) {
	inputs, diags := apiPermissionsToAPI(context.Background(), types.MapNull(types.ObjectType{AttrTypes: apiPermissionAttrTypes}))
	require.False(t, diags.HasError())
	assert.Equal(t, []client.APIPermissionInput{}, inputs)
}

func TestAPISetState_KeepsNullOrEmptyPermissions(t *testing.T) {
	ctx := context.Background()
	r := &apiResource{}
	api := &client.API{ID: "api-1", Name: "Lab", Resource: "https://lab.example.com"}
	objectType := types.ObjectType{AttrTypes: apiPermissionAttrTypes}

	var m apiResourceModel
	require.False(t, r.setState(ctx, api, types.MapNull(objectType), &m).HasError())
	assert.True(t, m.Permissions.IsNull(), "unset in config stays unset")

	empty, _ := types.MapValueFrom(ctx, objectType, map[string]apiPermissionModel{})
	require.False(t, r.setState(ctx, api, empty, &m).HasError())
	assert.False(t, m.Permissions.IsNull())
	assert.Empty(t, m.Permissions.Elements(), "an empty map in config stays an empty map")
}

func TestGrantPermissionIDs(t *testing.T) {
	ctx := context.Background()
	idOf := map[string]string{"map.read": "p1", "map.write": "p2"}

	keys, _ := types.SetValueFrom(ctx, types.StringType, []string{"map.read"})
	ids, diags := grantPermissionIDs(ctx, path.Root("user_delegated_permissions"), keys, idOf)
	require.False(t, diags.HasError())
	assert.Equal(t, []string{"p1"}, ids)

	ids, diags = grantPermissionIDs(ctx, path.Root("client_permissions"), types.SetNull(types.StringType), idOf)
	require.False(t, diags.HasError())
	assert.Equal(t, []string{}, ids)

	unknown, _ := types.SetValueFrom(ctx, types.StringType, []string{"map.delete"})
	_, diags = grantPermissionIDs(ctx, path.Root("client_permissions"), unknown, idOf)
	require.True(t, diags.HasError())
	assert.Contains(t, diags.Errors()[0].Detail(), `"map.delete"`)
	assert.Contains(t, diags.Errors()[0].Detail(), "map.read, map.write")
}

func TestGrantPermissionsToState(t *testing.T) {
	ctx := context.Background()
	keyOf := map[string]string{"p1": "map.read"}

	set, diags := grantPermissionsToState(ctx, false, []string{"p1"}, keyOf)
	require.False(t, diags.HasError())
	assert.True(t, set.IsNull(), "no access is null")

	set, diags = grantPermissionsToState(ctx, true, nil, keyOf)
	require.False(t, diags.HasError())
	assert.False(t, set.IsNull())
	assert.Empty(t, set.Elements(), "access without permissions is an empty set")

	set, diags = grantPermissionsToState(ctx, true, []string{"p1"}, keyOf)
	require.False(t, diags.HasError())
	var keys []string
	require.False(t, set.ElementsAs(ctx, &keys, false).HasError())
	assert.Equal(t, []string{"map.read"}, keys)
}

func TestAbsoluteURIValidator(t *testing.T) {
	ctx := context.Background()
	for value, valid := range map[string]bool{
		"https://lab.example.com":       true,
		"https://lab.example.com/api":   true,
		"urn:example:lab":               true,
		"lab.example.com":               false,
		"/relative":                     false,
		"https://lab.example.com/#frag": false,
	} {
		resp := &validator.StringResponse{}
		absoluteURIValidator{}.ValidateString(ctx, validator.StringRequest{
			Path: path.Root("resource"), ConfigValue: types.StringValue(value),
		}, resp)
		assert.Equal(t, !valid, resp.Diagnostics.HasError(), value)
	}
}

func TestAPIClientGrantImportState(t *testing.T) {
	ctx := context.Background()
	r := &apiClientGrantResource{}
	schemaResp := &resource.SchemaResponse{}
	r.Schema(ctx, resource.SchemaRequest{}, schemaResp)
	require.False(t, schemaResp.Diagnostics.HasError())

	newState := func() tfsdk.State {
		return tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
	}

	resp := &resource.ImportStateResponse{State: newState()}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "api-1/client-1"}, resp)
	require.False(t, resp.Diagnostics.HasError())
	var apiID, clientID types.String
	resp.State.GetAttribute(ctx, path.Root("api_id"), &apiID)
	resp.State.GetAttribute(ctx, path.Root("client_id"), &clientID)
	assert.Equal(t, "api-1", apiID.ValueString())
	assert.Equal(t, "client-1", clientID.ValueString())

	for _, bad := range []string{"api-1", "/client-1", "api-1/", "a/b/c"} {
		resp := &resource.ImportStateResponse{State: newState()}
		r.ImportState(ctx, resource.ImportStateRequest{ID: bad}, resp)
		assert.True(t, resp.Diagnostics.HasError(), bad)
	}
}

func TestGrantMismatch(t *testing.T) {
	want := &client.APIClientGrant{UserDelegatedAccess: true, UserDelegatedPermissionIDs: []string{"p1", "p2"}}
	assert.Empty(t, grantMismatch(want, &client.APIClientGrant{UserDelegatedAccess: true, UserDelegatedPermissionIDs: []string{"p2", "p1"}}),
		"order does not matter")
	assert.Contains(t, grantMismatch(want, &client.APIClientGrant{UserDelegatedAccess: true, UserDelegatedPermissionIDs: []string{"p1"}}),
		"permissions differ")

	publicClient := &client.APIClientGrant{ClientAccess: true, ClientPermissionIDs: []string{}}
	assert.Contains(t, grantMismatch(publicClient, &client.APIClientGrant{ClientPermissionIDs: []string{}}),
		"only confidential clients")
}
