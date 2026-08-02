package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
)

// SpaceOrgResolver returns the org that owns a space, or an error when the
// space does not exist. Used by RequireSpaceInOrg.
type SpaceOrgResolver func(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, error)

// RequireSpaceInOrg enforces the single org+space scoping convention: a
// request to /orgs/{orgID}/spaces/{spaceID}/... only proceeds when the space
// exists AND belongs to that org — otherwise 404. Requests whose route has no
// spaceID parameter (org-level space list/create) pass through untouched.
func RequireSpaceInOrg(resolve SpaceOrgResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			spaceIDRaw := chi.URLParam(r, "spaceID")
			if spaceIDRaw == "" {
				next.ServeHTTP(w, r)
				return
			}
			orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
			if err != nil {
				respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
				return
			}
			spaceID, err := uuid.Parse(spaceIDRaw)
			if err != nil {
				respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
				return
			}
			ownerOrg, err := resolve(r.Context(), spaceID)
			if err != nil || ownerOrg != orgID {
				respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "space not found")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequestID is middleware that assigns a unique request ID to each request.
var RequestID = respond.RequestID

// RequestIDFromContext returns the request ID from the context, or empty string.
var RequestIDFromContext = respond.RequestIDFromContext

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

// WriteHeader captures the status code.
func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wrote {
		rw.status = code
		rw.wrote = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

// Write captures 200 as default status.
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wrote {
		rw.status = http.StatusOK
		rw.wrote = true
	}
	n, err := rw.ResponseWriter.Write(b)
	if err != nil {
		return n, fmt.Errorf("writing response: %w", err)
	}
	return n, nil
}

// Unwrap supports http.ResponseController.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Logging is middleware that logs each request with its duration and status.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		slog.Info("http request", //nolint:gosec // G706: path/method/status are not attacker-controlled log sinks
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", respond.RequestIDFromContext(r.Context()),
		)
	})
}

// NewCORS returns a CORS middleware that only echoes Access-Control-Allow-Origin
// when the request's Origin matches one of allowedOrigins. The wildcard "*"
// in allowedOrigins permits any origin and must only ever come from an operator
// setting AZIMUTHAL_ALLOWED_ORIGINS=* deliberately.
//
// A nil or empty list emits no CORS headers at all, which is the default in
// every environment: the browser then enforces same-origin, which is what the
// SPA needs (it is served from this same binary in production and through
// Vite's server-side /api proxy in development, so neither is a cross-origin
// caller). Cross-origin access is a boot-time decision, never a runtime one.
//
// This function is the only CORS middleware. A permissive `CORS` variant that
// echoed Access-Control-Allow-Origin: * unconditionally used to sit beside it
// and was selected whenever RouterConfig.AllowedOrigins was nil — a fail-open
// default that every test harness silently picked up. It was removed in the
// S5 security pass; do not reintroduce a "just for tests" permissive path.
func NewCORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowAny, allowSet := buildOriginAllowList(allowedOrigins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed := resolveAllowedOrigin(origin, allowAny, allowSet)
			if allowed != "" {
				writeCORSHeaders(w, allowed)
			}
			if r.Method == http.MethodOptions {
				if allowed == "" && origin != "" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func buildOriginAllowList(origins []string) (bool, map[string]struct{}) {
	allowAny := false
	set := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		if o == "*" {
			allowAny = true
		}
		set[o] = struct{}{}
	}
	return allowAny, set
}

func resolveAllowedOrigin(origin string, allowAny bool, allowSet map[string]struct{}) string {
	if origin == "" {
		return ""
	}
	if allowAny {
		return origin
	}
	if _, ok := allowSet[origin]; ok {
		return origin
	}
	return ""
}

func writeCORSHeaders(w http.ResponseWriter, allowed string) {
	w.Header().Set("Access-Control-Allow-Origin", allowed)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-Request-ID")
	w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
	w.Header().Set("Access-Control-Max-Age", "86400")
}

// ContentSecurityPolicy is the policy served on every response.
//
// One global policy rather than a per-route one, on purpose: the SPA, the JSON
// API, the Swagger UI and the wiki render endpoint all sit on a single origin,
// and a policy that varies by path is a policy nobody can reason about. Every
// directive below is the strictest value that leaves a shipped feature working,
// and where it is not strict the reason is written down.
//
//   - script-src 'self' is the point of the whole header. No 'unsafe-inline',
//     no 'unsafe-eval', no nonce, no hash. The built SPA carries no inline
//     script — vite emits `<script type="module" crossorigin src="/assets/…">`
//     and nothing else — and the Swagger UI page's initialiser was moved out to
//     /api/docs/init.js so this directive could stay bare. Markup that gets
//     past the wiki sanitiser still cannot execute.
//   - style-src needs 'unsafe-inline', and it is the one loosening here. Three
//     independent sources need it and none can be nonced away: the Swagger UI
//     page's literal <style> block, and at runtime the <style> elements tiptap
//     (createStyleTag) and react-style-singleton inject into the head. React's
//     own style={{…}} props are unaffected either way — React writes those
//     through the CSSOM, which CSP does not govern. Styles cannot execute; the
//     residual risk is defacement, not code.
//   - img-src is deliberately as wide as the wiki sanitiser's own URL policy.
//     rehype-sanitize's default schema permits http and https `src`, converted
//     legacy markdown pages carry external images, and attachments are fetched
//     with a bearer token and handed to <img> as blob: URLs (fetchObjectURL in
//     web/src/lib/api.ts). Narrowing it here would break shipped content from a
//     second place while the sanitiser still permitted it — two policies
//     disagreeing about one question. What decides which URLs reach a page is
//     the sanitiser; this directive only has to not contradict it.
//   - font-src needs data: — the built stylesheet inlines Inter as
//     url(data:font/woff…).
//   - media-src carries blob: for the same reason img-src does: every
//     attachment reaches the browser through fetchObjectURL, and an audio or
//     video attachment is that same code path with a different element. It is
//     the one directive here that is not exercised by anything shipped today —
//     kept because the failure it would otherwise produce is a silent one, a
//     dead player and a console line nobody is watching. There is deliberately
//     no worker-src: nothing in this tree constructs a Worker, and a
//     same-origin one would fall back to default-src and work anyway.
//   - connect-src 'self' matches the frontend's default API base of /api/v1.
//     An operator who builds the SPA with an absolute VITE_API_BASE_URL is
//     pointing it at another origin and must widen this — the same coupling
//     AZIMUTHAL_ALLOWED_ORIGINS already carries.
//   - object-src 'none' and base-uri 'self' close the two classic bypasses a
//     script-src alone leaves open: plugin content, and rewriting the document
//     base so a relative script src resolves off-origin.
//   - frame-ancestors 'none' is the modern clickjacking control; the
//     X-Frame-Options header beside it says the same for browsers that never
//     learned the directive. Nothing in this product embeds itself in a frame.
const ContentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: http: https:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"media-src 'self' blob:; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// strictTransportSecurity asks a browser to refuse plaintext to this host for a
// year.
//
// NEITHER `preload` NOR `includeSubDomains`, and it is one reason applied
// twice: this binary knows only the host it was asked for, and both directives
// commit hostnames it has never seen. `preload` submits the domain to a
// browser-vendor list that is slow to leave again. `includeSubDomains` on an
// apex pins every sibling — an operator self-hosting at example.com would find
// an unrelated http://legacy.example.com unreachable for a year because of a
// header they did not write, and there is no configuration knob here to say
// otherwise. The sibling-subdomain cookie-injection attack that directive is
// mainly for does not apply either: the internal session is a bearer token in
// localStorage, not a cookie. The origin this binary actually serves is still
// protected.
//
// Sent unconditionally rather than only when r.TLS != nil, and that is
// deliberate in both directions. A browser ignores HSTS received over plain
// HTTP entirely — the spec requires a secure transport — so a localhost or
// plain-HTTP deployment is unaffected. And the usual production shape here is a
// TLS-terminating proxy in front of this binary, where r.TLS is nil on every
// request; a conditional would have switched the header off in exactly the
// deployment that needs it.
const strictTransportSecurity = "max-age=31536000"

// SecurityHeaders sets the response headers that hold for every route.
//
// X-Content-Type-Options: nosniff stops a browser second-guessing a declared
// Content-Type and executing bytes as a type the server did not choose. The
// attachment and avatar serve paths each set it per-response already; setting
// it globally means a route added later inherits it rather than having to
// remember, and it reaches the routes that never set it — notably the wiki
// render endpoint, which returns user-authored content as text/html.
//
// It is set before the handler runs, so a handler's own Set replaces it rather
// than duplicating it.
//
// What it is NOT: a defence against a content type the server declares
// deliberately. nosniff constrains sniffing, not rendering — a response the
// server labels text/html is still rendered as HTML. That is exactly why the
// attachment serve path sniffs the stored bytes instead of relying on this
// header, and why adding this middleware would not on its own have closed
// that hole.
//
// The other four headers arrived with the v0.4.1 trust patch. Until then this
// middleware set nosniff and nothing else: no CSP, so script that reached a
// rendered page ran with the origin's full authority; no frame controls, so the
// whole app could be framed; no referrer policy, so a full URL — which in this
// product carries org, space and entity ids — travelled to every cross-origin
// destination a user clicked through to.
//
// Referrer-Policy is strict-origin-when-cross-origin: a same-origin navigation
// keeps the full path (the SPA is one origin, so nothing internal is lost), a
// cross-origin one sends the bare origin, and an HTTPS→HTTP downgrade sends
// nothing at all.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Strict-Transport-Security", strictTransportSecurity)
		next.ServeHTTP(w, r)
	})
}

// Recoverer is middleware that recovers from panics and returns a 500 error.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rvr := recover(); rvr != nil {
				slog.Error("panic recovered", //nolint:gosec // G706: path is not an attacker-controlled log sink
					"error", rvr,
					"request_id", respond.RequestIDFromContext(r.Context()),
					"path", r.URL.Path,
				)
				respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
