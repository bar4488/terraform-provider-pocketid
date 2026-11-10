package client

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// API is an OAuth API (resource server) in Pocket ID: tokens issued for it
// carry its Resource as the audience and the granted permission keys as scopes.
type API struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Resource    string          `json:"resource"`
	Permissions []APIPermission `json:"permissions"`
}

// APIPermission is one permission (scope) of an API. Its ID stays the same as
// long as its Key does.
type APIPermission struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// APICreateRequest creates an API. The resource cannot be changed afterwards.
type APICreateRequest struct {
	Name     string `json:"name"`
	Resource string `json:"resource"`
}

// APIUpdateRequest renames an API.
type APIUpdateRequest struct {
	Name string `json:"name"`
}

// APIPermissionInput is one permission in a full permission set.
type APIPermissionInput struct {
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// APIPermissionsUpdateRequest replaces the full permission set of an API.
type APIPermissionsUpdateRequest struct {
	Permissions []APIPermissionInput `json:"permissions"`
}

// APIClientGrant is what one OIDC client may do with one API: request tokens on
// behalf of a user (user-delegated) and/or as itself (client credentials),
// each limited to the listed permission IDs.
type APIClientGrant struct {
	UserDelegatedAccess        bool     `json:"userDelegatedAccess"`
	ClientAccess               bool     `json:"clientAccess"`
	UserDelegatedPermissionIDs []string `json:"userDelegatedPermissionIds"`
	ClientPermissionIDs        []string `json:"clientPermissionIds"`
}

// ClientAPIGrant is one entry of GET /api/api-access/{clientId}/apis: an API
// the client has a grant on, with the API's permissions.
type ClientAPIGrant struct {
	API API `json:"api"`
	APIClientGrant
}

// CreateAPI creates an API.
func (c *Client) CreateAPI(req *APICreateRequest) (*API, error) {
	return doJSON[API](c, "POST", "/api/apis", req)
}

// GetAPI retrieves an API with its permissions.
func (c *Client) GetAPI(apiID string) (*API, error) {
	return doJSON[API](c, "GET", "/api/apis/"+url.PathEscape(apiID), nil)
}

// UpdateAPI renames an API.
func (c *Client) UpdateAPI(apiID string, req *APIUpdateRequest) (*API, error) {
	return doJSON[API](c, "PUT", "/api/apis/"+url.PathEscape(apiID), req)
}

// UpdateAPIPermissions replaces the permission set of an API. Permissions are
// matched by key: kept keys keep their IDs (and so their grants), missing keys
// are deleted, new keys are added.
func (c *Client) UpdateAPIPermissions(apiID string, permissions []APIPermissionInput) (*API, error) {
	if permissions == nil {
		permissions = []APIPermissionInput{}
	}
	return doJSON[API](c, "PUT", "/api/apis/"+url.PathEscape(apiID)+"/permissions",
		&APIPermissionsUpdateRequest{Permissions: permissions})
}

// DeleteAPI deletes an API, its permissions and every client's grant on it.
func (c *Client) DeleteAPI(apiID string) error {
	_, err := c.doRequest("DELETE", "/api/apis/"+url.PathEscape(apiID), nil)
	return err
}

// SetAPIClientGrant creates or replaces a client's grant on one API, leaving its
// grants on other APIs untouched.
func (c *Client) SetAPIClientGrant(apiID, clientID string, grant *APIClientGrant) (*APIClientGrant, error) {
	return doJSON[APIClientGrant](c, "PUT",
		"/api/apis/"+url.PathEscape(apiID)+"/clients/"+url.PathEscape(clientID), normalizeGrant(grant))
}

// DeleteAPIClientGrant removes a client's grant on one API.
func (c *Client) DeleteAPIClientGrant(apiID, clientID string) error {
	_, err := c.doRequest("DELETE", "/api/apis/"+url.PathEscape(apiID)+"/clients/"+url.PathEscape(clientID), nil)
	return err
}

// ListClientAPIGrants lists every API the client has a grant on.
func (c *Client) ListClientAPIGrants(clientID string) ([]ClientAPIGrant, error) {
	result, err := doJSON[[]ClientAPIGrant](c, "GET", "/api/api-access/"+url.PathEscape(clientID)+"/apis", nil)
	if err != nil {
		return nil, err
	}
	return *result, nil
}

// normalizeGrant sends empty lists rather than null, which the API expects.
func normalizeGrant(g *APIClientGrant) *APIClientGrant {
	out := *g
	if out.UserDelegatedPermissionIDs == nil {
		out.UserDelegatedPermissionIDs = []string{}
	}
	if out.ClientPermissionIDs == nil {
		out.ClientPermissionIDs = []string{}
	}
	return &out
}

func doJSON[T any](c *Client, method, endpoint string, body interface{}) (*T, error) {
	respBody, err := c.doRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	var result T
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("error unmarshaling response: %w", err)
	}
	return &result, nil
}
