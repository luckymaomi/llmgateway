package controlapi

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/luckymaomi/llmgateway/internal/identity"
)

type principalContextKey struct{}

func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			a.writeIdentityError(w, r, identity.ErrInvalidCredential)
			return
		}
		principal, err := a.identity.AuthenticateSession(r.Context(), cookie.Value)
		if err != nil {
			a.clearCookies(w)
			a.writeIdentityError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
	})
}

func (a *API) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csrfCookie, err := r.Cookie(csrfCookieName)
		csrfHeader := r.Header.Get("X-CSRF-Token")
		if err != nil || csrfCookie.Value == "" || csrfHeader == "" || subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(csrfHeader)) != 1 || !a.identity.VerifyCSRF(principalFromContext(r.Context()), csrfHeader) {
			writeProblem(w, r, problem{
				Status:  http.StatusForbidden,
				Code:    "csrf_failed",
				Message: "CSRF validation failed.",
				Stage:   "authentication",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) requireAdministrator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !principalFromContext(r.Context()).CanManageUsers() {
			a.writeIdentityError(w, r, identity.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) requireProviderAdministrator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !principalFromContext(r.Context()).CanOperateProviders() {
			a.writeIdentityError(w, r, identity.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func principalFromContext(ctx context.Context) identity.Principal {
	principal, _ := ctx.Value(principalContextKey{}).(identity.Principal)
	return principal
}
