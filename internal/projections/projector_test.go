package projections

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/shared/contracts/events"
)

type fakeStore struct {
	users    []UserProjection
	orgs     []OrganizationProjection
	userErr  error
	orgErr   error
	skipUser bool
}

func (f *fakeStore) ProjectUser(_ context.Context, eventID string, user UserProjection) (bool, error) {
	if f.userErr != nil {
		return false, f.userErr
	}
	if f.skipUser {
		return false, nil
	}
	f.users = append(f.users, user)
	return true, nil
}

func (f *fakeStore) ProjectOrganization(_ context.Context, eventID string, org OrganizationProjection) error {
	if f.orgErr != nil {
		return f.orgErr
	}
	f.orgs = append(f.orgs, org)
	return nil
}

func envelopeBytes(t *testing.T, typ string, version int, data any) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id":             "11111111-1111-1111-1111-111111111111",
		"type":           typ,
		"version":        version,
		"occurred_at":    "2026-01-01T00:00:00Z",
		"producer":       "test",
		"aggregate":      map[string]any{"type": "test", "id": "22222222-2222-2222-2222-222222222222", "version": 1},
		"correlation_id": "33333333-3333-3333-3333-333333333333",
		"data":           data,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return payload
}

func TestHandleUserRegistered(t *testing.T) {
	store := &fakeStore{}
	projector := New(store, kit.NewTestLogger())

	payload := envelopeBytes(t, "identity.user_registered", 1, events.UserRegistered{
		UserID:           "44444444-4444-4444-4444-444444444444",
		Email:            "ada@example.com",
		Name:             "Ada",
		OrganizationName: "Acme",
	})
	if err := projector.HandleUserRegistered(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(store.users) != 1 || store.users[0].Email != "ada@example.com" {
		t.Fatalf("users = %+v", store.users)
	}
}

func TestHandleUserRegisteredRejectsInvalidEnvelope(t *testing.T) {
	store := &fakeStore{}
	projector := New(store, kit.NewTestLogger())

	if err := projector.HandleUserRegistered(context.Background(), []byte("{")); err == nil {
		t.Fatal("expected decode error")
	}
	// Missing user id.
	payload := envelopeBytes(t, "identity.user_registered", 1, events.UserRegistered{Email: "a@example.com"})
	if err := projector.HandleUserRegistered(context.Background(), payload); err == nil {
		t.Fatal("expected validation error")
	}
	if len(store.users) != 0 {
		t.Fatalf("users = %+v, want none", store.users)
	}
}

func TestHandleUserRegisteredSkipsEmailOwnedByOtherUser(t *testing.T) {
	store := &fakeStore{skipUser: true}
	projector := New(store, kit.NewTestLogger())

	payload := envelopeBytes(t, "identity.user_registered", 1, events.UserRegistered{
		UserID: "44444444-4444-4444-4444-444444444444",
		Email:  "legacy@example.com",
	})
	if err := projector.HandleUserRegistered(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(store.users) != 0 {
		t.Fatalf("users = %+v, want none", store.users)
	}
}

func TestHandleOrganizationCreated(t *testing.T) {
	store := &fakeStore{}
	projector := New(store, kit.NewTestLogger())

	payload := envelopeBytes(t, "workspace.organization_created", 1, events.OrganizationCreated{
		OrganizationID: "55555555-5555-5555-5555-555555555555",
		Name:           "Acme",
		Slug:           "acme",
		OwnerUserID:    "44444444-4444-4444-4444-444444444444",
	})
	if err := projector.HandleOrganizationCreated(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(store.orgs) != 1 || store.orgs[0].Slug != "acme" {
		t.Fatalf("orgs = %+v", store.orgs)
	}
}

func TestHandleOrganizationCreatedRetriesWhenOwnerMissing(t *testing.T) {
	store := &fakeStore{orgErr: ErrOwnerNotProjected}
	projector := New(store, kit.NewTestLogger())

	payload := envelopeBytes(t, "workspace.organization_created", 1, events.OrganizationCreated{
		OrganizationID: "55555555-5555-5555-5555-555555555555",
		Name:           "Acme",
		Slug:           "acme",
		OwnerUserID:    "44444444-4444-4444-4444-444444444444",
	})
	err := projector.HandleOrganizationCreated(context.Background(), payload)
	if !errors.Is(err, ErrOwnerNotProjected) {
		t.Fatalf("err = %v, want ErrOwnerNotProjected", err)
	}
}

func TestHandleOrganizationCreatedRejectsInvalidEnvelope(t *testing.T) {
	store := &fakeStore{}
	projector := New(store, kit.NewTestLogger())

	payload := envelopeBytes(t, "workspace.organization_created", 1, events.OrganizationCreated{
		OrganizationID: "55555555-5555-5555-5555-555555555555",
	})
	if err := projector.HandleOrganizationCreated(context.Background(), payload); err == nil {
		t.Fatal("expected validation error")
	}
	if len(store.orgs) != 0 {
		t.Fatalf("orgs = %+v, want none", store.orgs)
	}
}
