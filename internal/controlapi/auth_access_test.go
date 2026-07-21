package controlapi

import (
	"net/http"
	"slices"
	"strings"
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
	invitationResponse := request(t, fixture.handler, http.MethodPost, "/api/control/invitations", map[string]any{"expiresAt": expiresAt}, true, true)
	requireStatus(t, invitationResponse, http.StatusCreated)
	invitation := decodeData[createdInvitationView](t, invitationResponse)
	if invitation.Invitation.Status != "issued" || invitation.Invitation.CreatedBy != "Admin" ||
		invitation.Code != "invite_once_secret" || !fixture.identity.createdInvitationAt.Equal(expiresAt) || fixture.identity.createdInvitationKey.IdempotencyKey == uuid.Nil || fixture.identity.createdInvitationKey.RequestID == "" {
		t.Fatal("invitation creation did not preserve its command, presentation, and one-time response boundary")
	}
	if invitationResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invitation secret response was cacheable")
	}
	if len(fixture.identity.displayNameCalls) != 0 {
		t.Fatal("invitation creation performed a display-name lookup after its mutation")
	}
	claimTime := fixture.now.Add(2 * time.Minute)
	fixture.identity.invitations[0].ClaimedBy = &fixture.memberID
	fixture.identity.invitations[0].ClaimedAt = &claimTime
	listResponse := request(t, fixture.handler, http.MethodGet, "/api/control/invitations?page=1&pageSize=20", nil, true, false)
	requireStatus(t, listResponse, http.StatusOK)
	listed := decodeData[pageView[invitationView]](t, listResponse)
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].CreatedBy != "Admin" || listed.Items[0].ClaimedBy == nil || *listed.Items[0].ClaimedBy != "Member" {
		t.Fatalf("invitation identity presentation = %+v", listed)
	}
	if len(fixture.identity.displayNameCalls) != 1 || !slices.Equal(fixture.identity.displayNameCalls[0], []uuid.UUID{fixture.adminID, fixture.memberID}) {
		t.Fatalf("invitation display-name batches = %v", fixture.identity.displayNameCalls)
	}
	if strings.Contains(listResponse.Body.String(), "invite_once_secret") || strings.Contains(listResponse.Body.String(), `"code"`) {
		t.Fatal("invitation list exposed the one-time code field")
	}

	modelID := fixture.activeModelID
	idempotencyKey := uuid.New()
	keyResponse := requestWithIdempotencyKey(t, fixture.handler, http.MethodPost, "/api/control/keys", map[string]any{
		"ownerId": fixture.memberID.String(), "name": "CI", "authorizedModelIds": []string{modelID.String()},
	}, true, true, idempotencyKey.String())
	requireStatus(t, keyResponse, http.StatusCreated)
	created := decodeData[createdGatewayKeyView](t, keyResponse)
	if keyResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("gateway-key secret response was cacheable")
	}
	if created.Secret != "llmg_one_time_secret" || created.Key.OwnerID != fixture.memberID.String() || created.Key.OwnerName != "Member" || created.Key.Status != "active" ||
		!slices.Equal(created.Key.AuthorizedModelIDs, []string{modelID.String()}) || !slices.Equal(created.Key.AuthorizedModels, []string{"fast"}) {
		t.Fatalf("unexpected gateway key: %+v", created)
	}
	if fixture.identity.createdKeyOwnerID != fixture.memberID || fixture.identity.createdKeyName != "CI" ||
		!slices.Equal(fixture.identity.createdKeyModelIDs, []uuid.UUID{modelID}) || fixture.identity.createdKeyMutation.IdempotencyKey != idempotencyKey || fixture.identity.createdKeyMutation.RequestID == "" {
		t.Fatalf("unexpected gateway key command: owner=%s name=%q models=%v mutation=%+v", fixture.identity.createdKeyOwnerID, fixture.identity.createdKeyName, fixture.identity.createdKeyModelIDs, fixture.identity.createdKeyMutation)
	}
}

func TestInvitationMutationRequiresIdempotencyKey(t *testing.T) {
	fixture := newControlFixture(t)
	body := map[string]any{"expiresAt": time.Now().UTC().Add(24 * time.Hour)}
	for _, key := range []string{"", "not-a-uuid", uuid.Nil.String()} {
		response := requestWithIdempotencyKey(t, fixture.handler, http.MethodPost, "/api/control/invitations", body, true, true, key)
		requireStatus(t, response, http.StatusBadRequest)
	}
	if !fixture.identity.createdInvitationAt.IsZero() {
		t.Fatal("invitation command reached identity without a valid idempotency key")
	}
}

func TestGatewayKeyMutationRequiresIdempotencyKey(t *testing.T) {
	fixture := newControlFixture(t)

	response := requestWithIdempotencyKey(t, fixture.handler, http.MethodPost, "/api/control/keys", map[string]any{
		"ownerId": fixture.memberID.String(), "name": "CI", "authorizedModelIds": []string{fixture.activeModelID.String()},
	}, true, true, "")
	requireStatus(t, response, http.StatusBadRequest)
	if fixture.identity.createdKeyOwnerID != uuid.Nil {
		t.Fatalf("gateway key command reached identity without an idempotency key: %s", fixture.identity.createdKeyOwnerID)
	}
}

func TestGatewayKeyRevocationHTTPIsReplayableAndHidesForeignKeys(t *testing.T) {
	fixture := newControlFixture(t)
	fixture.identity.principal.UserID = fixture.memberID
	fixture.identity.principal.DisplayName = "Member"
	fixture.identity.principal.Role = identity.RoleMember
	ownedKeyID := uuid.New()
	foreignKeyID := uuid.New()
	fixture.identity.keys[fixture.memberID] = []identity.GatewayKey{{
		ID: ownedKeyID, UserID: fixture.memberID, Name: "Owned", Prefix: "llmg_owned", CreatedAt: fixture.now,
	}}
	fixture.identity.keys[fixture.adminID] = []identity.GatewayKey{{
		ID: foreignKeyID, UserID: fixture.adminID, Name: "Foreign", Prefix: "llmg_foreign", CreatedAt: fixture.now,
	}}

	for attempt := 1; attempt <= 2; attempt++ {
		response := request(t, fixture.handler, http.MethodPost, "/api/control/keys/"+ownedKeyID.String()+"/revoke", nil, true, true)
		requireStatus(t, response, http.StatusOK)
		revoked := decodeData[gatewayKeyView](t, response)
		if revoked.ID != ownedKeyID.String() || revoked.Status != "revoked" {
			t.Fatalf("revocation attempt %d response = %+v", attempt, revoked)
		}
	}

	foreignResponse := request(t, fixture.handler, http.MethodPost, "/api/control/keys/"+foreignKeyID.String()+"/revoke", nil, true, true)
	requireStatus(t, foreignResponse, http.StatusNotFound)
	missingResponse := request(t, fixture.handler, http.MethodPost, "/api/control/keys/"+uuid.NewString()+"/revoke", nil, true, true)
	requireStatus(t, missingResponse, http.StatusNotFound)
	if fixture.identity.keys[fixture.adminID][0].RevokedAt != nil {
		t.Fatal("foreign gateway key was modified through the member control route")
	}
}

func TestSessionCapabilityContract(t *testing.T) {
	tests := []struct {
		name         string
		role         identity.Role
		capabilities []string
	}{
		{name: "administrator", role: identity.RoleAdministrator, capabilities: []string{"providers:read", "providers:write", "credentials:read", "credentials:write", "access:read", "access:write", "ledger:read", "ledger:write", "playground:use", "revisions:publish"}},
		{name: "member", role: identity.RoleMember, capabilities: []string{"access:read", "ledger:read", "playground:use"}},
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
