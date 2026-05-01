package respond_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
)

func TestJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	respond.JSON(rr, http.StatusOK, map[string]string{"key": "value"})

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body["key"] != "value" {
		t.Errorf("body[key] = %q, want %q", body["key"], "value")
	}
}

func TestError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	respond.Error(rr, req, http.StatusNotFound, respond.CodeNotFound, "item not found")

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body.Error.Code != "NOT_FOUND" {
		t.Errorf("error.code = %q, want %q", body.Error.Code, "NOT_FOUND")
	}
	if body.Error.Message != "item not found" {
		t.Errorf("error.message = %q, want %q", body.Error.Message, "item not found")
	}
}

func TestErrorWithRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	// Simulate RequestID middleware
	handler := respond.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "bad input")
	}))
	handler.ServeHTTP(rr, req)

	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body.Error.RequestID == "" {
		t.Error("expected request_id to be set")
	}
	if !strings.HasPrefix(body.Error.RequestID, "req_") {
		t.Errorf("request_id = %q, expected prefix 'req_'", body.Error.RequestID)
	}
}

func TestDecodeJSON(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}

	body := bytes.NewBufferString(`{"name":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)

	var dst input
	if err := respond.DecodeJSON(req, &dst); err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if dst.Name != "test" {
		t.Errorf("Name = %q, want %q", dst.Name, "test")
	}
}

func TestDecodeJSONNilBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = nil

	var dst struct{}
	err := respond.DecodeJSON(req, &dst)
	if err == nil {
		t.Error("expected error for nil body")
	}
}

func TestRequestID(t *testing.T) {
	handler := respond.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := respond.RequestIDFromContext(r.Context())
		if id == "" {
			t.Error("expected request ID in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Without header — auto-generated
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if id := rr.Header().Get("X-Request-ID"); id == "" {
		t.Error("expected X-Request-ID header")
	}

	// With header — preserved
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Request-ID", "custom-id")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if id := rr2.Header().Get("X-Request-ID"); id != "custom-id" {
		t.Errorf("X-Request-ID = %q, want %q", id, "custom-id")
	}
}
