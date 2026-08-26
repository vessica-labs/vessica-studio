package collab

const schemaSQL = `
CREATE TABLE IF NOT EXISTS vstd_users (
  id text PRIMARY KEY,
  email text UNIQUE,
  display_name text NOT NULL,
  password_hash text,
  github_id bigint UNIQUE,
  github_login text UNIQUE,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS vstd_teams (
  id text PRIMARY KEY,
  name text NOT NULL,
  owner_user_id text REFERENCES vstd_users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS vstd_memberships (
  team_id text NOT NULL REFERENCES vstd_teams(id) ON DELETE CASCADE,
  user_id text NOT NULL REFERENCES vstd_users(id) ON DELETE CASCADE,
  role text NOT NULL CHECK (role IN ('owner','member')),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (team_id,user_id)
);
CREATE TABLE IF NOT EXISTS vstd_decks (
  id text PRIMARY KEY,
  team_id text NOT NULL REFERENCES vstd_teams(id) ON DELETE CASCADE,
  storage_key text NOT NULL UNIQUE,
  owner_user_id text REFERENCES vstd_users(id),
  title text NOT NULL,
  visibility text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','team')),
  forked_from_id text REFERENCES vstd_decks(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS vstd_invitations (
  id text PRIMARY KEY,
  team_id text NOT NULL REFERENCES vstd_teams(id) ON DELETE CASCADE,
  email text NOT NULL,
  token_hash text NOT NULL UNIQUE,
  invited_by text NOT NULL REFERENCES vstd_users(id),
  expires_at timestamptz NOT NULL,
  accepted_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS vstd_open_invite_email ON vstd_invitations(team_id,email)
  WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE TABLE IF NOT EXISTS vstd_sessions (
  id text PRIMARY KEY,
  user_id text NOT NULL REFERENCES vstd_users(id) ON DELETE CASCADE,
  token_hash text NOT NULL UNIQUE,
  csrf_token text NOT NULL,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS vstd_password_resets (
  id text PRIMARY KEY,
  user_id text NOT NULL REFERENCES vstd_users(id) ON DELETE CASCADE,
  token_hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS vstd_player_handoffs (
  id text PRIMARY KEY,
  user_id text NOT NULL REFERENCES vstd_users(id) ON DELETE CASCADE,
  deck_id text NOT NULL REFERENCES vstd_decks(id) ON DELETE CASCADE,
  mode text NOT NULL CHECK (mode IN ('view','present','edit')),
  token_hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS vstd_player_sessions (
  id text PRIMARY KEY,
  user_id text NOT NULL REFERENCES vstd_users(id) ON DELETE CASCADE,
  deck_id text NOT NULL REFERENCES vstd_decks(id) ON DELETE CASCADE,
  mode text NOT NULL CHECK (mode IN ('view','present','edit')),
  token_hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS vstd_audit_events (
  id bigserial PRIMARY KEY,
  team_id text,
  actor_user_id text,
  action text NOT NULL,
  deck_id text,
  target_user_id text,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS vstd_session_token ON vstd_sessions(token_hash);
CREATE INDEX IF NOT EXISTS vstd_player_token ON vstd_player_sessions(token_hash);
CREATE INDEX IF NOT EXISTS vstd_deck_owner ON vstd_decks(owner_user_id);
`
