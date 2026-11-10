package client_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Trozz/terraform-provider-pocketid/internal/client"
)

type recorded struct {
	method, path string
	body         map[string]any
}

func apiTestServer(t *testing.T, status int, response string) (*client.Client, *[]recorded) {
	t.Helper()
	var calls []recorded
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := recorded{method: r.Method, path: r.URL.Path}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			require.NoError(t, json.Unmarshal(b, &call.body))
		}
		calls = append(calls, call)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	c, err := client.NewClient(server.URL, "test-token", false, 5)
	require.NoError(t, err)
	return c, &calls
}

const apiJSON = `{
	"id": "api-1", "name": "Lab", "resource": "https://lab.example.com",
	"permissions": [{"id": "perm-1", "key": "map.read", "name": "Read the map", "description": "See hosts"}]
}`

func TestCreateAPI(t *testing.T) {
	c, calls := apiTestServer(t, http.StatusCreated, apiJSON)
	api, err := c.CreateAPI(&client.APICreateRequest{Name: "Lab", Resource: "https://lab.example.com"})
	require.NoError(t, err)

	assert.Equal(t, "api-1", api.ID)
	assert.Equal(t, "https://lab.example.com", api.Resource)
	require.Len(t, api.Permissions, 1)
	assert.Equal(t, "map.read", api.Permissions[0].Key)
	assert.Equal(t, "See hosts", *api.Permissions[0].Description)

	require.Len(t, *calls, 1)
	assert.Equal(t, "POST", (*calls)[0].method)
	assert.Equal(t, "/api/apis", (*calls)[0].path)
	assert.Equal(t, map[string]any{"name": "Lab", "resource": "https://lab.example.com"}, (*calls)[0].body)
}

func TestUpdateAPIPermissions_SendsFullSet(t *testing.T) {
	c, calls := apiTestServer(t, http.StatusOK, apiJSON)
	_, err := c.UpdateAPIPermissions("api-1", []client.APIPermissionInput{{Key: "map.read", Name: "Read the map"}})
	require.NoError(t, err)
	assert.Equal(t, "PUT", (*calls)[0].method)
	assert.Equal(t, "/api/apis/api-1/permissions", (*calls)[0].path)
	assert.Equal(t, []any{map[string]any{"key": "map.read", "name": "Read the map"}}, (*calls)[0].body["permissions"])
}

func TestUpdateAPIPermissions_EmptySetIsAList(t *testing.T) {
	c, calls := apiTestServer(t, http.StatusOK, apiJSON)
	_, err := c.UpdateAPIPermissions("api-1", nil)
	require.NoError(t, err)
	assert.Equal(t, []any{}, (*calls)[0].body["permissions"], "removing every permission must send [], not null")
}

func TestSetAPIClientGrant_SendsEmptyListsNotNull(t *testing.T) {
	c, calls := apiTestServer(t, http.StatusOK,
		`{"userDelegatedAccess": true, "clientAccess": false, "userDelegatedPermissionIds": ["perm-1"], "clientPermissionIds": []}`)
	grant, err := c.SetAPIClientGrant("api-1", "client-1", &client.APIClientGrant{
		UserDelegatedAccess:        true,
		UserDelegatedPermissionIDs: []string{"perm-1"},
	})
	require.NoError(t, err)
	assert.True(t, grant.UserDelegatedAccess)

	call := (*calls)[0]
	assert.Equal(t, "PUT", call.method)
	assert.Equal(t, "/api/apis/api-1/clients/client-1", call.path)
	assert.Equal(t, []any{}, call.body["clientPermissionIds"])
	assert.Equal(t, false, call.body["clientAccess"])
}

func TestListClientAPIGrants(t *testing.T) {
	c, calls := apiTestServer(t, http.StatusOK, `[{
		"api": `+apiJSON+`,
		"userDelegatedAccess": true, "clientAccess": false,
		"userDelegatedPermissionIds": ["perm-1"], "clientPermissionIds": []
	}]`)
	grants, err := c.ListClientAPIGrants("client-1")
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.Equal(t, "api-1", grants[0].API.ID)
	assert.Equal(t, []string{"perm-1"}, grants[0].UserDelegatedPermissionIDs)
	assert.Equal(t, "/api/api-access/client-1/apis", (*calls)[0].path)
}

func TestDeleteAPIClientGrant(t *testing.T) {
	c, calls := apiTestServer(t, http.StatusNoContent, ``)
	require.NoError(t, c.DeleteAPIClientGrant("api-1", "client-1"))
	assert.Equal(t, "DELETE", (*calls)[0].method)
	assert.Equal(t, "/api/apis/api-1/clients/client-1", (*calls)[0].path)
}

func TestIsNotFound(t *testing.T) {
	c, _ := apiTestServer(t, http.StatusNotFound, `{"error": "API not found"}`)
	_, err := c.GetAPI("missing")
	require.Error(t, err)
	assert.True(t, client.IsNotFound(err))
	assert.Equal(t, "HTTP 404: API not found", err.Error(), "the message format is unchanged")

	c, _ = apiTestServer(t, http.StatusBadRequest, `{"error": "bad"}`)
	_, err = c.GetAPI("x")
	assert.False(t, client.IsNotFound(err))
	assert.False(t, client.IsNotFound(nil))
}
