package controlapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type userView struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"displayName"`
	Email       string          `json:"email"`
	Role        identity.Role   `json:"role"`
	Status      identity.Status `json:"status"`
	KeyCount    int             `json:"keyCount"`
	DisabledAt  *time.Time      `json:"disabledAt,omitempty"`
	DeletedAt   *time.Time      `json:"deletedAt,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type createdMemberView struct {
	Member          userView `json:"member"`
	InitialPassword string   `json:"initialPassword"`
}

type sessionRevocationView struct {
	RevokedSessions int64 `json:"revokedSessions"`
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	query := parseListQuery(r)
	status, err := identityStatusFilter(query.Status)
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	page, err := a.identity.ListUsers(r.Context(), principalFromContext(r.Context()), status, query.Search, identity.Page{Offset: query.offset(), Size: int32(query.PageSize)})
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	views := make([]userView, 0, len(page.Items))
	for _, user := range page.Items {
		keys, listErr := a.identity.ListGatewayKeys(r.Context(), principalFromContext(r.Context()), user.ID)
		if listErr != nil {
			a.writeIdentityError(w, r, listErr)
			return
		}
		views = append(views, presentUser(user, len(keys)))
	}
	writeData(w, http.StatusOK, pageView[userView]{Items: views, Total: page.Total, Page: query.Page, PageSize: query.PageSize})
}

func (a *API) createMember(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := identityMutationRequest(w, r)
	if !ok {
		return
	}
	created, err := a.identity.CreateMember(r.Context(), principalFromContext(r.Context()), input.Email, input.DisplayName, mutation)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusCreated, createdMemberView{Member: presentUser(created.User, 0), InitialPassword: created.InitialPassword})
}

func (a *API) updateMember(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathUUID(w, r, "userID")
	if !ok {
		return
	}
	var input struct {
		Email             string    `json:"email"`
		DisplayName       string    `json:"displayName"`
		ExpectedUpdatedAt time.Time `json:"expectedUpdatedAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := identityMutationRequest(w, r)
	if !ok {
		return
	}
	user, err := a.identity.UpdateMember(r.Context(), principalFromContext(r.Context()), identity.MemberChange{ID: userID, Email: input.Email, DisplayName: input.DisplayName, ExpectedUpdatedAt: input.ExpectedUpdatedAt}, mutation)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, presentUser(user, 0))
}

func (a *API) setUserStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathUUID(w, r, "userID")
	if !ok {
		return
	}
	var input struct {
		Status identity.Status `json:"status"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := identityMutationRequest(w, r)
	if !ok {
		return
	}
	user, err := a.identity.SetUserStatus(r.Context(), principalFromContext(r.Context()), userID, input.Status, mutation)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, presentUser(user, 0))
}

func (a *API) deleteMember(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathUUID(w, r, "userID")
	if !ok {
		return
	}
	mutation, ok := identityMutationRequest(w, r)
	if !ok {
		return
	}
	user, err := a.identity.DeleteMember(r.Context(), principalFromContext(r.Context()), userID, mutation)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, presentUser(user, 0))
}

func (a *API) resetMemberPassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathUUID(w, r, "userID")
	if !ok {
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

func identityStatusFilter(value string) (*identity.Status, error) {
	if value == "" {
		return nil, nil
	}
	status := identity.Status(value)
	if status != identity.StatusActive && status != identity.StatusDisabled {
		return nil, identity.ErrInvalidInput
	}
	return &status, nil
}

func presentUser(user identity.User, keyCount int) userView {
	return userView{ID: user.ID.String(), DisplayName: user.DisplayName, Email: user.Email, Role: user.Role, Status: user.Status, KeyCount: keyCount,
		DisabledAt: utcTimePointer(user.DisabledAt), DeletedAt: utcTimePointer(user.DeletedAt), CreatedAt: user.CreatedAt.UTC(), UpdatedAt: user.UpdatedAt.UTC()}
}

func (a *API) collectUsers(r *http.Request) ([]identity.User, error) {
	principal := principalFromContext(r.Context())
	var all []identity.User
	for offset := int32(0); ; offset += 200 {
		page, err := a.identity.ListUsers(r.Context(), principal, nil, "", identity.Page{Offset: offset, Size: 200})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if int64(len(all)) >= page.Total || len(page.Items) == 0 {
			return all, nil
		}
	}
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeDecodeError(w, r, err)
		return uuid.Nil, false
	}
	return id, true
}
