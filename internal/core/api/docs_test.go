package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
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
	defer func() { _ = resp.Body.Close() }()

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
	defer func() { _ = resp.Body.Close() }()

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
	defer func() { _ = resp.Body.Close() }()

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
		"/orgs/{orgID}/tags",
		"/orgs/{orgID}/tags/{slug}/entities",
		"/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/tags",
		"/orgs/{orgID}/spaces/{spaceID}/tickets",
		"/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}",
		"/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/status",
		"/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/assign",
		"/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/comments",
		"/orgs/{orgID}/spaces/{spaceID}/tickets/search",
		"/orgs/{orgID}/spaces/{spaceID}/tickets/kanban",
		"/orgs/{orgID}/spaces/{spaceID}/wiki",
		"/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}",
		"/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/comments",
		"/orgs/{orgID}/spaces/{spaceID}/wiki/tree",
		"/orgs/{orgID}/spaces/{spaceID}/wiki/search",
		"/orgs/{orgID}/spaces/{spaceID}/projects/items",
		"/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}",
		"/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/comments",
		"/orgs/{orgID}/spaces/{spaceID}/projects/sprints",
		"/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}",
		"/orgs/{orgID}/spaces/{spaceID}/projects/sprints/active",
		"/orgs/{orgID}/spaces/{spaceID}/projects/backlog",
		"/orgs/{orgID}/spaces/{spaceID}/projects/roadmap",
		// P3 entity shares and attachments.
		"/orgs/{orgID}/shares",
		"/orgs/{orgID}/shared/{entityType}/{entityID}",
		"/orgs/{orgID}/spaces/{spaceID}/attachments",
		"/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/share-impact",
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
	defer func() { _ = resp.Body.Close() }()

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
		// Invite acceptance (P2.5): the raw crypto/rand token in the URL is
		// the credential, exactly like the password-reset pattern — these
		// are public by design (see route_accounting_test's public rows).
		"/invites/{token}": {"get": true},
		"/invites/accept":  {"post": true},
		// Customer portal (unauthenticated half). An external requester holds
		// no account by design (migration 044), so these three cannot carry a
		// BearerAuth requirement. Possession of the emailed magic-link token
		// is the credential — the same reasoning as the invite pair above.
		// The requester-authenticated half (/portal/{portalKey}/my/...) is
		// NOT listed here and must keep its security requirement.
		"/portal/{portalKey}":                   {"get": true},
		"/portal/{portalKey}/auth/request-link": {"post": true},
		"/portal/auth/redeem":                   {"post": true},
		// Internal-user credential links (D1). Possession of the raw crypto/rand
		// token is the credential (inspect/consume), and forgot-password is
		// reached by someone signed out — none can carry a BearerAuth
		// requirement. The authenticated email-change request and the org-admin
		// issuance routes are NOT listed and keep their security requirement.
		"/credential-links/forgot-password": {"post": true},
		"/credential-links/inspect":         {"post": true},
		"/credential-links/consume":         {"post": true},
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
	defer func() { _ = resp.Body.Close() }()

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
	defer func() { _ = resp.Body.Close() }()

	var spec map[string]interface{}
	require.NoError(t, yaml.NewDecoder(resp.Body).Decode(&spec))

	paths := spec["paths"].(map[string]interface{})
	loginPath := paths["/auth/login"].(map[string]interface{})
	loginPost := loginPath["post"].(map[string]interface{})

	// Login should NOT have a security field
	_, hasSecurity := loginPost["security"]
	require.False(t, hasSecurity, "POST /auth/login must NOT have security requirement")
}

// TestDocsSpec_EveryRouterPathIsDocumented checks the committed spec against
// the live router (T2).
//
// This replaces a test that skipped unconditionally with the reason "spec sync
// check is handled by make docs-check and CI pipeline". That was a section 2
// skip-discipline violation — no SKIP: marker, no issue, no re-enable
// condition — and the reason was not true. `make docs-check` does run swag and
// diff, but it is a local target; CI's docs job only greps the committed YAML
// for a few structural markers (openapi:, info:, title:, BearerAuth:) and
// would pass against a spec missing every path in the API.
//
// So the guard was asserted by nothing, and that is exactly how three
// page-lock operations could have gone on being published in the spec after
// their routes were deleted.
//
// N1: this test shipped with an undocumentedRoutes ledger of nineteen known
// gaps — the whole P2.5 administration surface, plus both avatar routes. The
// ledger is gone, because the gap is: sixteen of the nineteen turned out to be
// annotated all along but under snake_case path placeholders ({org_id}) where
// the router and the other eighty-eight documented paths use {orgID}, so the
// spec described endpoints at names nothing served and this test could not
// match them; the remaining three, the avatars, had no annotations at all and
// sat in a package the docs target never even scanned. Both are fixed, and
// with nothing left to exempt the exemption mechanism is gone too — a ledger
// that can be added to is a ledger that will be.
//
// Regenerating with swag from inside a test is the wrong shape — it shells
// out, writes the repo, and needs a toolchain the test cannot assume. The
// drift that actually matters is one-directional and checkable without it:
// every path the router serves must appear in the committed spec. A path
// documented but not served is the weaker failure and is left to
// `make docs-check`, which this PR ran.
func TestDocsSpec_EveryRouterPathIsDocumented(t *testing.T) {
	docsSrv := newDocsTestServer(t)
	resp, err := http.Get(docsSrv.URL + "/api/docs/openapi.yaml")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var spec map[string]interface{}
	require.NoError(t, yaml.NewDecoder(resp.Body).Decode(&spec))

	paths, ok := spec["paths"].(map[string]interface{})
	require.True(t, ok, "spec has no paths object")

	documented := make(map[string]struct{}, len(paths))
	for p := range paths {
		documented[p] = struct{}{}
	}

	// The committed spec omits the /api/v1 prefix the router serves under.
	const apiPrefix = "/api/v1"

	ts := newTestServer(t)
	mux, ok := ts.Handler.(chi.Routes)
	require.True(t, ok, "router must expose chi.Routes for enumeration")

	var missing []string
	seen := make(map[string]struct{})
	require.NoError(t, chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// chi mounts leave "/*" artifacts on subtree roots; normalise exactly
		// as the route-accounting sweep does.
		route = strings.ReplaceAll(route, "/*/", "/")
		route = strings.TrimSuffix(route, "*")
		route = strings.TrimSuffix(route, "/")
		if !strings.HasPrefix(route, apiPrefix) {
			return nil // /health, /ready, /api/docs, the SPA fallback
		}
		specPath := strings.TrimPrefix(route, apiPrefix)
		if specPath == "" {
			return nil
		}
		key := method + " " + specPath
		if _, dup := seen[key]; dup {
			return nil
		}
		seen[key] = struct{}{}

		if _, isDocumented := documented[specPath]; !isDocumented {
			missing = append(missing, key)
		}
		return nil
	}))

	sort.Strings(missing)
	require.Emptyf(t, missing,
		"routes served by the router but absent from docs/api/openapi.yaml:\n  %s\n\n"+
			"Add swaggo annotations to the handler and run `make docs`. A route missing "+
			"from the spec is undocumented API; a stale spec entry is caught by "+
			"`make docs-check`.",
		strings.Join(missing, "\n  "))
}

// S6 — the docs page loads nothing from the internet.
//
// Every stylesheet and script it names must be served by this deployment. A
// CDN reference is two separate problems: on an isolated network — the normal
// case for a self-hosted product — the page is a blank screen with no
// explanation; and on a connected one it is third-party code, resolved from a
// floating tag, executing on the origin that holds an administrator's session.
func TestDocsEndpoint_LoadsNoExternalAssets(t *testing.T) {
	srv := newDocsTestServer(t)
	resp, err := http.Get(srv.URL + "/api/docs")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	page, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Deliberately broad: any absolute URL is a fetch this deployment does not
	// control, whichever host it names.
	for _, marker := range []string{"http://", "https://", "//unpkg.com", "cdn."} {
		require.NotContains(t, string(page), marker,
			"the docs page must reference no external origin (found %q)", marker)
	}
	require.Contains(t, string(page), "/api/docs/assets/swagger-ui.css")
	require.Contains(t, string(page), "/api/docs/assets/swagger-ui-bundle.js")
	require.Contains(t, string(page), "/api/docs/assets/swagger-ui-standalone-preset.js")
}

// And the assets the page names are actually served, with plausible content
// types — a 404 here is the same blank screen the CDN gave on an air-gapped
// network, so asserting only that the HTML changed would prove nothing.
func TestDocsAssets_AreServedFromTheBinary(t *testing.T) {
	srv := newDocsTestServer(t)

	for _, tc := range []struct {
		path        string
		contentType string
		mustContain string
	}{
		{"/api/docs/assets/swagger-ui.css", "text/css", ".swagger-ui"},
		{"/api/docs/assets/swagger-ui-bundle.js", "javascript", "SwaggerUIBundle"},
		{"/api/docs/assets/swagger-ui-standalone-preset.js", "javascript", "StandalonePreset"},
	} {
		resp, err := http.Get(srv.URL + tc.path)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Equal(t, http.StatusOK, resp.StatusCode, "%s", tc.path)
		require.Contains(t, resp.Header.Get("Content-Type"), tc.contentType, "%s", tc.path)
		require.Contains(t, string(body), tc.mustContain, "%s served the wrong file", tc.path)
		require.Greater(t, len(body), 1024, "%s looks truncated", tc.path)
	}
}

// S6 — the spec is no longer readable cross-origin by every site on the
// internet.
//
// It carried Access-Control-Allow-Origin: * unconditionally, which handed any
// page a visiting administrator opened the deployment's entire API surface:
// every route, parameter and schema. Its only consumer is the same-origin
// docs UI. A genuine cross-origin consumer belongs in the deployment's
// AZIMUTHAL_ALLOWED_ORIGINS allow-list, which the router's CORS middleware
// applies to this route like any other — not in a wildcard this handler sets
// for itself.
func TestDocsSpec_DoesNotAllowEveryOrigin(t *testing.T) {
	srv := newDocsTestServer(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/docs/openapi.yaml", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://attacker.example")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"),
		"the spec route must not set its own CORS header; the deployment's allow-list decides")
}
