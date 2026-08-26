package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/collab"
)

type collaborationFixture struct {
	server                      *Server
	handler                     http.Handler
	store                       *collab.Store
	owner, member               collab.User
	ownerSession, memberSession collab.Session
	ownerCookie, memberCookie   string
	ownerDeck, memberDeck       collab.Deck
}

func integrationCollaborationFixture(t *testing.T) collaborationFixture {
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
	schema := fmt.Sprintf("vstd_server_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	store, err := collab.Open(ctx, u.String(), "owner-login")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})

	st := testStudio(t)
	owner, err := store.BootstrapGitHub(ctx, 101, "owner-login", map[string]string{"demo": "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	ownerDeck, _ := store.DeckByStorage(ctx, "demo")
	_, invite, _ := store.CreateInvitation(ctx, owner.ID, "member@example.com")
	member, err := store.AcceptInvitation(ctx, invite, "Member", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.NewDeck("member-deck", "Member deck"); err != nil {
		t.Fatal(err)
	}
	memberDeck, err := store.CreateDeck(ctx, member.ID, "member-deck", "Member deck", "")
	if err != nil {
		t.Fatal(err)
	}
	ownerCookie, ownerSession, _ := store.CreateSession(ctx, owner.ID)
	memberCookie, memberSession, _ := store.CreateSession(ctx, member.ID)

	s := New(st, ModePublic)
	s.Collab = store
	s.appOrigin = "https://app.example.test"
	s.playerOrigin = "https://present.example.test"
	s.St.Config.AppHost = s.appOrigin
	s.St.Config.PublicHost = s.playerOrigin
	s.ContentSync = &ContentSync{ready: true, state: syncStateReady}
	return collaborationFixture{s, s.Routes(), store, owner, member, ownerSession, memberSession,
		ownerCookie, memberCookie, ownerDeck, memberDeck}
}

func appMutation(method, path, body, cookie, csrf, origin string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "app.example.test"
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&http.Cookie{Name: accountSessionCookie, Value: cookie})
	return r
}

func playerBearer(method, path, body, token string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "present.example.test"
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func exchangeMode(t *testing.T, f collaborationFixture, userID, deckID, mode string) string {
	t.Helper()
	handoff, err := f.store.CreateHandoff(context.Background(), userID, deckID, mode)
	if err != nil {
		t.Fatal(err)
	}
	access, _, err := f.store.ExchangeHandoff(context.Background(), handoff)
	if err != nil {
		t.Fatal(err)
	}
	return access
}

func TestCollaborationHandlerAuthorizationMatrixPostgres(t *testing.T) {
	f := integrationCollaborationFixture(t)

	for _, tc := range []struct {
		name, csrf, origin string
	}{
		{"missing csrf", "", f.server.appOrigin},
		{"wrong csrf", "wrong", f.server.appOrigin},
		{"wrong origin", f.ownerSession.CSRF, "https://evil.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			f.handler.ServeHTTP(rr, appMutation(http.MethodPost, "/api/app/decks", `{"title":"Blocked"}`, f.ownerCookie, tc.csrf, tc.origin))
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status=%d, want 403; body=%s", rr.Code, rr.Body.String())
			}
		})
	}

	memberTeam := httptest.NewRequest(http.MethodGet, "/api/app/team", nil)
	memberTeam.Host = "app.example.test"
	memberTeam.AddCookie(&http.Cookie{Name: accountSessionCookie, Value: f.memberCookie})
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, memberTeam)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("member team administration status=%d, want 403", rr.Code)
	}

	rr = httptest.NewRecorder()
	f.handler.ServeHTTP(rr, appMutation(http.MethodPost, "/api/app/decks/"+f.memberDeck.ID+"/launch", `{"mode":"view"}`, f.ownerCookie, f.ownerSession.CSRF, f.server.appOrigin))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("other private deck launch status=%d, want 403", rr.Code)
	}
	if err := f.store.SetVisibility(context.Background(), f.memberDeck.ID, f.member.ID, "team"); err != nil {
		t.Fatal(err)
	}

	rr = httptest.NewRecorder()
	f.handler.ServeHTTP(rr, appMutation(http.MethodPost, "/api/app/decks/"+f.memberDeck.ID+"/launch", `{"mode":"edit"}`, f.ownerCookie, f.ownerSession.CSRF, f.server.appOrigin))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("owner edit of member deck status=%d, want 403", rr.Code)
	}

	view := exchangeMode(t, f, f.owner.ID, f.memberDeck.ID, "view")
	rr = httptest.NewRecorder()
	f.handler.ServeHTTP(rr, playerBearer(http.MethodGet, "/api/vessica/member-deck/tasks", "", view))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("view Vessica status=%d, want 401", rr.Code)
	}

	present := exchangeMode(t, f, f.owner.ID, f.memberDeck.ID, "present")
	rr = httptest.NewRecorder()
	f.handler.ServeHTTP(rr, playerBearer(http.MethodGet, "/api/vessica/member-deck/tasks", "", present))
	if rr.Code != http.StatusOK {
		t.Fatalf("present Vessica status=%d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	f.handler.ServeHTTP(rr, playerBearer(http.MethodPut, "/api/deck/member-deck/slide/0010-title/fragment", `<section class="slide"></section>`, present))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("present edit status=%d, want 403", rr.Code)
	}

	edit := exchangeMode(t, f, f.member.ID, f.memberDeck.ID, "edit")
	rr = httptest.NewRecorder()
	f.handler.ServeHTTP(rr, playerBearer(http.MethodGet, "/api/deck/demo/slide/0010-a", "", edit))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-deck read status=%d, want 403", rr.Code)
	}
	slides, err := f.server.St.SlideIDs("member-deck")
	if err != nil || len(slides) == 0 {
		t.Fatalf("member deck slides=%v, err=%v", slides, err)
	}
	rr = httptest.NewRecorder()
	f.handler.ServeHTTP(rr, playerBearer(http.MethodPost, "/api/deck/member-deck/slide/"+slides[0]+"/attachment?filename=test.csv", "metric,value\ncollaboration,1\n", edit))
	if rr.Code != http.StatusOK {
		t.Fatalf("edit attachment upload status=%d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPlayerHandoffExchangeRequiresPlayerOriginAndSetsMediaCookiePostgres(t *testing.T) {
	f := integrationCollaborationFixture(t)
	handoff, err := f.store.CreateHandoff(context.Background(), f.member.ID, f.memberDeck.ID, "edit")
	if err != nil {
		t.Fatal(err)
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/player/session", strings.NewReader(`{"token":"`+handoff+`"}`))
	bad.Host = "present.example.test"
	bad.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, bad)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("wrong-origin exchange status=%d, want 403", rr.Code)
	}

	good := httptest.NewRequest(http.MethodPost, "/api/player/session", strings.NewReader(`{"token":"`+handoff+`"}`))
	good.Host = "present.example.test"
	good.Header.Set("Origin", f.server.playerOrigin)
	rr = httptest.NewRecorder()
	f.handler.ServeHTTP(rr, good)
	if rr.Code != http.StatusOK {
		t.Fatalf("exchange status=%d; body=%s", rr.Code, rr.Body.String())
	}
	result := rr.Result()
	defer result.Body.Close()
	var media *http.Cookie
	for _, c := range result.Cookies() {
		if c.Name == playerMediaCookie {
			media = c
		}
	}
	if media == nil || !media.HttpOnly || !media.Secure || media.SameSite != http.SameSiteLaxMode {
		t.Fatalf("player media cookie is not constrained: %#v", media)
	}
}
