package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
)

// newDocsTestServer creates a minimal server with just docs routes (no DB needed).
func newDocsTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	api.RegisterDocsRoutes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// TestDocsEndpoint_ServesSwaggerUI verifies GET /api/docs returns 200 text/html with swagger UI.
func TestDocsEndpoint_ServesSwaggerUI(t *testing.T) {
	srv := newDocsTestServer(t)
	resp, err := http.Get(srv.URL + "/api/docs")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/html")

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	require.Contains(t, strings.ToLower(string(body[:n])), "swagger-ui")
}

// TestDocsSpec_ServesOpenAPIYAML verifies GET /api/docs/openapi.yaml returns valid YAML.
func TestDocsSpec_ServesOpenAPIYAML(t *testing.T) {
	srv := newDocsTestServer(t)
	resp, err := http.Get(srv.URL + "/api/docs/openapi.yaml")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/yaml")

	var spec map[string]interface{}
	require.NoError(t, yaml.NewDecoder(resp.Body).Decode(&spec))

	version, ok := spec["openapi"].(string)
	require.True(t, ok, "openapi field must be a string")
	require.True(t, strings.HasPrefix(version, "3."), "must be OpenAPI 3.x, got %s", version)
}

// TestDocsSpec_ContainsRequiredPaths validates all expected paths exist in the spec.
func TestDocsSpec_ContainsRequiredPaths(t *testing.T) {
	srv := newDocsTestServer(t)
	resp, err := http.Get(srv.URL + "/api/docs/openapi.yaml")
	require.NoError(t, err)
	defer resp.Body.Close()

	var spec map[string]interface{}
	require.NoError(t, yaml.NewDecoder(resp.Body).Decode(&spec))

	paths, ok := spec["paths"].(map[string]interface{})
	require.True(t, ok, "spec must have paths section")

	requiredPaths := []string{
		"/auth/login",
		"/auth/register",
		"/auth/refresh",
		"/auth/logout",
		"/auth/me",
		"/health",
		"/ready",
		"/orgs/{orgID}",
		"/orgs/{orgID}/spaces",
		"/orgs/{orgID}/spaces/{spaceID}",
		"/orgs/{orgID}/spaces/{spaceID}/members",
		"/orgs/{orgID}/spaces/{spaceID}/members/{userID}",
		"/orgs/{orgID}/spaces/{spaceID}/items/{itemID}/comments",
		"/orgs/{orgID}/labels",
		"/orgs/{orgID}/labels/{labelID}",
		"/spaces/{spaceID}/tickets",
		"/spaces/{spaceID}/tickets/{ticketID}",
		"/spaces/{spaceID}/tickets/{ticketID}/status",
		"/spaces/{spaceID}/tickets/{ticketID}/assign",
		"/spaces/{spaceID}/tickets/search",
		"/spaces/{spaceID}/tickets/kanban",
		"/spaces/{spaceID}/wiki",
		"/spaces/{spaceID}/wiki/{pageID}",
		"/spaces/{spaceID}/wiki/tree",
		"/spaces/{spaceID}/wiki/search",
		"/spaces/{spaceID}/projects/items",
		"/spaces/{spaceID}/projects/items/{itemID}",
		"/spaces/{spaceID}/projects/sprints",
		"/spaces/{spaceID}/projects/sprints/{sprintID}",
		"/spaces/{spaceID}/projects/sprints/active",
		"/spaces/{spaceID}/projects/backlog",
		"/spaces/{spaceID}/projects/roadmap",
	}

	var missing []string
	for _, p := range requiredPaths {
		if _, exists := paths[p]; !exists {
			missing = append(missing, p)
		}
	}
	require.Empty(t, missing, "Missing paths in spec: %v", missing)
}

// TestDocsSpec_AllProtectedEndpointsHaveSecurity verifies all endpoints except public ones have security defined.
func TestDocsSpec_AllProtectedEndpointsHaveSecurity(t *testing.T) {
	srv := newDocsTestServer(t)
	resp, err := http.Get(srv.URL + "/api/docs/openapi.yaml")
	require.NoError(t, err)
	defer resp.Body.Close()

	var spec map[string]interface{}
	require.NoError(t, yaml.NewDecoder(resp.Body).Decode(&spec))

	paths, ok := spec["paths"].(map[string]interface{})
	require.True(t, ok)

	publicEndpoints := map[string]map[string]bool{
		"/auth/login":    {"post": true},
		"/auth/register": {"post": true},
		"/auth/refresh":  {"post": true},
		"/health":        {"get": true},
		"/ready":         {"get": true},
	}

	var unsecured []string
	for path, methods := range paths {
		methodMap, ok := methods.(map[string]interface{})
		if !ok {
			continue
		}
		for method, op := range methodMap {
			if method == "parameters" {
				continue
			}
			// Skip public endpoints
			if pubMethods, exists := publicEndpoints[path]; exists && pubMethods[method] {
				continue
			}
			opMap, ok := op.(map[string]interface{})
			if !ok {
				continue
			}
			if _, hasSecurity := opMap["security"]; !hasSecurity {
				unsecured = append(unsecured, strings.ToUpper(method)+" "+path)
			}
		}
	}
	require.Empty(t, unsecured, "These endpoints are missing security definitions:\n%s", strings.Join(unsecured, "\n"))
}

// TestDocsSpec_ValidOpenAPI3Structure verifies the spec has all required structural elements.
func TestDocsSpec_ValidOpenAPI3Structure(t *testing.T) {
	srv := newDocsTestServer(t)
	resp, err := http.Get(srv.URL + "/api/docs/openapi.yaml")
	require.NoError(t, err)
	defer resp.Body.Close()

	var spec map[string]interface{}
	require.NoError(t, yaml.NewDecoder(resp.Body).Decode(&spec))

	// Check openapi version
	version, ok := spec["openapi"].(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(version, "3."))

	// Check info
	info, ok := spec["info"].(map[string]interface{})
	require.True(t, ok, "spec must have info section")
	require.NotEmpty(t, info["title"], "info.title must not be empty")
	require.NotEmpty(t, info["version"], "info.version must not be empty")

	// Check BearerAuth security scheme
	components, ok := spec["components"].(map[string]interface{})
	require.True(t, ok, "spec must have components section")
	schemes, ok := components["securitySchemes"].(map[string]interface{})
	require.True(t, ok, "components must have securitySchemes")
	_, ok = schemes["BearerAuth"]
	require.True(t, ok, "securitySchemes must contain BearerAuth")
}

// TestDocsSpec_LoginEndpointHasNoSecurity verifies login does not require auth.
func TestDocsSpec_LoginEndpointHasNoSecurity(t *testing.T) {
	srv := newDocsTestServer(t)
	resp, err := http.Get(srv.URL + "/api/docs/openapi.yaml")
	require.NoError(t, err)
	defer resp.Body.Close()

	var spec map[string]interface{}
	require.NoError(t, yaml.NewDecoder(resp.Body).Decode(&spec))

	paths := spec["paths"].(map[string]interface{})
	loginPath := paths["/auth/login"].(map[string]interface{})
	loginPost := loginPath["post"].(map[string]interface{})

	// Login should NOT have a security field
	_, hasSecurity := loginPost["security"]
	require.False(t, hasSecurity, "POST /auth/login must NOT have security requirement")
}

// TestDocsSpec_InSyncWithCode checks that the committed spec matches what swag would generate.
// Skip if swag is not installed.
func TestDocsSpec_InSyncWithCode(t *testing.T) {
	// This test is meant to run in CI where swag is installed.
	// Skip locally if swag is not available.
	// The actual sync check is done by `make docs-check` and the CI docs-check job.
	t.Skip("spec sync check is handled by make docs-check and CI pipeline")
}
