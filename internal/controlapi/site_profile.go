package controlapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/siteprofile"
)

type siteProfileService interface {
	Get(context.Context) (siteprofile.Profile, error)
	Update(context.Context, identity.Principal, siteprofile.Update) (siteprofile.Profile, error)
}

type SiteProfileAPI struct {
	service siteProfileService
	logger  *slog.Logger
}

type siteProfileView struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Contact     string    `json:"contact"`
	Version     int64     `json:"version"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func NewSiteProfileAPI(service siteProfileService, logger *slog.Logger) *SiteProfileAPI {
	if service == nil {
		panic("site profile service is required")
	}
	if logger == nil {
		panic("site profile logger is required")
	}
	return &SiteProfileAPI{service: service, logger: logger}
}

func (a *SiteProfileAPI) RegisterAuthenticatedRoutes(router chi.Router, authorizationMiddleware, mutationMiddleware func(http.Handler) http.Handler) {
	router.With(authorizationMiddleware, mutationMiddleware).Put("/site-profile", a.update)
}

func (a *SiteProfileAPI) get(w http.ResponseWriter, r *http.Request) {
	profile, err := a.service.Get(r.Context())
	if err != nil {
		a.logger.Error("site profile read failed", "request_id", httpserver.RequestIDFromContext(r.Context()), "error", err)
		writeProblem(w, r, problem{Status: http.StatusServiceUnavailable, Code: "site_profile_unavailable", Message: "Site profile is unavailable.", Retryable: true})
		return
	}
	writeData(w, http.StatusOK, presentSiteProfile(profile))
}

func (a *SiteProfileAPI) update(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name            string `json:"name"`
		Description     string `json:"description"`
		Contact         string `json:"contact"`
		ExpectedVersion int64  `json:"expectedVersion"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	profile, err := a.service.Update(r.Context(), principalFromContext(r.Context()), siteprofile.Update{
		Name: input.Name, Description: input.Description, Contact: input.Contact,
		ExpectedVersion: input.ExpectedVersion, RequestID: httpserver.RequestIDFromContext(r.Context()),
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, presentSiteProfile(profile))
}

func (a *SiteProfileAPI) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, siteprofile.ErrInvalidInput):
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_site_profile", Message: "Site profile input is invalid.", Stage: "site_profile"})
	case errors.Is(err, siteprofile.ErrForbidden):
		writeProblem(w, r, problem{Status: http.StatusForbidden, Code: "forbidden", Message: "Forbidden.", Stage: "site_profile"})
	case errors.Is(err, siteprofile.ErrConflict):
		writeProblem(w, r, problem{Status: http.StatusConflict, Code: "conflict", Message: "Site profile changed before this update.", Stage: "site_profile", Retryable: true})
	default:
		a.logger.Error("site profile update failed", "request_id", httpserver.RequestIDFromContext(r.Context()), "error", err)
		writeProblem(w, r, problem{Status: http.StatusServiceUnavailable, Code: "site_profile_unavailable", Message: "Site profile is unavailable.", Stage: "site_profile", Retryable: true})
	}
}

func presentSiteProfile(profile siteprofile.Profile) siteProfileView {
	return siteProfileView{
		Name: profile.Name, Description: profile.Description, Contact: profile.Contact,
		Version: profile.Version, UpdatedAt: profile.UpdatedAt.UTC(),
	}
}
