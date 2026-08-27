package collab

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func integrationStore(t *testing.T) *Store {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("vstd_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	store, err := Open(ctx, u.String(), "owner-login")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	return store
}

func TestCollaborationLifecyclePostgres(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()

	owner, err := s.BootstrapGitHub(ctx, 12345, "OWNER-LOGIN", map[string]string{"existing": "Existing deck"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BootstrapGitHub(ctx, 54321, "owner-login", nil); err == nil {
		t.Fatal("owner GitHub login was rebound to a different numeric identity")
	}
	existing, err := s.DeckByStorage(ctx, "existing")
	if err != nil || existing.OwnerUserID != owner.ID || existing.Visibility != "private" {
		t.Fatalf("owner bootstrap did not claim existing deck: %#v, %v", existing, err)
	}
	ownerRaw, ownerSession, err := s.CreateSession(ctx, owner.ID)
	if err != nil || ownerSession.User.Role != "owner" || ownerSession.CSRF == "" {
		t.Fatalf("owner session: %#v, %v", ownerSession, err)
	}

	inv, inviteToken, err := s.CreateInvitation(ctx, owner.ID, " Member@Example.com ")
	if err != nil || inv.Email != "member@example.com" {
		t.Fatalf("invitation: %#v, %v", inv, err)
	}
	member, err := s.AcceptInvitation(ctx, inviteToken, "Team Member", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptInvitation(ctx, inviteToken, "Replay", "correct horse battery staple"); err == nil {
		t.Fatal("single-use invitation was accepted twice")
	}
	if _, err := s.LoginPassword(ctx, strings.ToUpper(member.Email), "correct horse battery staple"); err != nil {
		t.Fatalf("normalized password login: %v", err)
	}
	memberRaw, _, err := s.CreateSession(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}

	memberDeck, err := s.CreateDeck(ctx, member.ID, "member-private", "Member private", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Can(ctx, owner.ID, memberDeck, "view") || s.Can(ctx, owner.ID, memberDeck, "edit") {
		t.Fatal("owner gained authority over another member's private deck")
	}
	if err := s.SetVisibility(ctx, memberDeck.ID, member.ID, "team"); err != nil {
		t.Fatal(err)
	}
	memberDeck, _ = s.DeckByID(ctx, memberDeck.ID)
	if !s.Can(ctx, owner.ID, memberDeck, "view") || !s.Can(ctx, owner.ID, memberDeck, "present") || s.Can(ctx, owner.ID, memberDeck, "edit") {
		t.Fatal("team-shared permissions do not preserve symmetric ownership")
	}
	if _, err := s.CreateHandoff(ctx, owner.ID, memberDeck.ID, "edit"); err == nil {
		t.Fatal("owner received edit handoff for member deck")
	}

	handoff, err := s.CreateHandoff(ctx, owner.ID, memberDeck.ID, "present")
	if err != nil {
		t.Fatal(err)
	}
	access, player, err := s.ExchangeHandoff(ctx, handoff)
	if err != nil || player.Mode != "present" || player.Deck.ID != memberDeck.ID {
		t.Fatalf("handoff exchange: %#v, %v", player, err)
	}
	if _, _, err := s.ExchangeHandoff(ctx, handoff); err == nil {
		t.Fatal("single-use handoff exchanged twice")
	}
	if err := s.SetVisibility(ctx, memberDeck.ID, member.ID, "private"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlayerSession(ctx, access); err == nil {
		t.Fatal("visibility revocation did not invalidate player session immediately")
	}

	expired, err := s.CreateHandoff(ctx, member.ID, memberDeck.ID, "edit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE vstd_player_handoffs SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, tokenHash(expired)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ExchangeHandoff(ctx, expired); err == nil {
		t.Fatal("expired handoff exchanged")
	}

	if err := s.SetVisibility(ctx, memberDeck.ID, member.ID, "team"); err != nil {
		t.Fatal(err)
	}
	memberAccessHandoff, _ := s.CreateHandoff(ctx, member.ID, memberDeck.ID, "edit")
	memberAccess, _, err := s.ExchangeHandoff(ctx, memberAccessHandoff)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMember(ctx, owner.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, memberRaw); err == nil {
		t.Fatal("member removal did not revoke account session")
	}
	if _, err := s.PlayerSession(ctx, memberAccess); err == nil {
		t.Fatal("member removal did not revoke player session")
	}
	transferred, _ := s.DeckByID(ctx, memberDeck.ID)
	if transferred.OwnerUserID != owner.ID || transferred.Visibility != "team" {
		t.Fatalf("member deck transfer lost owner or visibility: %#v", transferred)
	}
	if _, err := s.Session(ctx, ownerRaw); err != nil {
		t.Fatalf("owner session was affected by member removal: %v", err)
	}
}

func TestPersonalCatalogIsolationAndSharedDeckPlacementPostgres(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	owner, err := s.BootstrapGitHub(ctx, 111, "owner-login", map[string]string{"shared": "Shared"})
	if err != nil {
		t.Fatal(err)
	}
	_, invite, err := s.CreateInvitation(ctx, owner.ID, "member-catalog@example.com")
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.AcceptInvitation(ctx, invite, "Member", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	shared, err := s.DeckByStorage(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetVisibility(ctx, shared.ID, owner.ID, "team"); err != nil {
		t.Fatal(err)
	}
	ownerFolder, err := s.CreateFolder(ctx, owner.ID, "Owner folder")
	if err != nil {
		t.Fatal(err)
	}
	memberFolder, err := s.CreateFolder(ctx, member.ID, "Member folder")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MoveDecksToFolder(ctx, member.ID, []string{shared.ID}, memberFolder.ID); err != nil {
		t.Fatalf("shared deck placement: %v", err)
	}
	ownerCatalog, _ := s.Catalog(ctx, owner.ID)
	memberCatalog, _ := s.Catalog(ctx, member.ID)
	if len(ownerCatalog.Folders) != 1 || ownerCatalog.Folders[0].ID != ownerFolder.ID {
		t.Fatalf("owner folders leaked: %#v", ownerCatalog.Folders)
	}
	if len(memberCatalog.Folders) != 1 || memberCatalog.Folders[0].ID != memberFolder.ID || memberCatalog.Folders[0].Count != 1 {
		t.Fatalf("member catalog wrong: %#v", memberCatalog)
	}
	if err := s.DeleteFolder(ctx, member.ID, memberFolder.ID); err != nil {
		t.Fatal(err)
	}
	memberCatalog, _ = s.Catalog(ctx, member.ID)
	for _, deck := range memberCatalog.Decks {
		if deck.ID == shared.ID && deck.FolderID != "" {
			t.Fatalf("deleted folder did not return shared deck to root: %#v", deck)
		}
	}
}

func TestPasswordResetRevokesAllSessionTypes(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	owner, err := s.BootstrapGitHub(ctx, 99, "owner-login", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.CreateInvitation(ctx, owner.ID, "reset@example.com")
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.AcceptInvitation(ctx, token, "Reset User", "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	account, _, _ := s.CreateSession(ctx, member.ID)
	deck, _ := s.CreateDeck(ctx, member.ID, "reset-deck", "Reset deck", "")
	handoff, _ := s.CreateHandoff(ctx, member.ID, deck.ID, "edit")
	player, _, _ := s.ExchangeHandoff(ctx, handoff)
	reset, _, err := s.CreatePasswordReset(ctx, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPassword(ctx, reset, "a different sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, account); err == nil {
		t.Fatal("password reset left account session active")
	}
	if _, err := s.PlayerSession(ctx, player); err == nil {
		t.Fatal("password reset left player session active")
	}
	if _, err := s.LoginPassword(ctx, member.Email, "a different sufficiently long password"); err != nil {
		t.Fatalf("new password login failed: %v", err)
	}
}
