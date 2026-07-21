package controlapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type gatewayKeyView struct {
	ID                 string     `json:"id"`
	OwnerID            string     `json:"ownerId"`
	OwnerName          string     `json:"ownerName"`
	Name               string     `json:"name"`
	Prefix             string     `json:"prefix"`
	Status             string     `json:"status"`
	AuthorizedModelIDs []string   `json:"authorizedModelIds"`
	AuthorizedModels   []string   `json:"authorizedModels"`
	ExpiresAt          *time.Time `json:"expiresAt,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	LastUsedAt         *time.Time `json:"lastUsedAt,omitempty"`
}

type createdGatewayKeyView struct {
	Key    gatewayKeyView `json:"key"`
	Secret string         `json:"secret"`
}

type ownedGatewayKey struct {
	Key       identity.GatewayKey
	OwnerName string
}

func (a *API) createKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OwnerID            uuid.UUID   `json:"ownerId"`
		Name               string      `json:"name"`
		AuthorizedModelIDs []uuid.UUID `json:"authorizedModelIds"`
		ExpiresAt          *time.Time  `json:"expiresAt"`
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
	ownerID := input.OwnerID
	ownerName := principal.DisplayName
	if ownerID != principal.UserID {
		users, err := a.collectUsers(r)
		if err != nil {
			a.writeIdentityError(w, r, err)
			return
		}
		ownerName = ""
		for _, user := range users {
			if user.ID == ownerID {
				ownerName = user.DisplayName
				break
			}
		}
		if ownerName == "" {
			a.writeIdentityError(w, r, identity.ErrNotFound)
			return
		}
	}
	key, err := a.identity.CreateGatewayKey(r.Context(), principal, ownerID, input.Name, input.AuthorizedModelIDs, input.ExpiresAt, mutation)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	view := presentGatewayKey(key, ownerName, "")
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusCreated, createdGatewayKeyView{Key: view, Secret: key.Secret})
}

func (a *API) listKeys(w http.ResponseWriter, r *http.Request) {
	items, err := a.collectKeys(r)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	query := parseListQuery(r)
	views := make([]gatewayKeyView, 0, len(items))
	for _, item := range items {
		view := presentGatewayKey(item.Key, item.OwnerName, "")
		if query.Status != "" && view.Status != query.Status {
			continue
		}
		if !containsFold(view.Name+" "+view.Prefix+" "+view.OwnerName, query.Search) {
			continue
		}
		views = append(views, view)
	}
	writeData(w, http.StatusOK, paginate(views, query))
}

func (a *API) revokeKey(w http.ResponseWriter, r *http.Request) {
	keyID, err := uuid.Parse(chi.URLParam(r, "keyID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	items, err := a.collectKeys(r)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	var selected *ownedGatewayKey
	for index := range items {
		if items[index].Key.ID == keyID {
			selected = &items[index]
			break
		}
	}
	if selected == nil {
		a.writeIdentityError(w, r, identity.ErrNotFound)
		return
	}
	if err := a.identity.RevokeGatewayKey(r.Context(), principalFromContext(r.Context()), keyID); err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, presentGatewayKey(selected.Key, selected.OwnerName, "revoked"))
}

func (a *API) collectKeys(r *http.Request) ([]ownedGatewayKey, error) {
	principal := principalFromContext(r.Context())
	if principal.Role != identity.RoleAdministrator {
		items, err := a.identity.ListGatewayKeys(r.Context(), principal, principal.UserID)
		if err != nil {
			return nil, err
		}
		result := make([]ownedGatewayKey, 0, len(items))
		for _, item := range items {
			result = append(result, ownedGatewayKey{Key: item, OwnerName: principal.DisplayName})
		}
		return result, nil
	}
	users, err := a.collectUsers(r)
	if err != nil {
		return nil, err
	}
	var result []ownedGatewayKey
	for _, user := range users {
		items, err := a.identity.ListGatewayKeys(r.Context(), principal, user.ID)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			result = append(result, ownedGatewayKey{Key: item, OwnerName: user.DisplayName})
		}
	}
	return result, nil
}

func presentGatewayKey(key identity.GatewayKey, ownerName, forcedStatus string) gatewayKeyView {
	status := forcedStatus
	if status == "" {
		switch {
		case key.RevokedAt != nil:
			status = "revoked"
		case key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now().UTC()):
			status = "expired"
		default:
			status = "active"
		}
	}
	modelIDs := make([]string, 0, len(key.AuthorizedModelIDs))
	for _, modelID := range key.AuthorizedModelIDs {
		modelIDs = append(modelIDs, modelID.String())
	}
	return gatewayKeyView{
		ID:                 key.ID.String(),
		OwnerID:            key.UserID.String(),
		OwnerName:          ownerName,
		Name:               key.Name,
		Prefix:             key.Prefix,
		Status:             status,
		AuthorizedModelIDs: modelIDs,
		AuthorizedModels:   append([]string(nil), key.AuthorizedModels...),
		ExpiresAt:          utcTimePointer(key.ExpiresAt),
		CreatedAt:          key.CreatedAt.UTC(),
		LastUsedAt:         utcTimePointer(key.LastUsedAt),
	}
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
