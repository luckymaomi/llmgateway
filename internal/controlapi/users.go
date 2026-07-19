package controlapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type userView struct {
	ID                   string        `json:"id"`
	DisplayName          string        `json:"displayName"`
	Email                string        `json:"email"`
	Role                 identity.Role `json:"role"`
	Status               string        `json:"status"`
	ModelCount           int           `json:"modelCount"`
	KeyCount             int           `json:"keyCount"`
	QuotaRemainingTokens *int64        `json:"quotaRemainingTokens,omitempty"`
	CreatedAt            time.Time     `json:"createdAt"`
	LastActiveAt         *time.Time    `json:"lastActiveAt,omitempty"`
}

type invitationView struct {
	ID         string        `json:"id"`
	CodePrefix string        `json:"codePrefix"`
	Role       identity.Role `json:"role"`
	Status     string        `json:"status"`
	ExpiresAt  time.Time     `json:"expiresAt"`
	CreatedBy  string        `json:"createdBy"`
	ClaimedBy  *string       `json:"claimedBy,omitempty"`
	Code       string        `json:"code,omitempty"`
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.collectUsers(r)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	query := parseListQuery(r)
	status, statusErr := identityStatusFilter(query.Status)
	if statusErr != nil {
		writeDecodeError(w, r, statusErr)
		return
	}
	filtered := make([]identity.User, 0, len(users))
	for _, user := range users {
		if status != nil && user.Status != *status {
			continue
		}
		if !containsFold(user.DisplayName+" "+user.Email, query.Search) {
			continue
		}
		filtered = append(filtered, user)
	}
	views := make([]userView, 0, len(filtered))
	principal := principalFromContext(r.Context())
	for _, user := range filtered {
		keys, err := a.identity.ListGatewayKeys(r.Context(), principal, user.ID)
		if err != nil {
			a.writeIdentityError(w, r, err)
			return
		}
		views = append(views, presentUser(user, len(keys)))
	}
	writeData(w, http.StatusOK, paginate(views, query))
}

func (a *API) reviewUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		Decision string `json:"decision"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var status identity.Status
	switch input.Decision {
	case "approve", "activate":
		status = identity.StatusActive
	case "suspend":
		status = identity.StatusDisabled
	default:
		writeDecodeError(w, r, identity.ErrInvalidInput)
		return
	}
	user, err := a.identity.SetUserStatus(r.Context(), principalFromContext(r.Context()), userID, status)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	keys, err := a.identity.ListGatewayKeys(r.Context(), principalFromContext(r.Context()), user.ID)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, presentUser(user, len(keys)))
}

func (a *API) createInvitation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Role      identity.Role `json:"role"`
		ExpiresAt time.Time     `json:"expiresAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	validFor := time.Until(input.ExpiresAt)
	invitation, err := a.identity.CreateInvitation(r.Context(), principalFromContext(r.Context()), input.Role, validFor)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, presentInvitation(invitation, principalFromContext(r.Context()).DisplayName, ""))
}

func (a *API) listInvitations(w http.ResponseWriter, r *http.Request) {
	items, err := a.collectInvitations(r)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	query := parseListQuery(r)
	views := make([]invitationView, 0, len(items))
	for _, item := range items {
		view := presentInvitation(item, "", "")
		if query.Status != "" && view.Status != query.Status {
			continue
		}
		if !containsFold(view.CodePrefix+" "+string(view.Role), query.Search) {
			continue
		}
		views = append(views, view)
	}
	writeData(w, http.StatusOK, paginate(views, query))
}

func (a *API) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "invitationID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	items, err := a.collectInvitations(r)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	var selected *identity.Invitation
	for index := range items {
		if items[index].ID == id {
			selected = &items[index]
			break
		}
	}
	if selected == nil {
		a.writeIdentityError(w, r, identity.ErrNotFound)
		return
	}
	if err := a.identity.RevokeInvitation(r.Context(), principalFromContext(r.Context()), id); err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, presentInvitation(*selected, "", "revoked"))
}

func (a *API) collectUsers(r *http.Request) ([]identity.User, error) {
	principal := principalFromContext(r.Context())
	var all []identity.User
	for offset := int32(0); ; offset += 200 {
		page, err := a.identity.ListUsers(r.Context(), principal, nil, identity.Page{Offset: offset, Size: 200})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if int64(len(all)) >= page.Total || len(page.Items) == 0 {
			return all, nil
		}
	}
}

func (a *API) collectInvitations(r *http.Request) ([]identity.Invitation, error) {
	principal := principalFromContext(r.Context())
	var all []identity.Invitation
	for offset := int32(0); ; offset += 200 {
		items, err := a.identity.ListInvitations(r.Context(), principal, identity.Page{Offset: offset, Size: 200})
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < 200 {
			return all, nil
		}
	}
}

func presentUser(user identity.User, keyCount int) userView {
	return userView{
		ID:          user.ID.String(),
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Role:        user.Role,
		Status:      presentIdentityStatus(user.Status),
		ModelCount:  0,
		KeyCount:    keyCount,
		CreatedAt:   user.CreatedAt.UTC(),
	}
}

func presentInvitation(invitation identity.Invitation, createdBy, forcedStatus string) invitationView {
	status := forcedStatus
	if status == "" {
		switch {
		case invitation.RevokedAt != nil:
			status = "revoked"
		case invitation.ClaimedAt != nil:
			status = "claimed"
		case !invitation.ExpiresAt.After(time.Now().UTC()):
			status = "expired"
		default:
			status = "issued"
		}
	}
	return invitationView{
		ID:         invitation.ID.String(),
		CodePrefix: prefix(invitation.Code, 13),
		Role:       invitation.Role,
		Status:     status,
		ExpiresAt:  invitation.ExpiresAt.UTC(),
		CreatedBy:  createdBy,
		Code:       invitation.Code,
	}
}

func identityStatusFilter(value string) (*identity.Status, error) {
	if value == "" {
		return nil, nil
	}
	status := identity.Status("")
	switch value {
	case "pending_review":
		status = identity.StatusPending
	case "active":
		status = identity.StatusActive
	case "suspended":
		status = identity.StatusDisabled
	default:
		return nil, identity.ErrInvalidInput
	}
	return &status, nil
}

func presentIdentityStatus(status identity.Status) string {
	switch status {
	case identity.StatusPending:
		return "pending_review"
	case identity.StatusDisabled:
		return "suspended"
	default:
		return string(status)
	}
}

func containsFold(value, search string) bool {
	return search == "" || strings.Contains(strings.ToLower(value), strings.ToLower(search))
}
