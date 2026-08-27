package collab

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const DefaultTeamID = "default-team"

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

type User struct {
	ID          string `json:"id"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name"`
	GitHubLogin string `json:"github_login,omitempty"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

type Deck struct {
	ID          string `json:"id"`
	StorageKey  string `json:"storage_key"`
	Title       string `json:"title"`
	OwnerUserID string `json:"owner_user_id"`
	OwnerName   string `json:"owner_name"`
	Visibility  string `json:"visibility"`
	ForkedFrom  string `json:"forked_from_id,omitempty"`
	Owned       bool   `json:"owned"`
}

type Session struct {
	User User
	CSRF string
}

type PlayerSession struct {
	User User
	Deck Deck
	Mode string
}

type Invitation struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Team struct {
	Members     []User       `json:"members"`
	Invitations []Invitation `json:"invitations"`
}

type Store struct {
	db         *sql.DB
	ownerLogin string
	teamName   string
	now        func() time.Time
}

func Open(ctx context.Context, databaseURL, ownerLogin string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	s := &Store{db: db, ownerLogin: strings.ToLower(strings.TrimSpace(ownerLogin)), teamName: "Vessica Studio", now: time.Now}
	if s.ownerLogin == "" {
		db.Close()
		return nil, fmt.Errorf("VSTD_OWNER_GITHUB_LOGIN is required")
	}
	if err := s.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(908172635)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS vstd_schema_migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("collaboration migration registry: %w", err)
	}
	for _, migration := range []struct {
		version int
		sql     string
	}{{version: 1, sql: schemaSQL}, {version: 2, sql: catalogSchemaSQL}} {
		var applied bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM vstd_schema_migrations WHERE version=$1)`, migration.version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("collaboration migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO vstd_schema_migrations(version) VALUES($1)`, migration.version); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO vstd_teams(id,name) VALUES($1,$2) ON CONFLICT (id) DO NOTHING`, DefaultTeamID, s.teamName)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func tokenHash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func NormalizeEmail(value string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	p, err := mail.ParseAddress(v)
	if err != nil || p.Address != v || len(v) > 254 {
		return "", fmt.Errorf("invalid email address")
	}
	return v, nil
}

func Slug(title string) string {
	v := strings.ToLower(strings.TrimSpace(title))
	v = strings.Trim(slugRe.ReplaceAllString(v, "-"), "-")
	if len(v) > 54 {
		v = strings.Trim(v[:54], "-")
	}
	if v == "" {
		v = "presentation"
	}
	return v
}

func (s *Store) UniqueStorageKey(ctx context.Context, title string) (string, error) {
	base := Slug(title)
	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i+1)
		}
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM vstd_decks WHERE storage_key=$1)`, candidate).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not allocate a unique presentation slug")
}

func (s *Store) StorageKeyAvailable(ctx context.Context, key string) (bool, error) {
	if key == "" || Slug(key) != key {
		return false, fmt.Errorf("slug must use lowercase letters, numbers, and hyphens")
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM vstd_decks WHERE storage_key=$1)`, key).Scan(&exists); err != nil {
		return false, err
	}
	return !exists, nil
}

func (s *Store) BootstrapGitHub(ctx context.Context, githubID int64, login string, filesystemDecks map[string]string) (User, error) {
	login = strings.ToLower(strings.TrimSpace(login))
	if login != s.ownerLogin {
		return User{}, fmt.Errorf("GitHub user %q is not the configured owner", login)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(908172636)`); err != nil {
		return User{}, err
	}
	var u User
	var boundGitHubID sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT id,COALESCE(email,''),display_name,COALESCE(github_login,''),status,github_id FROM vstd_users WHERE github_id=$1 OR github_login=$2 LIMIT 1`, githubID, login).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.GitHubLogin, &u.Status, &boundGitHubID)
	if errors.Is(err, sql.ErrNoRows) {
		u = User{ID: randomID("usr"), DisplayName: login, GitHubLogin: login, Status: "active", Role: "owner"}
		_, err = tx.ExecContext(ctx, `INSERT INTO vstd_users(id,display_name,github_id,github_login,status) VALUES($1,$2,$3,$4,'active')`, u.ID, u.DisplayName, githubID, login)
	}
	if err != nil {
		return User{}, err
	}
	if boundGitHubID.Valid && boundGitHubID.Int64 != githubID {
		return User{}, fmt.Errorf("configured owner login is already bound to a different GitHub identity")
	}
	u.Role = "owner"
	if _, err := tx.ExecContext(ctx, `UPDATE vstd_users SET github_id=$1,github_login=$2,status='active',updated_at=now() WHERE id=$3`, githubID, login, u.ID); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vstd_teams SET owner_user_id=$1 WHERE id=$2`, u.ID, DefaultTeamID); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO vstd_memberships(team_id,user_id,role,status) VALUES($1,$2,'owner','active') ON CONFLICT(team_id,user_id) DO UPDATE SET role='owner',status='active'`, DefaultTeamID, u.ID); err != nil {
		return User{}, err
	}
	for key, title := range filesystemDecks {
		id := randomID("deck")
		if _, err := tx.ExecContext(ctx, `INSERT INTO vstd_decks(id,team_id,storage_key,owner_user_id,title,visibility) VALUES($1,$2,$3,$4,$5,'private') ON CONFLICT(storage_key) DO UPDATE SET owner_user_id=COALESCE(vstd_decks.owner_user_id,EXCLUDED.owner_user_id),title=EXCLUDED.title`, id, DefaultTeamID, key, u.ID, title); err != nil {
			return User{}, err
		}
	}
	if err := auditTx(ctx, tx, u.ID, "owner.bootstrap", "", u.ID, map[string]any{"github_login": login}); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) ReconcileDecks(ctx context.Context, filesystemDecks map[string]string) error {
	var owner sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT owner_user_id FROM vstd_teams WHERE id=$1`, DefaultTeamID).Scan(&owner); err != nil {
		return err
	}
	for key, title := range filesystemDecks {
		_, err := s.db.ExecContext(ctx, `INSERT INTO vstd_decks(id,team_id,storage_key,owner_user_id,title,visibility) VALUES($1,$2,$3,$4,$5,'private') ON CONFLICT(storage_key) DO UPDATE SET title=EXCLUDED.title`, randomID("deck"), DefaultTeamID, key, nullable(owner), title)
		if err != nil {
			return err
		}
	}
	return nil
}

func nullable(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, userID string) (raw string, session Session, err error) {
	raw, err = randomToken(32)
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return "", Session{}, err
	}
	expires := s.now().Add(30 * 24 * time.Hour)
	if _, err = s.db.ExecContext(ctx, `INSERT INTO vstd_sessions(id,user_id,token_hash,csrf_token,expires_at) VALUES($1,$2,$3,$4,$5)`, randomID("ses"), userID, tokenHash(raw), csrf, expires); err != nil {
		return "", Session{}, err
	}
	session, err = s.Session(ctx, raw)
	return raw, session, err
}

func (s *Store) Session(ctx context.Context, raw string) (Session, error) {
	var out Session
	err := s.db.QueryRowContext(ctx, `SELECT u.id,COALESCE(u.email,''),u.display_name,COALESCE(u.github_login,''),m.role,u.status,s.csrf_token
FROM vstd_sessions s JOIN vstd_users u ON u.id=s.user_id JOIN vstd_memberships m ON m.user_id=u.id AND m.team_id=$1
WHERE s.token_hash=$2 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.status='active' AND m.status='active'`, DefaultTeamID, tokenHash(raw)).
		Scan(&out.User.ID, &out.User.Email, &out.User.DisplayName, &out.User.GitHubLogin, &out.User.Role, &out.User.Status, &out.CSRF)
	if err != nil {
		return Session{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE vstd_sessions SET last_seen_at=now() WHERE token_hash=$1`, tokenHash(raw))
	return out, nil
}

func (s *Store) RevokeSession(ctx context.Context, raw string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE vstd_sessions SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, tokenHash(raw))
	return err
}

func (s *Store) RevokePlayerSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE vstd_player_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID)
	return err
}

func (s *Store) LoginPassword(ctx context.Context, email, password string) (User, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return User{}, fmt.Errorf("invalid email or password")
	}
	var u User
	var hash sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT u.id,u.email,u.display_name,COALESCE(u.github_login,''),m.role,u.status,u.password_hash
FROM vstd_users u JOIN vstd_memberships m ON m.user_id=u.id AND m.team_id=$1 WHERE u.email=$2 AND u.status='active' AND m.status='active'`, DefaultTeamID, email).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.GitHubLogin, &u.Role, &u.Status, &hash)
	if err != nil || !hash.Valid || !CheckPassword(hash.String, password) {
		return User{}, fmt.Errorf("invalid email or password")
	}
	return u, nil
}

func (s *Store) ListDecks(ctx context.Context, userID string) ([]Deck, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,d.storage_key,d.title,COALESCE(d.owner_user_id,''),COALESCE(u.display_name,''),d.visibility,COALESCE(d.forked_from_id,''),(d.owner_user_id=$1)
FROM vstd_decks d LEFT JOIN vstd_users u ON u.id=d.owner_user_id
WHERE d.owner_user_id=$1 OR (d.visibility='team' AND d.owner_user_id IS NOT NULL AND d.owner_user_id<>$1)
ORDER BY (d.owner_user_id=$1) DESC,d.updated_at DESC,d.title`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deck
	for rows.Next() {
		var d Deck
		if err := rows.Scan(&d.ID, &d.StorageKey, &d.Title, &d.OwnerUserID, &d.OwnerName, &d.Visibility, &d.ForkedFrom, &d.Owned); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DeckByID(ctx context.Context, id string) (Deck, error) {
	return s.deck(ctx, `d.id=$1`, id)
}

func (s *Store) DeckByStorage(ctx context.Context, storage string) (Deck, error) {
	return s.deck(ctx, `d.storage_key=$1`, storage)
}

func (s *Store) deck(ctx context.Context, where, arg string) (Deck, error) {
	var d Deck
	err := s.db.QueryRowContext(ctx, `SELECT d.id,d.storage_key,d.title,COALESCE(d.owner_user_id,''),COALESCE(u.display_name,''),d.visibility,COALESCE(d.forked_from_id,'') FROM vstd_decks d LEFT JOIN vstd_users u ON u.id=d.owner_user_id WHERE `+where, arg).
		Scan(&d.ID, &d.StorageKey, &d.Title, &d.OwnerUserID, &d.OwnerName, &d.Visibility, &d.ForkedFrom)
	return d, err
}

func (s *Store) CreateDeck(ctx context.Context, userID, storage, title, forkedFrom string) (Deck, error) {
	id := randomID("deck")
	var fork any
	if forkedFrom != "" {
		fork = forkedFrom
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO vstd_decks(id,team_id,storage_key,owner_user_id,title,visibility,forked_from_id) VALUES($1,$2,$3,$4,$5,'private',$6)`, id, DefaultTeamID, storage, userID, title, fork)
	if err != nil {
		return Deck{}, err
	}
	action := "deck.create"
	if forkedFrom != "" {
		action = "deck.fork"
	}
	_ = s.Audit(ctx, userID, action, id, "", map[string]any{"forked_from": forkedFrom})
	return s.DeckByID(ctx, id)
}

func (s *Store) DeleteDeckRecord(ctx context.Context, id, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM vstd_decks WHERE id=$1 AND owner_user_id=$2`, id, userID)
	return err
}

func (s *Store) SetVisibility(ctx context.Context, id, userID, visibility string) error {
	if visibility != "private" && visibility != "team" {
		return fmt.Errorf("visibility must be private or team")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vstd_decks SET visibility=$1,updated_at=now() WHERE id=$2 AND owner_user_id=$3`, visibility, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return sql.ErrNoRows
	}
	return s.Audit(ctx, userID, "deck.visibility", id, "", map[string]any{"visibility": visibility})
}

func (s *Store) Can(ctx context.Context, userID string, deck Deck, action string) bool {
	if userID == "" {
		return false
	}
	owned := deck.OwnerUserID == userID
	switch action {
	case "view", "present", "fork":
		return owned || deck.Visibility == "team"
	case "edit", "change_visibility", "external_share":
		return owned
	default:
		return false
	}
}

func (s *Store) CanUser(ctx context.Context, userID, action string) bool {
	switch action {
	case "administer_team":
		ok, err := s.IsOwner(ctx, userID)
		return err == nil && ok
	default:
		return false
	}
}

func (s *Store) CreateHandoff(ctx context.Context, userID, deckID, mode string) (string, error) {
	if mode != "view" && mode != "present" && mode != "edit" {
		return "", fmt.Errorf("invalid launch mode")
	}
	d, err := s.DeckByID(ctx, deckID)
	if err != nil {
		return "", err
	}
	action := mode
	if !s.Can(ctx, userID, d, action) {
		return "", sql.ErrNoRows
	}
	raw, err := randomToken(32)
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO vstd_player_handoffs(id,user_id,deck_id,mode,token_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, randomID("hnd"), userID, deckID, mode, tokenHash(raw), s.now().Add(time.Minute))
	if err == nil {
		s.TouchDeck(ctx, userID, deckID)
	}
	return raw, err
}

func (s *Store) ExchangeHandoff(ctx context.Context, raw string) (access string, ps PlayerSession, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", PlayerSession{}, err
	}
	defer tx.Rollback()
	var userID, deckID, mode string
	err = tx.QueryRowContext(ctx, `UPDATE vstd_player_handoffs SET used_at=now() WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now() RETURNING user_id,deck_id,mode`, tokenHash(raw)).Scan(&userID, &deckID, &mode)
	if err != nil {
		return "", PlayerSession{}, err
	}
	access, err = randomToken(32)
	if err != nil {
		return "", PlayerSession{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO vstd_player_sessions(id,user_id,deck_id,mode,token_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, randomID("ply"), userID, deckID, mode, tokenHash(access), s.now().Add(12*time.Hour))
	if err != nil {
		return "", PlayerSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", PlayerSession{}, err
	}
	ps, err = s.PlayerSession(ctx, access)
	return access, ps, err
}

func (s *Store) PlayerSession(ctx context.Context, raw string) (PlayerSession, error) {
	var p PlayerSession
	err := s.db.QueryRowContext(ctx, `SELECT u.id,COALESCE(u.email,''),u.display_name,COALESCE(u.github_login,''),m.role,u.status,
d.id,d.storage_key,d.title,COALESCE(d.owner_user_id,''),COALESCE(o.display_name,''),d.visibility,COALESCE(d.forked_from_id,''),p.mode
FROM vstd_player_sessions p JOIN vstd_users u ON u.id=p.user_id JOIN vstd_memberships m ON m.user_id=u.id AND m.team_id=$1
JOIN vstd_decks d ON d.id=p.deck_id LEFT JOIN vstd_users o ON o.id=d.owner_user_id
WHERE p.token_hash=$2 AND p.revoked_at IS NULL AND p.expires_at>now() AND u.status='active' AND m.status='active'`, DefaultTeamID, tokenHash(raw)).
		Scan(&p.User.ID, &p.User.Email, &p.User.DisplayName, &p.User.GitHubLogin, &p.User.Role, &p.User.Status,
			&p.Deck.ID, &p.Deck.StorageKey, &p.Deck.Title, &p.Deck.OwnerUserID, &p.Deck.OwnerName, &p.Deck.Visibility, &p.Deck.ForkedFrom, &p.Mode)
	if err != nil {
		return PlayerSession{}, err
	}
	if !s.Can(ctx, p.User.ID, p.Deck, p.Mode) {
		return PlayerSession{}, sql.ErrNoRows
	}
	return p, nil
}

func (s *Store) CreateInvitation(ctx context.Context, actorID, email string) (Invitation, string, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return Invitation{}, "", err
	}
	if ok, _ := s.IsOwner(ctx, actorID); !ok {
		return Invitation{}, "", sql.ErrNoRows
	}
	var active bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM vstd_users u JOIN vstd_memberships m ON m.user_id=u.id AND m.team_id=$1 WHERE u.email=$2 AND u.status='active' AND m.status='active')`, DefaultTeamID, email).Scan(&active); err != nil {
		return Invitation{}, "", err
	}
	if active {
		return Invitation{}, "", fmt.Errorf("that email is already an active team member")
	}
	// Expired invitations are terminal even before a cleanup job runs. Marking
	// them revoked frees the one-open-invitation constraint for a fresh invite.
	if _, err := s.db.ExecContext(ctx, `UPDATE vstd_invitations SET revoked_at=now() WHERE team_id=$1 AND email=$2 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at<=now()`, DefaultTeamID, email); err != nil {
		return Invitation{}, "", err
	}
	raw, err := randomToken(32)
	if err != nil {
		return Invitation{}, "", err
	}
	inv := Invitation{ID: randomID("inv"), Email: email, ExpiresAt: s.now().Add(7 * 24 * time.Hour)}
	_, err = s.db.ExecContext(ctx, `INSERT INTO vstd_invitations(id,team_id,email,token_hash,invited_by,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, inv.ID, DefaultTeamID, email, tokenHash(raw), actorID, inv.ExpiresAt)
	if err != nil {
		return Invitation{}, "", err
	}
	_ = s.Audit(ctx, actorID, "invitation.create", "", "", map[string]any{"email": email})
	return inv, raw, nil
}

func (s *Store) AcceptInvitation(ctx context.Context, raw, name, password string) (User, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return User{}, fmt.Errorf("name is required")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var invID, email string
	err = tx.QueryRowContext(ctx, `UPDATE vstd_invitations SET accepted_at=now() WHERE token_hash=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>now() RETURNING id,email`, tokenHash(raw)).Scan(&invID, &email)
	if err != nil {
		return User{}, err
	}
	u := User{Email: email, DisplayName: name, Role: "member", Status: "active"}
	var priorStatus string
	err = tx.QueryRowContext(ctx, `SELECT id,status FROM vstd_users WHERE email=$1 FOR UPDATE`, email).Scan(&u.ID, &priorStatus)
	if errors.Is(err, sql.ErrNoRows) {
		u.ID = randomID("usr")
		_, err = tx.ExecContext(ctx, `INSERT INTO vstd_users(id,email,display_name,password_hash,status) VALUES($1,$2,$3,$4,'active')`, u.ID, email, name, hash)
	} else if err == nil && priorStatus == "inactive" {
		_, err = tx.ExecContext(ctx, `UPDATE vstd_users SET display_name=$1,password_hash=$2,status='active',updated_at=now() WHERE id=$3`, name, hash, u.ID)
	} else if err == nil {
		return User{}, fmt.Errorf("account is already active")
	}
	if err != nil {
		return User{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO vstd_memberships(team_id,user_id,role,status) VALUES($1,$2,'member','active') ON CONFLICT(team_id,user_id) DO UPDATE SET role='member',status='active'`, DefaultTeamID, u.ID)
	if err != nil {
		return User{}, err
	}
	if err := auditTx(ctx, tx, u.ID, "invitation.accept", "", u.ID, map[string]any{"invitation_id": invID}); err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}

func (s *Store) CreatePasswordReset(ctx context.Context, email string) (string, User, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return "", User{}, sql.ErrNoRows
	}
	var u User
	err = s.db.QueryRowContext(ctx, `SELECT id,email,display_name,status FROM vstd_users WHERE email=$1 AND status='active'`, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Status)
	if err != nil {
		return "", User{}, err
	}
	raw, err := randomToken(32)
	if err != nil {
		return "", User{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO vstd_password_resets(id,user_id,token_hash,expires_at) VALUES($1,$2,$3,$4)`, randomID("rst"), u.ID, tokenHash(raw), s.now().Add(time.Hour))
	return raw, u, err
}

func (s *Store) ResetPassword(ctx context.Context, raw, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var userID string
	err = tx.QueryRowContext(ctx, `UPDATE vstd_password_resets SET used_at=now() WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now() RETURNING user_id`, tokenHash(raw)).Scan(&userID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vstd_users SET password_hash=$1,updated_at=now() WHERE id=$2`, hash, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vstd_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vstd_player_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vstd_password_resets SET used_at=COALESCE(used_at,now()) WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if err := auditTx(ctx, tx, userID, "auth.password_reset", "", userID, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) IsOwner(ctx context.Context, userID string) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM vstd_memberships WHERE team_id=$1 AND user_id=$2 AND role='owner' AND status='active')`, DefaultTeamID, userID).Scan(&ok)
	return ok, err
}

func (s *Store) Team(ctx context.Context, actorID string) (Team, error) {
	if ok, err := s.IsOwner(ctx, actorID); err != nil || !ok {
		if err != nil {
			return Team{}, err
		}
		return Team{}, sql.ErrNoRows
	}
	var out Team
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,COALESCE(u.email,''),u.display_name,COALESCE(u.github_login,''),m.role,u.status FROM vstd_memberships m JOIN vstd_users u ON u.id=m.user_id WHERE m.team_id=$1 AND m.status='active' AND u.status='active' ORDER BY m.role DESC,u.display_name`, DefaultTeamID)
	if err != nil {
		return Team{}, err
	}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.GitHubLogin, &u.Role, &u.Status); err != nil {
			rows.Close()
			return Team{}, err
		}
		out.Members = append(out.Members, u)
	}
	rows.Close()
	inv, err := s.db.QueryContext(ctx, `SELECT id,email,expires_at FROM vstd_invitations WHERE team_id=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>now() ORDER BY created_at DESC`, DefaultTeamID)
	if err != nil {
		return Team{}, err
	}
	defer inv.Close()
	for inv.Next() {
		var i Invitation
		if err := inv.Scan(&i.ID, &i.Email, &i.ExpiresAt); err != nil {
			return Team{}, err
		}
		out.Invitations = append(out.Invitations, i)
	}
	return out, inv.Err()
}

func (s *Store) RevokeInvitation(ctx context.Context, actorID, invitationID string) error {
	if ok, _ := s.IsOwner(ctx, actorID); !ok {
		return sql.ErrNoRows
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vstd_invitations SET revoked_at=now() WHERE id=$1 AND team_id=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, invitationID, DefaultTeamID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return sql.ErrNoRows
	}
	return s.Audit(ctx, actorID, "invitation.revoke", "", "", map[string]any{"invitation_id": invitationID})
}

func (s *Store) ResendInvitation(ctx context.Context, actorID, invitationID string) (Invitation, string, error) {
	if ok, _ := s.IsOwner(ctx, actorID); !ok {
		return Invitation{}, "", sql.ErrNoRows
	}
	var email string
	if err := s.db.QueryRowContext(ctx, `SELECT email FROM vstd_invitations WHERE id=$1 AND team_id=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, invitationID, DefaultTeamID).Scan(&email); err != nil {
		return Invitation{}, "", err
	}
	if err := s.RevokeInvitation(ctx, actorID, invitationID); err != nil {
		return Invitation{}, "", err
	}
	return s.CreateInvitation(ctx, actorID, email)
}

func (s *Store) RemoveMember(ctx context.Context, actorID, targetID string) error {
	if ok, _ := s.IsOwner(ctx, actorID); !ok || actorID == targetID {
		return sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE vstd_memberships SET status='inactive' WHERE team_id=$1 AND user_id=$2 AND role='member' AND status='active'`, DefaultTeamID, targetID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	for _, q := range []string{
		`UPDATE vstd_users SET status='inactive',updated_at=now() WHERE id=$1`,
		`UPDATE vstd_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`,
		`UPDATE vstd_player_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`,
		`UPDATE vstd_decks SET owner_user_id=$2,updated_at=now() WHERE owner_user_id=$1`,
	} {
		args := []any{targetID}
		if strings.Contains(q, "$2") {
			args = append(args, actorID)
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	if err := auditTx(ctx, tx, actorID, "member.remove", "", targetID, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Audit(ctx context.Context, actorID, action, deckID, targetID string, metadata map[string]any) error {
	b, _ := json.Marshal(metadata)
	_, err := s.db.ExecContext(ctx, `INSERT INTO vstd_audit_events(team_id,actor_user_id,action,deck_id,target_user_id,metadata) VALUES($1,NULLIF($2,''),$3,NULLIF($4,''),NULLIF($5,''),$6)`, DefaultTeamID, actorID, action, deckID, targetID, b)
	return err
}

func auditTx(ctx context.Context, tx *sql.Tx, actorID, action, deckID, targetID string, metadata map[string]any) error {
	b, _ := json.Marshal(metadata)
	_, err := tx.ExecContext(ctx, `INSERT INTO vstd_audit_events(team_id,actor_user_id,action,deck_id,target_user_id,metadata) VALUES($1,NULLIF($2,''),$3,NULLIF($4,''),NULLIF($5,''),$6)`, DefaultTeamID, actorID, action, deckID, targetID, b)
	return err
}
