package controlapi

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

func TestAuthenticationContract(t *testing.T) {
	fixture := newControlFixture(t)

	statusResponse := request(t, fixture.handler, http.MethodGet, "/api/control/setup/status", nil, false, false)
	requireStatus(t, statusResponse, http.StatusOK)
	status := decodeData[setupStatusView](t, statusResponse)
	if !status.Required {
		t.Fatal("setup should be required before bootstrap")
	}

	bootstrapResponse := request(t, fixture.handler, http.MethodPost, "/api/control/setup", map[string]any{
		"displayName": "Primary Admin",
		"email":       "owner@example.test",
		"password":    "correct horse battery staple",
	}, false, false)
	requireStatus(t, bootstrapResponse, http.StatusCreated)
	session := decodeData[sessionView](t, bootstrapResponse)
	if session.UserID != fixture.adminID.String() || session.Role != identity.RoleAdministrator || session.CSRFToken != "csrf-test-token" {
		t.Fatalf("unexpected bootstrap session: %+v", session)
	}
	if fixture.identity.bootstrapEmail != "owner@example.test" || fixture.identity.bootstrapName != "Primary Admin" {
		t.Fatalf("bootstrap input was not preserved: email=%q name=%q", fixture.identity.bootstrapEmail, fixture.identity.bootstrapName)
	}
	cookies := bootstrapResponse.Result().Cookies()
	if len(cookies) != 2 || cookies[0].Name != sessionCookieName || cookies[1].Name != csrfCookieName {
		t.Fatalf("unexpected session cookies: %+v", cookies)
	}

	loginResponse := request(t, fixture.handler, http.MethodPost, "/api/control/session", map[string]string{
		"email": "admin@example.test", "password": "valid password",
	}, false, false)
	requireStatus(t, loginResponse, http.StatusOK)
	loggedIn := decodeData[sessionView](t, loginResponse)
	if loggedIn.DisplayName != "Admin" || len(loggedIn.Capabilities) == 0 {
		t.Fatalf("unexpected login session: %+v", loggedIn)
	}

	sessionResponse := request(t, fixture.handler, http.MethodGet, "/api/control/session", nil, true, false)
	requireStatus(t, sessionResponse, http.StatusOK)
	current := decodeData[sessionView](t, sessionResponse)
	if current.UserID != fixture.adminID.String() || current.CSRFToken != "csrf-test-token" {
		t.Fatalf("unexpected current session: %+v", current)
	}

	registrationResponse := request(t, fixture.handler, http.MethodPost, "/api/control/registrations", map[string]string{
		"invitation":  "invite_once_secret",
		"displayName": "New Member",
		"email":       "new@example.test",
		"password":    "valid password",
	}, false, false)
	requireStatus(t, registrationResponse, http.StatusAccepted)
	registration := decodeData[registrationView](t, registrationResponse)
	if registration.UserID != fixture.identity.registered.ID.String() || registration.Role != identity.RoleMember || registration.Status != "pending_review" {
		t.Fatalf("unexpected registration: %+v", registration)
	}
	if fixture.identity.registrationCode != "invite_once_secret" {
		t.Fatalf("registration invitation = %q", fixture.identity.registrationCode)
	}
}

func TestAccessManagementContract(t *testing.T) {
	fixture := newControlFixture(t)
	fixture.identity.keys[fixture.memberID] = []identity.GatewayKey{{
		ID: uuidForTest("1971eeb0-8b6a-48b4-8df8-8b8a6d28353e"), UserID: fixture.memberID, Name: "Automation", Prefix: "llmg_member", CreatedAt: fixture.now,
	}}

	usersResponse := request(t, fixture.handler, http.MethodGet, "/api/control/users?page=1&pageSize=20", nil, true, false)
	requireStatus(t, usersResponse, http.StatusOK)
	users := decodeData[pageView[userView]](t, usersResponse)
	if users.Total != 2 || len(users.Items) != 2 || users.Items[1].KeyCount != 1 || users.Items[1].Status != "pending_review" {
		t.Fatalf("unexpected users page: %+v", users)
	}

	reviewResponse := request(t, fixture.handler, http.MethodPost, "/api/control/users/"+fixture.memberID.String()+"/review", map[string]string{"decision": "approve"}, true, true)
	requireStatus(t, reviewResponse, http.StatusOK)
	reviewed := decodeData[userView](t, reviewResponse)
	if reviewed.Status != "active" || fixture.identity.reviewedUserID != fixture.memberID || fixture.identity.reviewedStatus != identity.StatusActive {
		t.Fatalf("unexpected reviewed user: %+v", reviewed)
	}

	expiresAt := time.Now().UTC().Add(48 * time.Hour)
	invitationResponse := request(t, fixture.handler, http.MethodPost, "/api/control/invitations", map[string]any{
		"role": identity.RoleMember, "expiresAt": expiresAt,
	}, true, true)
	requireStatus(t, invitationResponse, http.StatusCreated)
	invitation := decodeData[invitationView](t, invitationResponse)
	if invitation.Status != "issued" || invitation.Role != identity.RoleMember || invitation.Code != "invite_once_secret" || fixture.identity.createdInvitationFor <= 0 {
		t.Fatalf("unexpected invitation: %+v", invitation)
	}

	keyResponse := request(t, fixture.handler, http.MethodPost, "/api/control/keys", map[string]any{
		"ownerId": fixture.memberID.String(), "name": "CI", "authorizedModels": []string{},
	}, true, true)
	requireStatus(t, keyResponse, http.StatusCreated)
	created := decodeData[createdGatewayKeyView](t, keyResponse)
	if created.Secret != "llmg_one_time_secret" || created.Key.OwnerID != fixture.memberID.String() || created.Key.OwnerName != "Member" || created.Key.Status != "active" {
		t.Fatalf("unexpected gateway key: %+v", created)
	}
}

func TestSessionCapabilityContract(t *testing.T) {
	tests := []struct {
		name         string
		role         identity.Role
		capabilities []string
	}{
		{name: "administrator", role: identity.RoleAdministrator, capabilities: []string{"providers:read", "providers:write", "access:read", "access:write", "revisions:publish"}},
		{name: "operator", role: identity.RoleOperator, capabilities: []string{"providers:read", "providers:write", "revisions:publish"}},
		{name: "member", role: identity.RoleMember, capabilities: []string{"access:read"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newControlFixture(t)
			fixture.identity.principal.Role = test.role

			response := request(t, fixture.handler, http.MethodGet, "/api/control/session", nil, true, false)
			requireStatus(t, response, http.StatusOK)
			session := decodeData[sessionView](t, response)
			if session.Role != test.role || !slices.Equal(session.Capabilities, test.capabilities) {
				t.Fatalf("session role/capabilities = %s/%v, want %s/%v", session.Role, session.Capabilities, test.role, test.capabilities)
			}
		})
	}
}

func uuidForTest(value string) uuid.UUID {
	return uuid.MustParse(value)
}
