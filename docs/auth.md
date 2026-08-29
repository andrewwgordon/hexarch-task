# Google Authentication + DaisyUI Login Page — Specification & Implementation Plan

**Project:** `hexarch` — hexagonal task manager (Go 1.27, Gin, `html/template`, HTMX, DaisyUI v5 via CDN)
**Status:** Draft for review
**Scope:** Google OAuth 2.0 sign-in for the `httpweb` adapter, a DaisyUI login page, and session-protected `/app` routes.

---

## 1. Project analysis (current state)

The codebase is a strict hexagonal (ports & adapters) app. Relevant facts:

| Area | Current state |
|------|---------------|
| Composition root | `cmd/main.go` builds one `application.TaskService` from a `repository.TaskRepository` and dispatches to `cli`, `httpapi`, or `httpweb` via the shared `adapters.Adapter` interface. |
| Web adapter | `internal/adapters/httpweb` — Gin engine under the `/app` group (`router.go`), dual-render strategy (full-page shell vs HTMX fragment via `HX-Request` + `Vary: HX-Request`), PRG fallback for JS-less mutations. |
| Templates | `templates/index.html` + `partials/*.html`; DaisyUI **v5** + Tailwind **v4** loaded from CDN, HTMX **4.0.0**; no Node/build pipeline; theme toggle stored in `localStorage` (`daisyui-theme`). |
| Views layer | `views.go` converts domain types to presentation values (`taskView`, `listPage`, `statsView`); `statusMeta`/`transitionTable` hold presentation knowledge. |
| Error rendering | `errors.go` → `writeError` renders `partials/error_alert.html` and maps `domain.Kind` to HTTP status via `httpconv`. |
| Persistence | `internal/repository` — Provider registry; providers `Open(ctx, uri)` and own schema setup; backends: `sqlite` (default), `postgres`, `oracle`, `mongodb`, `memory`; shared SQL core `sqldb`; conformance suite (`internal/repository/conformance`) for backend parity. |
| Tests | External test packages (`httpweb_test`, `httpapi_test`, `application_test`…), httptest against real Gin engines, in-memory repo + deterministic ID/clock DI. |
| Security posture | No auth, no sessions, no security headers today. The Gin skill (`.agents/skills/gin-skill`) provides JWT/session/OAuth guidance and documents the go-jose CVE-2026-34986 exposure when using `coreos/go-oidc`. |

**Gap:** there is no notion of a user anywhere — no user entity, no session, no auth middleware. The web UI is fully public.

---

## 2. Goals and non-goals

### Goals
1. Allow users to sign in with their Google account (**OAuth 2.0 Authorization Code flow**, server-side ID-token verification).
2. Protect all `/app` routes behind authentication (task data becomes private to signed-in users).
3. Ship a polished **DaisyUI login page** consistent with the existing UI (CDN stack, theme toggle, `base-*` colors).
4. Show the signed-in user in the navbar (avatar, name, sign-out) and support sign-out.
5. Follow the hexagon: auth logic lives in a new domain/application layer with ports; adapters stay thin; the app remains testable with fakes (same philosophy as the in-memory repo + fake clock).
6. **Do not break** the `cli` and `httpapi` adapters, the repository swap-ability, or the existing test suite.
7. Run identically with no Google credentials configured (auth disabled → today's behavior), preserving dev/test ergonomics.

### Non-goals (Phase 1)
- ❌ Per-user data ownership (tasks are global; ownership is a follow-up).
- ❌ Auth for the JSON `httpapi` (Bearer tokens) and `cli` (device flow) adapters.
- ❌ Password login, 2FA, account management UI, admin roles.
- ❌ Server-side session revocation / session table (cookie sessions are signed, stateless).
- ❌ A users/persistence migration across all five repository backends.

These are captured as future work (§15).

---

## 3. Requirements

### 3.1 Functional requirements (FR)

| ID | Requirement |
|----|-------------|
| FR-1 | `GET /app/login` renders a DaisyUI login page with a **“Sign in with Google”** button. |
| FR-2 | Clicking the button starts the Google OAuth flow (`GET /app/auth/login` → redirect to `accounts.google.com`). |
| FR-3 | `GET /app/auth/callback` exchanges the authorization code, **verifies the ID token server-side**, resolves the user, and creates a session. |
| FR-4 | After sign-in the user is redirected back to `/app/` (or the originally requested page, FR-10). |
| FR-5 | All existing `/app/*` routes require a valid session; unauthenticated browser loads redirect to `/app/login`. |
| FR-6 | Unauthenticated **HTMX fragment** requests respond `401` with an `HX-Redirect: /app/login` header so the UI navigates without a full reload. |
| FR-7 | The navbar shows the signed-in user (avatar from Google profile picture, name, email) plus a **Sign out** action. |
| FR-8 | `POST /app/auth/logout` ends the session and redirects to `/app/login`. |
| FR-9 | Failed/stale OAuth attempts render an inline DaisyUI error alert on the login page (`?error=…` state) instead of a bare 500. |
| FR-10 | Login respects a same-origin `?next=`/`next` return path (open-redirect safe) so shareable filter URLs survive the auth hop. |
| FR-11 | When Google credentials are not configured, the app boots and behaves exactly as today (auth disabled, warning logged). |

### 3.2 Non-functional / security requirements (NFR)

| ID | Requirement |
|----|-------------|
| NFR-1 | ID token validation **only server-side**; never trust client-supplied claims. Validate signature (Google JWKS), `aud` = our client ID, `exp`, `iss` = `https://accounts.google.com`. |
| NFR-2 | OAuth `state` parameter + double-submit-cookie CSRF protection (Google's documented pattern); **PKCE (S256)** for the code exchange. |
| NFR-3 | Session cookie: `HttpOnly`, `SameSite=Lax`, `Secure` in production, signed, TTL ≤ 24 h (configurable). Session secret from env, never hardcoded. |
| NFR-4 | No dependency on `coreos/go-oidc` / `go-jose` (CVE-2026-34986). Verify ID tokens with `golang.org/x/oauth2/google/idtoken` (fetches Google certs directly — no JWE parsing path). |
| NFR-5 | Errors never leak tokens, codes, or internal details. Log events at DEBUG/WARN only. |
| NFR-6 | `next`/return URLs are restricted to same-origin paths starting with `/`. |
| NFR-7 | Existing tests keep passing unmodified where possible; new auth tests follow the external-package + fake-DI conventions. |
| NFR-8 | Graceful degradation without JavaScript: sign-in and sign-out are plain links/forms (PRG), consistent with the existing UX. |

---

## 4. Proposed architecture

Auth is composed exactly like every other inbound adapter concern: a **domain core**, **application use cases** with injected ports, and **adapter-level mechanics** (cookies/HTTP) kept thin.

```
                                 ┌───────────────────────────────────────────┐
                                 │              inbound adapters             │
        cmd/main.go ────────────▶│  cli │ httpapi │ httpweb (+ auth handlers) │
        (composition root)       └─────────────────────────┬─────────────────┘
                           builds AuthService ─────────────┤ calls
                                                           ▼
                                 ┌───────────────────────────────────────────┐
                                 │          application (use cases)          │
                                 │   TaskService            AuthService       │
                                 │                          beginLogin        │
                                 │                          completeLogin     │
                                 │                          currentUser       │
                                 │                          logout            │
                                 └───────┬───────────────────────────┬────────┘
                                         │ depends on ports          │
                                         ▼                           ▼
              ┌────────────────────────────────────────┐  ┌──────────────────────┐
              │  outbound ports (interfaces)           │  │  GoogleGateway        │
              │  repository.TaskRepository (existing)  │  │  (idtoken + oauth2)   │
              └────────────────────────────────────────┘  └──────────────────────┘
```

### 4.1 New packages

```
internal/
├── auth/                          # NEW — domain + outbound ports (pure)
│   ├── user.go                    #   User value object (Google-derived claims)
│   ├── session.go                 #   Session value object (id, user, issued, expires)
│   ├── errors.go                  #   Typed errors: INVALID_STATE, TOKEN_INVALID,
│   │                              #     SESSION_EXPIRED, OAUTH_DISABLED …
│   ├── google/                    #   NEW — concrete Google provider (no Gin imports)
│   │   └── provider.go            #     oauth2.Config + idtoken.Validator wiring
│   └── auth_service.go            #   Inbound port (AuthService interface) + DTOs
├── application/
│   └── auth_service_impl.go       #   NEW — use cases with DI (same pattern as
│                                  #     task_service_impl.go)
└── adapters/httpweb/
    ├── auth_handlers.go           #   NEW — login, startOAuth, callback, logout, requireAuth
    ├── auth_handlers_test.go      #   NEW — web auth test suite
    ├── views.go                   #   MODIFY — add userView / toUserView
    ├── server.go / router.go      #   MODIFY — compose AuthService, wire groups/middleware
    ├── templates/
    │   ├── login.html             #   NEW — DaisyUI login page
    │   └── index.html             #   MODIFY — auth-aware navbar (user + sign out)
```

### 4.2 Layer design

**Domain — `internal/auth` (pure).**
- `User` value object: `Subject` (Google `sub` — stable, globally unique), `Email`, `Name`, `PictureURL`, `EmailVerified`. No persistence logic.
- `Session` value object: `ID`, `User`, `IssuedAt`, `ExpiresAt` with `Expired(now)`.
- Typed error kinds compatible with the existing pattern (`httpconv.StatusForKind` maps `UNAUTHENTICATED` → 401).

**Application — `AuthService` (inbound port, DI'd like `TaskService`).**
```go
type GoogleGateway interface {
    AuthURL(state, codeChallenge string) string            // build authorize URL
    Exchange(ctx, code, codeVerifier) (*IDToken, error)   // code → verified claims
}
type AuthService interface {
    BeginLogin(ctx) (LoginRequest, error)                 // state, verifier, redirect URL
    CompleteLogin(ctx, code, state, verifier) (Session, error)
    ValidateSession(ctx, rawSession) (User, error)        // signature/expiry → user
    Logout(ctx, rawSession) error
}
```
`LoginRequest` carries `State` + `CodeVerifier`; the **adapter** persists them in a short-lived cookie and renders the URL. This keeps OAuth mechanics out of the application layer while staying injectable for tests (fake gateway, like the fake clock).

**Outbound — `internal/auth/google`.** Wraps `golang.org/x/oauth2`:
- `oauth2.Config{ClientID, ClientSecret, Endpoint: google.Endpoint, RedirectURL, Scopes: []string{"openid", "email", "profile"}}`.
- PKCE: `code_challenge = S256(verifier)` (RFC 7636), verifier from `crypto/rand` (43–128 chars, base64url no padding).
- ID-token verification via `golang.org/x/oauth2/google/idtoken`:
```go
validator, err := idtoken.NewValidator(ctx, clientID)   // caches Google certs
payload, err := validator.Validate(ctx, rawIDToken, clientID)
// payload.Subject, payload.Issuer; email/name/picture read from payload.Claims
```
  (`Validate` enforces signature, `aud`, `exp`, `iss` — NFR-1.)

**Adapter — `httpweb`.** Sessions use `github.com/gin-contrib/sessions` (cookie store → gorilla/securecookie under the hood): signed cookie, `HttpOnly`, `SameSite=Lax`, `Secure` when `HEXARCH_ENV=production` (or when the callback URL is HTTPS). Session contents are the *verified* user claims (Phase 1 keeps the identity provider authoritative — see D4). `requireAuth` middleware sets `user` into the Gin context; handlers pass it to views.

**Composition root — `cmd/main.go`.** If `HEXARCH_GOOGLE_CLIENT_ID` and `HEXARCH_GOOGLE_CLIENT_SECRET` are present, build `AuthService` (Google gateway + session signing) and pass to `httpweb.NewWeb(svc, auth)`. Otherwise pass `nil` → auth middleware is a no-op pass-through (FR-11). `NewRouter(svc, auth)` mirrors this so **all existing tests compile and pass unchanged** (nil auth ⇒ unprotected engine).

---

## 5. OAuth flow (sequence)

```
Browser            httpweb                          Google
  │  GET /app/        │                                │
  │──────────────────▶│  requireAuth: no session       │
  │  303 → /app/login │                                │
  │◀──────────────────│                                │
  │  GET /app/login   │  render login.html (DaisyUI)   │
  │──────────────────▶│                                │
  │  click "Sign in with Google"                       │
  │  GET /app/auth/login                               │
  │──────────────────▶│  BeginLogin: state + PKCE      │
  │  set oauth_pending cookie (state+verifier, 5 min)  │
  │  302 → accounts.google.com  ──────────────────────▶│
  │◀───────────────────────────────────────────────────│  user authenticates
  │  GET /app/auth/callback?code=..&state=..           │
  │──────────────────▶│  verify state cookie matches    │
  │                   │  Exchange code (PKCE)          │
  │                   │  validate ID token (idtoken)   │
  │                   │  Session{ID, User, exp} → cookie│
  │  303 → /app/ (or ?next=)                           │
  │◀──────────────────│                                │
  │  GET /app/        │  requireAuth: ✅               │
```

---

## 6. HTTP surface (`httpweb` only)

### 6.1 Routes

| Method | Path | Auth | Handler | Description |
|--------|------|------|---------|-------------|
| GET | `/app/login` | public | `loginPage` | DaisyUI login page (FR-1) |
| GET | `/app/auth/login` | public | `startOAuth` | Begin flow: state+PKCE cookie, redirect to Google (FR-2) |
| GET | `/app/auth/callback` | public | `callback` | Exchange + verify + session (FR-3, FR-4) |
| POST | `/app/auth/logout` | required | `logout` | Clears session, redirects to login (FR-8) |
| GET | `/app/` … all existing `/app` routes | required | existing | Protected by `requireAuth` (FR-5, FR-6) |

Router wiring sketch:
```go
public := r.Group("/app")
{
    public.GET("/login", h.loginPage)
    public.GET("/auth/login", h.startOAuth)
    public.GET("/auth/callback", h.callback)
}
protected := r.Group("/app", h.requireAuth)
{
    protected.GET("/", h.index)
    protected.POST("/auth/logout", h.logout)
    // … existing routes move here unchanged …
}
```

### 6.2 Middleware behavior (`requireAuth`)

```
has valid session? ── yes ──▶ c.Set("user", user); c.Next()
        │
        no
        ├─ HX-Request? ── yes ──▶ 401 + header HX-Redirect: /app/login?next=<path>   (FR-6)
        │
        └─ full request ──▶ 303 See Other → /app/login?next=<escaped path>            (FR-5)
```
`next` is captured from the incoming request path (query included) when it is same-origin and starts with `/` (NFR-6); otherwise it is dropped.

### 6.3 Environment variables (new)

| Variable | Default | Notes |
|----------|---------|-------|
| `HEXARCH_GOOGLE_CLIENT_ID` | *(unset)* | Unset ⇒ auth disabled (FR-11) |
| `HEXARCH_GOOGLE_CLIENT_SECRET` | *(unset)* | Unset ⇒ auth disabled |
| `HEXARCH_GOOGLE_REDIRECT_URL` | `http://localhost:8081/app/auth/callback` | Must **exactly** match the *Authorized redirect URIs* in Google Cloud Console |
| `HEXARCH_SESSION_SECRET` | *(unset ⇒ generated per boot, sessions lost on restart)* | ≥ 32 random bytes, e.g. `openssl rand -base64 48`; required in production |
| `HEXARCH_SESSION_TTL` | `24h` | `time.ParseDuration` format |
| `HEXARCH_ENV` | `development` | `production` ⇒ `Secure` cookies + stricter headers |

### 6.4 Google Cloud Console setup (documented for operators)

1. Create an OAuth 2.0 **Web application** client ID/secret at console.cloud.google.com.
2. Add `http://localhost:8081/app/auth/callback` (dev) and the production HTTPS callback to **Authorized redirect URIs**.
3. No additional scopes needed: `openid email profile` is the sign-in default.

---

## 7. DaisyUI login page specification

Follows the component-discovery rules from the daisyUI skill and keeps visual parity with `index.html`: same CDN head (DaisyUI v5 + Tailwind v4 browser + HTMX 4.0.0), same `data-theme="light"` root, same theme-toggle control and `localStorage` script.

### 7.1 Layout (components)

```
hero bg-base-200 min-h-screen
└─ hero-content (centered, w-full max-w-md)
   └─ card bg-base-100 shadow
      └─ card-body
         ├─ h1 (brand) "To Do"            — text-primary, font-bold (primary used once)
         ├─ p  subtitle "Sign in to manage your tasks"
         ├─ divider "Continue with"
         ├─ <a href="/app/auth/login">     — Sign in with Google
         │    btn btn-outline w-full
         │    ├─ Google "G" multicolor SVG (official 4-color mark)
         │    └─ "Sign in with Google"
         └─ p  footnote (privacy note, text-xs text-base-content/60)
   └─ (top-right, outside card) theme toggle — swap-rotate control copied from index.html
   └─ error state (conditional): alert alert-error with message when ?error=…
```
- The Google button is a plain `<a class="btn btn-outline">` — a real link so it works without JavaScript (NFR-8); `btn-outline` matches Google's white, bordered brand button and follows the skill rule to use a variant only when the request calls for it (brand fidelity requires it here).
- Desktop/mobile: `hero-content` is centered and `max-w-md`; no JS, no HTMX needed on this page.
- Theme toggle: identical markup + script as `index.html` so the persisted choice (`daisyui-theme`) carries over.
- Error alert: reuse the visual language of `partials/error_alert.html` (`alert alert-error` + message), rendered inline from `?error=` query (FR-9).

### 7.2 Resulting template

`templates/login.html` — a full-page shell (like `index.html`), parsed automatically by the existing `loadTemplates()` glob (no router/template-loader changes needed beyond adding the file).

---

## 8. Auth-aware navbar (`index.html`)

- `handlers.index` obtains `user` from context (set by middleware) and passes `gin.H{"User": toUserView(user)}`.
- `views.go` gains `userView {Name, Email, PictureURL}` + `toUserView`.
- Navbar `navbar-end` (currently theme toggle + “+ New Task”):
  - when signed in: `avatar` (Google picture, `w-8 rounded-full`) + name/email (hidden on small screens) + `Sign out` (`btn btn-ghost btn-sm`, plain form POST → `/app/auth/logout` for JS-less PRG);
  - when auth is disabled (dev mode, FR-11): no user UI rendered.
- The “+ New Task” button and everything else stay untouched.

---

## 9. Security controls checklist (all enforced)

- [x] **Server-side token verification only** — `idtoken.Validator.Validate(ctx, token, clientID)`; ignores any client-sent identity (NFR-1).
- [x] **State double-submit cookie** — `oauth_pending` cookie (HttpOnly, SameSite=Lax, 5 min TTL) holding state + PKCE verifier; callback compares cookie state vs query state (constant-time compare) and clears the cookie (NFR-2).
- [x] **PKCE S256** for the code exchange (NFR-2).
- [x] **Session cookie** — signed (HMAC via gorilla/securecookie), `HttpOnly`, `SameSite=Lax`, `Secure` in production, `HEXARCH_SESSION_TTL` expiry enforced server-side on every request (NFR-3).
- [x] **Secret management** — all secrets from env; refuse to hardcode; boot-time warning when `HEXARCH_SESSION_SECRET` is missing (NFR-3).
- [x] **No go-jose/go-oidc** — `x/oauth2/google/idtoken` has no JWE parsing, so CVE-2026-34986 (and the go-jose CVE-2025-27144 lineage) is avoided by construction (NFR-4).
- [x] **Open-redirect protection** — `next` must start with `/` and be same-origin (NFR-6).
- [x] **Error hygiene** — generic user-facing messages; `code`/`state`/token values never rendered or logged (NFR-5).
- [x] **HTMX-aware 401** — `HX-Redirect` header so fragments don’t silently break (FR-6, htmx skill: drive client behavior via response headers).

**Follow-ups (not blocking Phase 1):** security headers middleware (CSP, `X-Frame-Options`, `Referrer-Policy`), CSRF tokens on mutating forms, rate limiting on `/app/auth/*` (see §14).

---

## 10. Dependencies

| Module | Why | Notes |
|--------|-----|-------|
| `golang.org/x/oauth2` | OAuth 2.0 client (config, exchange, PKCE helpers) | Current line `v0.x`; `go get golang.org/x/oauth2@latest` |
| `golang.org/x/oauth2/google` | `google.Endpoint` + `google/idtoken` subpackage (ID-token verification, cert caching) | Ships inside `x/oauth2`; no go-jose |
| `github.com/gin-contrib/sessions` | Signed cookie sessions for Gin | Wraps `gorilla/sessions` + `securecookie`; battle-tested |
| `crypto/rand`, `crypto/subtle`, `encoding/base64` | state/verifier generation, constant-time compare | stdlib |

No changes to the repository layer, the domain task model, or the CLI/API adapters.

---

## 11. Testing strategy

Follows the existing conventions exactly: **external test packages**, real Gin engines via `httptest`, fake DI for anything external.

| Suite | File | Approach |
|-------|------|----------|
| Application use cases | `internal/application/auth_service_impl_test.go` (`application_test`) | Fake `GoogleGateway` (scripted token payloads), fake clock; assert: begin-login generates state+verifier; complete-login happy path returns session; state mismatch → `INVALID_STATE`; bad token → `TOKEN_INVALID`; expired session → `SESSION_EXPIRED`. |
| Web adapter | `internal/adapters/httpweb/auth_handlers_test.go` (`httpweb_test`) | `NewRouter(svc, fakeAuth)`; cases: `/app/` unauthenticated ⇒ 303 → `/app/login`; `/app/login` renders (contains `Sign in with Google`, DaisyUI shell); HX fragment unauthenticated ⇒ 401 + `HX-Redirect` header; callback happy path ⇒ session cookie set + 303 → `/app/`; callback with wrong state ⇒ login page error alert; logout ⇒ cookie cleared + 303; authenticated index renders navbar user. |
| Regression | all existing suites | `NewRouter(svc, nil)` keeps them green (auth off) — verified in CI. |
| Integration | `test/integration_test.go` | One end-to-end pass over a real socket: unauthenticated → login → fake callback → protected page 200, with SQLite. |

Acceptance gate: `go vet ./...`, `gofmt -l .`, `go test ./...` all clean.

---

## 12. Implementation plan

Each step is independently committable; the repo is green after every step.

### Step 0 — Dependencies & config plumbing
- Add `golang.org/x/oauth2`, `github.com/gin-contrib/sessions`; `go mod tidy`.
- New `internal/auth/config.go`: reads `HEXARCH_GOOGLE_*`, `HEXARCH_SESSION_*`, `HEXARCH_ENV`; `Enabled()` predicate; validation errors (e.g. missing secret in production).
- **Accept:** `go build ./...`; config unit test; app boots unchanged when env absent.

### Step 1 — Auth domain + application
- `internal/auth/user.go`, `session.go`, `errors.go` (typed kinds; register `UNAUTHENTICATED` mapping in `httpconv`).
- `internal/auth/auth_service.go` (port + DTOs) + `internal/application/auth_service_impl.go` (DI for gateway/clock/now).
- **Accept:** application tests green with fake gateway (list above).

### Step 2 — Google provider
- `internal/auth/google/provider.go`: `Config` build from env, `AuthURL(state, challenge)`, `Exchange(ctx, code, verifier)`, `idtoken.NewValidator` verification.
- **Accept:** unit tests with `httptest` Google endpoints (authorize/token/JWKS stubs); `go vet` clean.

### Step 3 — httpweb adapter: sessions, middleware, handlers
- `auth_handlers.go`: `loginPage`, `startOAuth`, `callback`, `logout`, `requireAuth` (HX-aware).
- `router.go`: public + protected groups (sketch in §6.1); `NewRouter(svc, auth)` signature; `server.go`/`NewWeb(svc, auth)`.
- `cmd/main.go`: compose AuthService when enabled; pass through.
- **Accept:** web auth tests green; all pre-existing tests green with `nil` auth.

### Step 4 — Login page + navbar
- `templates/login.html` (§7); `views.go` `userView`/`toUserView`; `index.html` navbar (§8); wire `User` into `index` handler.
- **Accept:** manual smoke `HEXARCH_GOOGLE_REDIRECT_URL=http://localhost:8081/app/auth/callback ./hexarch httpweb` — full round trip with a real Google OAuth client; login page matches DaisyUI (light/dark toggle persists).

### Step 5 — Docs & polish
- README: auth section (env vars, console setup, route table), update web-UI feature list and environment table.
- Add `docs/auth.md` cross-reference from README.
- **Accept:** `go test ./...` green; README truthfully documents both modes.

### Step 6 — Hardening pass (review checklist)
- Security checklist (§9) re-verified; error paths re-test manually (Google “Cancel”/`access_denied`, expired state, replay of a used code, tampered session cookie, tampered `next`).

---

## 13. Risks & mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| `NewRouter`/`NewWeb` signature change ripples to tests | Build breakage | Keep `auth` nullable with no-op behavior; update call sites in the same step; existing tests remain valid (Step 3 acceptance). |
| Google redirect URI mismatch | Callback 400s | Exact-match requirement documented; dev default `http://localhost:8081/app/auth/callback` works over HTTP (Google permits localhost); production requires HTTPS. |
| Outbound HTTPS to Google blocked (corp firewall) | Login fails | `idtoken` fetches Google JWKS; document firewall allowlist (`accounts.google.com`, `www.googleapis.com`). |
| Session secret rotation | All sessions invalid | Documented ops note; TTL bounds damage; secret is env-driven. |
| go-jose family CVEs | DoS/RCE-ish | Avoided by construction — no go-oidc import; CI `govulncheck` gates future additions. |
| Tests accidentally skipping auth (all existing tests run unprotected) | False security confidence | New auth-specific suites always build engines with fake auth; integration suite covers one full protected path. |

---

## 14. Out of scope / future work (Phase 2+)

1. **User persistence & ownership** — `UserStore` outbound port (`FindByGoogleSubject`, `Create`) with `sqlite`/`memory` implementations through the existing provider registry; per-user task scoping (`TaskFilter.OwnerId`), user admin.
2. **Server-side sessions** — session table for revocation; swap the cookie session manager behind the same seam (single-file adapter change).
3. **API auth** — `/api` Bearer tokens (jwt/v5 or PASETO per gin-skill guidance) so SPA/curl clients authenticate too.
4. **UX** — “Continue as <email>” account chooser, sign-in error analytics, rate limiting on `/app/auth/*`.
5. **Hardening** — security headers middleware, CSRF tokens on all mutating forms, `Cache-Control: no-store` on `/app` pages.

---

## 15. Decision log (summary)

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Web-only auth scope | Login page is DaisyUI/HTML; `cli`/`httpapi` untouched (Phase 2 item 3). |
| D2 | Auth code + PKCE, state double-submit cookie | Google's documented secure pattern for web apps; defeats CSRF + code interception. |
| D3 | Signed client-side session cookie (gin-contrib/sessions) | Stateless, zero DB changes, fits swap-able architecture; revocation deferred (Phase 2 item 2). |
| D4 | No users table in Phase 1; Google claims are the identity | Keeps the change small and backend-agnostic; Phase 2 adds a `UserStore` port without rework. |
| D5 | `AuthService` in application layer with `GoogleGateway` port; concrete Google client isolated in `internal/auth/google` | Hexagon purity; identical to the repo/clock DI philosophy; fully fake-able tests. |
| D6 | `NewRouter(svc, auth)` with nil ⇒ auth disabled | Zero-config dev/test ergonomics; existing suites stay green; prod requires env. |
| D7 | `x/oauth2/google/idtoken` instead of `go-oidc` | Avoids go-jose dependency entirely (CVE-2026-34986 family) for a Google-only IdP. |