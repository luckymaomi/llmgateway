package controlapi

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type bootstrapRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
}

type registrationRequest struct {
	Invitation  string `json:"invitation"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type setupStatusView struct {
	Required bool `json:"required"`
}

type registrationView struct {
	UserID string        `json:"userId"`
	Role   identity.Role `json:"role"`
	Status string        `json:"status"`
}

type sessionView struct {
	UserID       string        `json:"userId"`
	DisplayName  string        `json:"displayName"`
	Role         identity.Role `json:"role"`
	Capabilities []string      `json:"capabilities"`
	CSRFToken    string        `json:"csrfToken"`
	ExpiresAt    time.Time     `json:"expiresAt"`
}

func (a *API) setupStatus(w http.ResponseWriter, r *http.Request) {
	bootstrapped, err := a.identity.IsBootstrapped(r.Context())
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, setupStatusView{Required: !bootstrapped})
}

func (a *API) bootstrap(w http.ResponseWriter, r *http.Request) {
	var input bootstrapRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	credentials, err := a.identity.Bootstrap(r.Context(), input.Email, input.DisplayName, input.Password)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	a.setSessionCookies(w, credentials.Token, credentials.CSRFToken, credentials.Principal.ExpiresAt)
	writeData(w, http.StatusCreated, presentSession(credentials.Principal, credentials.CSRFToken))
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var input registrationRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	user, err := a.identity.Register(r.Context(), input.Invitation, input.Email, input.DisplayName, input.Password)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	status := "pending_review"
	if user.Status == identity.StatusActive {
		status = "active"
	}
	writeData(w, http.StatusAccepted, registrationView{UserID: user.ID.String(), Role: user.Role, Status: status})
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var input loginRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if a.loginGuard != nil {
		retryAfter, err := a.loginGuard.Check(r.Context(), input.Email, a.clientAddress(r))
		if err != nil {
			a.writeIdentityError(w, r, err)
			return
		}
		if retryAfter > 0 {
			seconds := int64(retryAfter/time.Second) + 1
			w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
			writeProblem(w, r, problem{Status: http.StatusTooManyRequests, Code: "login_rate_limited", Message: "Too many login attempts.", Retryable: true, Stage: "authentication"})
			return
		}
	}
	credentials, err := a.identity.Login(r.Context(), input.Email, input.Password)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	if a.loginGuard != nil {
		if err := a.loginGuard.Reset(r.Context(), input.Email); err != nil && a.logger != nil {
			a.logger.Warn("login rate limit reset failed", "request_id", httpserver.RequestIDFromContext(r.Context()), "error", err)
		}
	}
	a.setSessionCookies(w, credentials.Token, credentials.CSRFToken, credentials.Principal.ExpiresAt)
	writeData(w, http.StatusOK, presentSession(credentials.Principal, credentials.CSRFToken))
}

func (a *API) session(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	csrfCookie, err := r.Cookie(csrfCookieName)
	if err != nil || csrfCookie.Value == "" || !a.identity.VerifyCSRF(principal, csrfCookie.Value) {
		writeProblem(w, r, problem{Status: http.StatusForbidden, Code: "csrf_failed", Message: "CSRF session fact is unavailable.", Stage: "authentication"})
		return
	}
	writeData(w, http.StatusOK, presentSession(principal, csrfCookie.Value))
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.identity.Logout(r.Context(), principalFromContext(r.Context())); err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	a.clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func presentSession(principal identity.Principal, csrfToken string) sessionView {
	return sessionView{
		UserID:       principal.UserID.String(),
		DisplayName:  principal.DisplayName,
		Role:         principal.Role,
		Capabilities: capabilitiesFor(principal.Role),
		CSRFToken:    csrfToken,
		ExpiresAt:    principal.ExpiresAt.UTC(),
	}
}

func capabilitiesFor(role identity.Role) []string {
	switch role {
	case identity.RoleAdministrator:
		return []string{"providers:read", "providers:write", "credentials:read", "credentials:write", "access:read", "access:write", "ledger:read", "ledger:write", "playground:use", "revisions:publish"}
	case identity.RoleMember:
		return []string{"access:read", "ledger:read", "playground:use"}
	default:
		return []string{}
	}
}

func (a *API) clientAddress(r *http.Request) string {
	host := r.RemoteAddr
	if parsedHost, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = parsedHost
	}
	if a.config.TrustedProxy != "" && host == a.config.TrustedProxy {
		forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
		if net.ParseIP(forwarded) != nil {
			return forwarded
		}
	}
	return host
}

func (a *API) setSessionCookies(w http.ResponseWriter, token, csrfToken string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", Expires: expiresAt, HttpOnly: true, Secure: a.config.CookieSecure, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: csrfToken, Path: "/", Expires: expiresAt, HttpOnly: false, Secure: a.config.CookieSecure, SameSite: http.SameSiteStrictMode})
}

func (a *API) clearCookies(w http.ResponseWriter) {
	expired := time.Unix(1, 0).UTC()
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", Expires: expired, MaxAge: -1, HttpOnly: true, Secure: a.config.CookieSecure, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: "", Path: "/", Expires: expired, MaxAge: -1, HttpOnly: false, Secure: a.config.CookieSecure, SameSite: http.SameSiteStrictMode})
}
