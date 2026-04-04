package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
)

// TestSmoke is an end-to-end smoke test that exercises the full application
// stack: database, migrations, authentication, and CRUD operations.
// It requires a real PostgreSQL database — set DATABASE_URL to run it.
func TestSmoke(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping smoke test")
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		t.Setenv("JWT_SECRET", "smoke-test-secret-not-for-production-use-1234")
	}
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_URL", dbURL)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	srv, cleanup, err := newServer(cfg)
	if err != nil {
		t.Fatalf("creating server: %v", err)
	}
	defer cleanup()

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	base := ts.URL

	// 1. GET /health — expects 200
	t.Run("health", func(t *testing.T) {
		body := doGet(t, client, base+"/health", "", http.StatusOK)
		if body["status"] != "ok" {
			t.Errorf("expected status ok, got %v", body["status"])
		}
	})

	// 2. GET /ready — expects 200
	t.Run("ready", func(t *testing.T) {
		body := doGet(t, client, base+"/ready", "", http.StatusOK)
		if body["status"] != "ready" {
			t.Errorf("expected status ready, got %v", body["status"])
		}
	})

	// 3. Register a user (the default org was created during newServer)
	var accessToken string
	var userID string

	t.Run("register_user", func(t *testing.T) {
		payload := map[string]string{
			"email":        fmt.Sprintf("smoke-%d@test.local", time.Now().UnixNano()),
			"display_name": "Smoke Tester",
			"password":     "test-password-123",
		}
		body := doPost(t, client, base+"/api/v1/auth/register", payload, "", http.StatusCreated)

		token, ok := body["access_token"].(string)
		if !ok || token == "" {
			t.Fatal("expected access_token in response")
		}
		accessToken = token

		user, ok := body["user"].(map[string]interface{})
		if !ok {
			t.Fatal("expected user in response")
		}
		id, ok := user["id"].(string)
		if !ok || id == "" {
			t.Fatal("expected user.id in response")
		}
		userID = id
	})

	if accessToken == "" {
		t.Fatal("cannot continue without access token")
	}

	// 4. Look up the default org ID from the database
	var orgID string
	t.Run("get_org_id", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		pool, poolErr := pgxpool.New(ctx, dbURL)
		if poolErr != nil {
			t.Fatalf("connecting to DB: %v", poolErr)
		}
		defer pool.Close()

		err := pool.QueryRow(ctx,
			"SELECT id FROM organizations WHERE slug = 'default' AND deleted_at IS NULL LIMIT 1",
		).Scan(&orgID)
		if err != nil {
			t.Fatalf("looking up default org: %v", err)
		}
	})

	if orgID == "" {
		t.Fatal("cannot continue without org ID")
	}

	_ = userID // validated during registration

	// 5. Create a space
	var spaceID string
	t.Run("create_space", func(t *testing.T) {
		slug := fmt.Sprintf("smoke-%d", time.Now().UnixNano())
		payload := map[string]interface{}{
			"slug":        slug,
			"name":        "Smoke Test Space",
			"description": "Created by smoke test",
			"type":        "service_desk",
			"is_private":  false,
		}
		url := fmt.Sprintf("%s/api/v1/orgs/%s/spaces", base, orgID)
		body := doPost(t, client, url, payload, accessToken, http.StatusCreated)

		id, ok := body["id"].(string)
		if !ok || id == "" {
			// Some handlers return PascalCase field names
			id, ok = body["ID"].(string)
		}
		if !ok || id == "" {
			t.Fatalf("expected space id in response, got %v", body)
		}
		spaceID = id
	})

	if spaceID == "" {
		t.Fatal("cannot continue without space ID")
	}

	// 6. Create a ticket in the space
	var ticketID string
	t.Run("create_ticket", func(t *testing.T) {
		payload := map[string]interface{}{
			"title":       "Smoke test ticket",
			"description": "Created by automated smoke test",
			"priority":    "medium",
			"labels":      []string{},
		}
		url := fmt.Sprintf("%s/api/v1/spaces/%s/tickets", base, spaceID)
		body := doPost(t, client, url, payload, accessToken, http.StatusCreated)

		id, ok := body["id"].(string)
		if !ok || id == "" {
			// Ticket handler returns PascalCase field names
			id, ok = body["ID"].(string)
		}
		if !ok || id == "" {
			t.Fatalf("expected ticket id in response, got %v", body)
		}
		ticketID = id
	})

	if ticketID == "" {
		t.Fatal("cannot continue without ticket ID")
	}

	// 7. Retrieve the ticket and verify it matches
	t.Run("get_ticket", func(t *testing.T) {
		url := fmt.Sprintf("%s/api/v1/spaces/%s/tickets/%s", base, spaceID, ticketID)
		body := doGet(t, client, url, accessToken, http.StatusOK)

		title := body["title"]
		if title == nil {
			title = body["Title"]
		}
		if title != "Smoke test ticket" {
			t.Errorf("expected title 'Smoke test ticket', got %v", title)
		}
		desc := body["description"]
		if desc == nil {
			desc = body["Description"]
		}
		if desc != "Created by automated smoke test" {
			t.Errorf("expected description 'Created by automated smoke test', got %v", desc)
		}
	})
}

// doGet performs a GET request, asserts the status, reads and closes the body,
// and returns the parsed JSON. All in one call to satisfy bodyclose linter.
func doGet(t *testing.T, client *http.Client, url, token string, wantStatus int) map[string]interface{} {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("expected status %d, got %d: %s", wantStatus, resp.StatusCode, string(raw))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parsing JSON %q: %v", string(raw), err)
	}
	return result
}

// doPost performs a POST request with JSON body, asserts the status, reads and
// closes the body, and returns the parsed JSON.
func doPost(t *testing.T, client *http.Client, url string, payload interface{}, token string, wantStatus int) map[string]interface{} {
	t.Helper()
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshalling payload: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("expected status %d, got %d: %s", wantStatus, resp.StatusCode, string(raw))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parsing JSON %q: %v", string(raw), err)
	}
	return result
}
