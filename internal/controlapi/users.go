package controlapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
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
	ID         string    `json:"id"`
	CodePrefix string    `json:"codePrefix"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expiresAt"`
	CreatedBy  string    `json:"createdBy"`
	ClaimedBy  *string   `json:"claimedBy,omitempty"`
}

type createdInvitationView struct {
	Invitation invitationView `json:"invitation"`
	Code       string         `json:"code"`
}

type sessionRevocationView struct {
	RevokedSessions int64 `json:"revokedSessions"`
}

type namedInvitation struct {
	Invitation identity.Invitation
	CreatedBy  string
	ClaimedBy  *string
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

func (a *API) resetMemberPassword(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := identityMutationRequest(w, r)
	if !ok {
		input.NewPassword = ""
		return
	}
	result, err := a.identity.ResetMemberPassword(r.Context(), principalFromContext(r.Context()), userID, input.NewPassword, mutation)
	input.NewPassword = ""
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, sessionRevocationView{RevokedSessions: result.RevokedSessions})
}

func (a *API) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	principal := principalFromContext(r.Context())
	result, err := a.identity.RevokeUserSessions(r.Context(), principal, userID, httpserver.RequestIDFromContext(r.Context()))
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, sessionRevocationView{RevokedSessions: result.RevokedSessions})
}

func (a *API) createInvitation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := identityMutationRequest(w, r)
	if !ok {
		return
	}
	principal := principalFromContext(r.Context())
	invitation, err := a.identity.CreateInvitation(r.Context(), principal, input.ExpiresAt, mutation)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusCreated, createdInvitationView{
		Invitation: presentInvitation(namedInvitation{Invitation: invitation, CreatedBy: principal.DisplayName}, ""),
		Code:       invitation.Code,
	})
}

func (a *API) listInvitations(w http.ResponseWriter, r *http.Request) {
	items, err := a.collectNamedInvitations(r)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	query := parseListQuery(r)
	views := make([]invitationView, 0, len(items))
	for _, item := range items {
		view := presentInvitation(item, "")
		if query.Status != "" && view.Status != query.Status {
			continue
		}
		claimedBy := ""
		if view.ClaimedBy != nil {
			claimedBy = *view.ClaimedBy
		}
		if !containsFold(view.CodePrefix+" "+view.CreatedBy+" "+claimedBy, query.Search) {
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
	items, err := a.collectNamedInvitations(r)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	var selected *namedInvitation
	for index := range items {
		if items[index].Invitation.ID == id {
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
	writeData(w, http.StatusOK, presentInvitation(*selected, "revoked"))
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

func (a *API) collectNamedInvitations(r *http.Request) ([]namedInvitation, error) {
	items, err := a.collectInvitations(r)
	if err != nil {
		return nil, err
	}
	userIDs := make([]uuid.UUID, 0, len(items)*2)
	seen := make(map[uuid.UUID]struct{}, len(items)*2)
	for _, item := range items {
		if item.CreatedBy == uuid.Nil {
			return nil, fmt.Errorf("identity presentation: invitation %s has no creator", item.ID)
		}
		if _, exists := seen[item.CreatedBy]; !exists {
			seen[item.CreatedBy] = struct{}{}
			userIDs = append(userIDs, item.CreatedBy)
		}
		if item.ClaimedBy != nil {
			if *item.ClaimedBy == uuid.Nil {
				return nil, fmt.Errorf("identity presentation: invitation %s has an invalid claimant", item.ID)
			}
			if _, exists := seen[*item.ClaimedBy]; !exists {
				seen[*item.ClaimedBy] = struct{}{}
				userIDs = append(userIDs, *item.ClaimedBy)
			}
		}
	}
	if len(userIDs) == 0 {
		return []namedInvitation{}, nil
	}
	names, err := a.identity.UserDisplayNames(r.Context(), principalFromContext(r.Context()), userIDs)
	if err != nil {
		return nil, err
	}
	result := make([]namedInvitation, 0, len(items))
	for _, item := range items {
		createdBy := names[item.CreatedBy]
		if strings.TrimSpace(createdBy) == "" {
			return nil, fmt.Errorf("identity presentation: creator %s has no display name", item.CreatedBy)
		}
		var claimedBy *string
		if item.ClaimedBy != nil {
			name := names[*item.ClaimedBy]
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("identity presentation: claimant %s has no display name", *item.ClaimedBy)
			}
			claimedBy = &name
		}
		result = append(result, namedInvitation{Invitation: item, CreatedBy: createdBy, ClaimedBy: claimedBy})
	}
	return result, nil
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

func presentInvitation(item namedInvitation, forcedStatus string) invitationView {
	invitation := item.Invitation
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
		CodePrefix: invitation.CodePrefix,
		Status:     status,
		ExpiresAt:  invitation.ExpiresAt.UTC(),
		CreatedBy:  item.CreatedBy,
		ClaimedBy:  item.ClaimedBy,
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
