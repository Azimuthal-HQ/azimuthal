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
		resp := mustGet(t, client, base+"/health")
		defer resp.Body.Close()

		assertStatus(t, resp, http.StatusOK)
		body := readJSON(t, resp)
		if body["status"] != "ok" {
			t.Errorf("expected status ok, got %v", body["status"])
		}
	})

	// 2. GET /ready — expects 200
	t.Run("ready", func(t *testing.T) {
		resp := mustGet(t, client, base+"/ready")
		defer resp.Body.Close()

		assertStatus(t, resp, http.StatusOK)
		body := readJSON(t, resp)
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
		resp := mustPost(t, client, base+"/api/v1/auth/register", payload, "")
		defer resp.Body.Close()

		assertStatus(t, resp, http.StatusCreated)
		body := readJSON(t, resp)

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
		resp := mustPost(t, client, url, payload, accessToken)
		defer resp.Body.Close()

		assertStatus(t, resp, http.StatusCreated)
		body := readJSON(t, resp)

		id, ok := body["id"].(string)
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
		}
		url := fmt.Sprintf("%s/api/v1/spaces/%s/tickets", base, spaceID)
		resp := mustPost(t, client, url, payload, accessToken)
		defer resp.Body.Close()

		assertStatus(t, resp, http.StatusCreated)
		body := readJSON(t, resp)

		id, ok := body["id"].(string)
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
		resp := mustGetAuth(t, client, url, accessToken)
		defer resp.Body.Close()

		assertStatus(t, resp, http.StatusOK)
		body := readJSON(t, resp)

		if body["title"] != "Smoke test ticket" {
			t.Errorf("expected title 'Smoke test ticket', got %v", body["title"])
		}
		if body["description"] != "Created by automated smoke test" {
			t.Errorf("expected description 'Created by automated smoke test', got %v", body["description"])
		}
	})
}

// --- helpers ---

func mustGet(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func mustGetAuth(t *testing.T, client *http.Client, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func mustPost(t *testing.T, client *http.Client, url string, payload interface{}, token string) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshalling payload: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
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
	return resp
}

func assertStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status %d, got %d: %s", expected, resp.StatusCode, string(body))
	}
}

func readJSON(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("parsing JSON %q: %v", string(body), err)
	}
	return result
}
