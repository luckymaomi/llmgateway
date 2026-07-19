package controlapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
)

type configurationRevisionView struct {
	ID                   string     `json:"id"`
	Sequence             int64      `json:"sequence"`
	Status               string     `json:"status"`
	CreatedBy            string     `json:"createdBy"`
	CreatedAt            time.Time  `json:"createdAt"`
	PublishedAt          *time.Time `json:"publishedAt,omitempty"`
	Summary              string     `json:"summary"`
	ValidationIssueCount int        `json:"validationIssueCount"`
}

type operationView struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Phase     string    `json:"phase"`
	Step      string    `json:"step"`
	Progress  int       `json:"progress"`
	RequestID string    `json:"requestId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	CanCancel bool      `json:"canCancel"`
	Result    any       `json:"result,omitempty"`
}

func (a *API) listConfigurationRevisions(w http.ResponseWriter, r *http.Request) {
	revisions, err := a.collectConfigurationRevisions(r)
	if err != nil {
		a.writeConfigurationError(w, r, err)
		return
	}
	active, activeErr := a.configuration.Active(r.Context(), principalFromContext(r.Context()))
	activeID := uuid.Nil
	if activeErr == nil {
		activeID = active.Revision.ID
	} else if !errors.Is(activeErr, configuration.ErrNotFound) {
		a.writeConfigurationError(w, r, activeErr)
		return
	}
	query := parseListQuery(r)
	views := make([]configurationRevisionView, 0, len(revisions))
	for _, revision := range revisions {
		view := presentConfigurationRevision(revision, activeID)
		if query.Status != "" && view.Status != query.Status {
			continue
		}
		if !containsFold(view.Summary+" "+view.CreatedBy, query.Search) {
			continue
		}
		views = append(views, view)
	}
	writeData(w, http.StatusOK, paginate(views, query))
}

func (a *API) validateConfigurationRevision(w http.ResponseWriter, r *http.Request) {
	revision, err := a.findConfigurationRevision(r)
	if err != nil {
		a.writeConfigurationError(w, r, err)
		return
	}
	if err := revision.Document.Validate(); err != nil {
		a.writeConfigurationError(w, r, err)
		return
	}
	now := time.Now().UTC()
	writeData(w, http.StatusOK, operationView{
		ID:        "configuration.validate." + revision.ID.String(),
		Kind:      "configuration.validate",
		Phase:     "completed",
		Step:      "Configuration validation completed.",
		Progress:  100,
		RequestID: httpserver.RequestIDFromContext(r.Context()),
		CreatedAt: now,
		UpdatedAt: now,
		CanCancel: false,
		Result:    presentConfigurationRevision(revision, uuid.Nil),
	})
}

func (a *API) publishConfigurationRevision(w http.ResponseWriter, r *http.Request) {
	a.publishConfiguration(w, r, "publish")
}

func (a *API) rollbackConfigurationRevision(w http.ResponseWriter, r *http.Request) {
	a.publishConfiguration(w, r, "rollback")
}

func (a *API) publishConfiguration(w http.ResponseWriter, r *http.Request, action string) {
	revisionID, err := uuid.Parse(chi.URLParam(r, "revisionID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		ExpectedActiveRevisionID string `json:"expectedActiveRevisionId"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	expectedVersion := int64(0)
	active, activeErr := a.configuration.Active(r.Context(), principalFromContext(r.Context()))
	switch {
	case activeErr == nil:
		expectedID, parseErr := uuid.Parse(input.ExpectedActiveRevisionID)
		if parseErr != nil || expectedID != active.Revision.ID {
			a.writeConfigurationError(w, r, configuration.ErrConflict)
			return
		}
		expectedVersion = active.Version
	case errors.Is(activeErr, configuration.ErrNotFound):
		if input.ExpectedActiveRevisionID != "" {
			a.writeConfigurationError(w, r, configuration.ErrConflict)
			return
		}
	default:
		a.writeConfigurationError(w, r, activeErr)
		return
	}
	published, err := a.configuration.Publish(r.Context(), principalFromContext(r.Context()), revisionID, expectedVersion)
	if err != nil {
		a.writeConfigurationError(w, r, err)
		return
	}
	step := "Configuration published."
	if action == "rollback" {
		step = "Configuration rollback completed."
	}
	writeData(w, http.StatusOK, operationView{
		ID:        "configuration." + action + "." + revisionID.String(),
		Kind:      "configuration." + action,
		Phase:     "completed",
		Step:      step,
		Progress:  100,
		RequestID: httpserver.RequestIDFromContext(r.Context()),
		CreatedAt: published.UpdatedAt.UTC(),
		UpdatedAt: published.UpdatedAt.UTC(),
		CanCancel: false,
		Result:    presentConfigurationRevision(published.Revision, published.Revision.ID),
	})
}

func (a *API) findConfigurationRevision(r *http.Request) (configuration.Revision, error) {
	revisionID, err := uuid.Parse(chi.URLParam(r, "revisionID"))
	if err != nil {
		return configuration.Revision{}, configuration.ErrInvalidInput
	}
	revisions, err := a.collectConfigurationRevisions(r)
	if err != nil {
		return configuration.Revision{}, err
	}
	for _, revision := range revisions {
		if revision.ID == revisionID {
			return revision, nil
		}
	}
	return configuration.Revision{}, configuration.ErrNotFound
}

func (a *API) collectConfigurationRevisions(r *http.Request) ([]configuration.Revision, error) {
	principal := principalFromContext(r.Context())
	var all []configuration.Revision
	for offset := int32(0); ; offset += 200 {
		items, err := a.configuration.ListRevisions(r.Context(), principal, offset, 200)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < 200 {
			return all, nil
		}
	}
}

func presentConfigurationRevision(revision configuration.Revision, activeID uuid.UUID) configurationRevisionView {
	status := "draft"
	if revision.ID == activeID {
		status = "published"
	} else if revision.PublishedAt != nil {
		status = "superseded"
	}
	summary := revision.Checksum
	if len(summary) > 12 {
		summary = summary[:12]
	}
	return configurationRevisionView{
		ID:                   revision.ID.String(),
		Sequence:             revision.Revision,
		Status:               status,
		CreatedBy:            revision.CreatedBy.String(),
		CreatedAt:            revision.CreatedAt.UTC(),
		PublishedAt:          utcTimePointer(revision.PublishedAt),
		Summary:              "SHA-256 " + summary,
		ValidationIssueCount: 0,
	}
}
