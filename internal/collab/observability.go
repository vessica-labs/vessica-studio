package collab

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	EventAudienceQRScan    = "audience.qr_scan"
	EventAudienceDeckView  = "audience.deck_view"
	EventAudienceSlideView = "audience.slide_view"
	EventAudienceChatJoin  = "audience.chat_join"
	EventTeamDeckView      = "team.deck_view"
	EventTeamSlideView     = "team.slide_view"
	EventServerError       = "server.error"
	EventOpenAIUsage       = "openai.usage"
)

// ObservabilityEvent is deliberately small and privacy-preserving. Anonymous
// audience members are represented by a random first-party visitor ID; IP
// addresses, user agents, share tokens, prompts, and response bodies are never
// persisted.
type ObservabilityEvent struct {
	Kind              string
	ActorUserID       string
	VisitorID         string
	VisitorName       string
	DeckID            string
	DeckStorageKey    string
	SlideID           string
	Source            string
	Method            string
	Path              string
	StatusCode        int
	Model             string
	InputTokens       int64
	OutputTokens      int64
	CachedInputTokens int64
	TotalTokens       int64
	DurationMS        int64
	DedupeKey         string
	Metadata          map[string]any
	OccurredAt        time.Time
}

type ObservabilitySummary struct {
	AudienceViewers   int   `json:"audience_viewers"`
	QRScans           int   `json:"qr_scans"`
	PresentationViews int   `json:"presentation_views"`
	SlideViews        int   `json:"slide_views"`
	ActiveTeamMembers int   `json:"active_team_members"`
	ServerErrors      int   `json:"server_errors"`
	OpenAIRequests    int   `json:"openai_requests"`
	OpenAITokens      int64 `json:"openai_tokens"`
}

type ObservabilityDaily struct {
	Date      string `json:"date"`
	Viewers   int    `json:"viewers"`
	Views     int    `json:"views"`
	Errors    int    `json:"errors"`
	Tokens    int64  `json:"tokens"`
	TeamUsers int    `json:"team_users"`
}

type ViewerPresentation struct {
	StorageKey string    `json:"storage_key"`
	Title      string    `json:"title"`
	Views      int       `json:"views"`
	SlideViews int       `json:"slide_views"`
	Slides     []string  `json:"slides"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type ViewerUsage struct {
	ID            string               `json:"id"`
	Name          string               `json:"name,omitempty"`
	FirstSeenAt   time.Time            `json:"first_seen_at"`
	LastSeenAt    time.Time            `json:"last_seen_at"`
	QRScans       int                  `json:"qr_scans"`
	Sources       []string             `json:"sources"`
	Presentations []ViewerPresentation `json:"presentations"`
	SlideViews    int                  `json:"slide_views"`
}

type SlideUsage struct {
	ID      string `json:"id"`
	Views   int    `json:"views"`
	Viewers int    `json:"viewers"`
}

type PresentationUsage struct {
	StorageKey string       `json:"storage_key"`
	Title      string       `json:"title"`
	Viewers    int          `json:"viewers"`
	Views      int          `json:"views"`
	SlideViews int          `json:"slide_views"`
	Slides     []SlideUsage `json:"slides"`
	LastSeenAt time.Time    `json:"last_seen_at"`
}

type TeamUsage struct {
	UserID       string    `json:"user_id"`
	Name         string    `json:"name"`
	Email        string    `json:"email,omitempty"`
	Role         string    `json:"role"`
	Actions      int       `json:"actions"`
	DeckViews    int       `json:"deck_views"`
	SlideViews   int       `json:"slide_views"`
	OpenAICalls  int       `json:"openai_calls"`
	OpenAITokens int64     `json:"openai_tokens"`
	LastActiveAt time.Time `json:"last_active_at"`
}

type ServerErrorUsage struct {
	OccurredAt time.Time `json:"occurred_at"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status_code"`
	ActorName  string    `json:"actor_name,omitempty"`
	Deck       string    `json:"deck,omitempty"`
}

type OpenAIUsage struct {
	Model             string    `json:"model"`
	Source            string    `json:"source"`
	Requests          int       `json:"requests"`
	InputTokens       int64     `json:"input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CachedInputTokens int64     `json:"cached_input_tokens"`
	TotalTokens       int64     `json:"total_tokens"`
	LastUsedAt        time.Time `json:"last_used_at"`
}

type ObservabilityDashboard struct {
	RangeDays     int                  `json:"range_days"`
	GeneratedAt   time.Time            `json:"generated_at"`
	Summary       ObservabilitySummary `json:"summary"`
	Daily         []ObservabilityDaily `json:"daily"`
	Viewers       []ViewerUsage        `json:"viewers"`
	Presentations []PresentationUsage  `json:"presentations"`
	Team          []TeamUsage          `json:"team"`
	Errors        []ServerErrorUsage   `json:"errors"`
	OpenAI        []OpenAIUsage        `json:"openai"`
	DroppedEvents uint64               `json:"dropped_events"`
	Truncated     bool                 `json:"truncated"`
}

func newObservabilityDashboard(days int, generatedAt time.Time, dropped uint64) ObservabilityDashboard {
	return ObservabilityDashboard{
		RangeDays:     days,
		GeneratedAt:   generatedAt,
		Daily:         []ObservabilityDaily{},
		Viewers:       []ViewerUsage{},
		Presentations: []PresentationUsage{},
		Team:          []TeamUsage{},
		Errors:        []ServerErrorUsage{},
		OpenAI:        []OpenAIUsage{},
		DroppedEvents: dropped,
	}
}

// RecordObservability writes a background batch in one statement. The caller
// is the server's bounded recorder, never an audience request goroutine.
func (s *Store) RecordObservability(ctx context.Context, events []ObservabilityEvent) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) > 128 {
		events = events[:128]
	}
	const cols = 21
	values := make([]string, 0, len(events))
	args := make([]any, 0, len(events)*cols)
	for i, event := range events {
		metadata, _ := json.Marshal(event.Metadata)
		when := event.OccurredAt
		if when.IsZero() {
			when = s.now()
		}
		base := i*cols + 1
		placeholders := make([]string, cols)
		for j := range placeholders {
			placeholders[j] = fmt.Sprintf("$%d", base+j)
		}
		values = append(values, "("+strings.Join(placeholders, ",")+")")
		args = append(args, DefaultTeamID, event.Kind, nullString(event.ActorUserID), nullString(event.VisitorID),
			nullString(event.VisitorName), nullString(event.DeckID), nullString(event.DeckStorageKey), nullString(event.SlideID),
			nullString(event.Source), nullString(event.Method), nullString(event.Path), nullInt(event.StatusCode),
			nullString(event.Model), max64(event.InputTokens, 0), max64(event.OutputTokens, 0),
			max64(event.CachedInputTokens, 0), max64(event.TotalTokens, 0), max64(event.DurationMS, 0),
			nullString(event.DedupeKey), metadata, when)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO vstd_observability_events
(team_id,kind,actor_user_id,visitor_id,visitor_name,deck_id,deck_storage_key,slide_id,source,method,path,status_code,model,input_tokens,output_tokens,cached_input_tokens,total_tokens,duration_ms,dedupe_key,metadata,occurred_at) VALUES `+
		strings.Join(values, ",")+` ON CONFLICT (team_id,dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`, args...)
	return err
}

func nullString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

type observabilityRow struct {
	ID                int64
	Kind, ActorUserID string
	VisitorID         string
	VisitorName       string
	DeckStorageKey    string
	SlideID           string
	Source            string
	Method            string
	Path              string
	StatusCode        int
	Model             string
	InputTokens       int64
	OutputTokens      int64
	CachedInputTokens int64
	TotalTokens       int64
	OccurredAt        time.Time
}

type viewerBuild struct {
	usage   ViewerUsage
	sources map[string]bool
	decks   map[string]*viewerPresentationBuild
}

type viewerPresentationBuild struct {
	usage  ViewerPresentation
	slides map[string]bool
}

type presentationBuild struct {
	usage   PresentationUsage
	viewers map[string]bool
	slides  map[string]*slideBuild
}

type slideBuild struct {
	usage   SlideUsage
	viewers map[string]bool
}

type dailyBuild struct {
	usage   ObservabilityDaily
	viewers map[string]bool
	team    map[string]bool
}

func (s *Store) ObservabilityDashboard(ctx context.Context, ownerID string, days int, dropped uint64) (ObservabilityDashboard, error) {
	if ok, err := s.IsOwner(ctx, ownerID); err != nil || !ok {
		if err != nil {
			return ObservabilityDashboard{}, err
		}
		return ObservabilityDashboard{}, sql.ErrNoRows
	}
	if days != 7 && days != 30 && days != 90 {
		days = 30
	}
	out := newObservabilityDashboard(days, s.now().UTC(), dropped)
	cutoff := s.now().Add(-time.Duration(days) * 24 * time.Hour)
	if err := s.db.QueryRowContext(ctx, `SELECT
COUNT(DISTINCT visitor_id) FILTER (WHERE visitor_id IS NOT NULL),
COUNT(*) FILTER (WHERE kind=$3),
COUNT(*) FILTER (WHERE kind=$4),
COUNT(*) FILTER (WHERE kind=$5),
COUNT(*) FILTER (WHERE kind=$6),
COUNT(*) FILTER (WHERE kind=$7),
COALESCE(SUM(total_tokens) FILTER (WHERE kind=$7),0)
FROM vstd_observability_events WHERE team_id=$1 AND occurred_at>=$2`, DefaultTeamID, cutoff,
		EventAudienceQRScan, EventAudienceDeckView, EventAudienceSlideView, EventServerError, EventOpenAIUsage).
		Scan(&out.Summary.AudienceViewers, &out.Summary.QRScans, &out.Summary.PresentationViews,
			&out.Summary.SlideViews, &out.Summary.ServerErrors, &out.Summary.OpenAIRequests, &out.Summary.OpenAITokens); err != nil {
		return out, err
	}

	deckTitles := map[string]string{}
	deckRows, err := s.db.QueryContext(ctx, `SELECT storage_key,title FROM vstd_decks WHERE team_id=$1`, DefaultTeamID)
	if err != nil {
		return out, err
	}
	for deckRows.Next() {
		var key, title string
		if err := deckRows.Scan(&key, &title); err != nil {
			deckRows.Close()
			return out, err
		}
		deckTitles[key] = title
	}
	deckRows.Close()

	users := map[string]*TeamUsage{}
	memberRows, err := s.db.QueryContext(ctx, `SELECT u.id,u.display_name,COALESCE(u.email,''),m.role,COALESCE(MAX(s.last_seen_at),u.created_at)
FROM vstd_memberships m JOIN vstd_users u ON u.id=m.user_id
LEFT JOIN vstd_sessions s ON s.user_id=u.id
WHERE m.team_id=$1 AND m.status='active' AND u.status='active'
GROUP BY u.id,u.display_name,u.email,m.role,u.created_at ORDER BY m.role DESC,u.display_name`, DefaultTeamID)
	if err != nil {
		return out, err
	}
	for memberRows.Next() {
		var usage TeamUsage
		if err := memberRows.Scan(&usage.UserID, &usage.Name, &usage.Email, &usage.Role, &usage.LastActiveAt); err != nil {
			memberRows.Close()
			return out, err
		}
		users[usage.UserID] = &usage
	}
	memberRows.Close()

	const detailLimit = 100000
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,COALESCE(actor_user_id,''),COALESCE(visitor_id,''),COALESCE(visitor_name,''),
COALESCE(deck_storage_key,''),COALESCE(slide_id,''),COALESCE(source,''),COALESCE(method,''),COALESCE(path,''),
COALESCE(status_code,0),COALESCE(model,''),input_tokens,output_tokens,cached_input_tokens,total_tokens,occurred_at
FROM vstd_observability_events WHERE team_id=$1 AND occurred_at>=$2 ORDER BY occurred_at DESC LIMIT $3`, DefaultTeamID, cutoff, detailLimit+1)
	if err != nil {
		return out, err
	}
	var events []observabilityRow
	for rows.Next() {
		var event observabilityRow
		if err := rows.Scan(&event.ID, &event.Kind, &event.ActorUserID, &event.VisitorID, &event.VisitorName,
			&event.DeckStorageKey, &event.SlideID, &event.Source, &event.Method, &event.Path, &event.StatusCode,
			&event.Model, &event.InputTokens, &event.OutputTokens, &event.CachedInputTokens, &event.TotalTokens, &event.OccurredAt); err != nil {
			rows.Close()
			return out, err
		}
		events = append(events, event)
	}
	rows.Close()
	if len(events) > detailLimit {
		out.Truncated = true
		events = events[:detailLimit]
	}

	viewers := map[string]*viewerBuild{}
	presentations := map[string]*presentationBuild{}
	daily := map[string]*dailyBuild{}
	openAI := map[string]*OpenAIUsage{}
	activeUsers := map[string]bool{}

	for _, event := range events {
		date := event.OccurredAt.UTC().Format("2006-01-02")
		d := daily[date]
		if d == nil {
			d = &dailyBuild{usage: ObservabilityDaily{Date: date}, viewers: map[string]bool{}, team: map[string]bool{}}
			daily[date] = d
		}
		if event.ActorUserID != "" {
			activeUsers[event.ActorUserID] = true
			d.team[event.ActorUserID] = true
			if u := users[event.ActorUserID]; u != nil {
				if event.OccurredAt.After(u.LastActiveAt) {
					u.LastActiveAt = event.OccurredAt
				}
				switch event.Kind {
				case EventTeamDeckView:
					u.DeckViews++
				case EventTeamSlideView:
					u.SlideViews++
				case EventOpenAIUsage:
					u.OpenAICalls++
					u.OpenAITokens += event.TotalTokens
				}
			}
		}
		if event.VisitorID != "" {
			d.viewers[event.VisitorID] = true
			v := viewers[event.VisitorID]
			if v == nil {
				v = &viewerBuild{usage: ViewerUsage{ID: event.VisitorID, FirstSeenAt: event.OccurredAt, LastSeenAt: event.OccurredAt}, sources: map[string]bool{}, decks: map[string]*viewerPresentationBuild{}}
				viewers[event.VisitorID] = v
			}
			if event.OccurredAt.Before(v.usage.FirstSeenAt) {
				v.usage.FirstSeenAt = event.OccurredAt
			}
			if event.OccurredAt.After(v.usage.LastSeenAt) {
				v.usage.LastSeenAt = event.OccurredAt
			}
			if event.VisitorName != "" {
				v.usage.Name = event.VisitorName
			}
			if event.Source != "" {
				v.sources[event.Source] = true
			}
			if event.Kind == EventAudienceQRScan {
				v.usage.QRScans++
			}
			if event.DeckStorageKey != "" && (event.Kind == EventAudienceDeckView || event.Kind == EventAudienceSlideView) {
				vp := v.decks[event.DeckStorageKey]
				if vp == nil {
					vp = &viewerPresentationBuild{usage: ViewerPresentation{StorageKey: event.DeckStorageKey, Title: titleOrKey(deckTitles, event.DeckStorageKey)}, slides: map[string]bool{}}
					v.decks[event.DeckStorageKey] = vp
				}
				if event.Kind == EventAudienceDeckView {
					vp.usage.Views++
				}
				if event.Kind == EventAudienceSlideView {
					vp.usage.SlideViews++
					v.usage.SlideViews++
					if event.SlideID != "" {
						vp.slides[event.SlideID] = true
					}
				}
				if event.OccurredAt.After(vp.usage.LastSeenAt) {
					vp.usage.LastSeenAt = event.OccurredAt
				}
			}
		}

		switch event.Kind {
		case EventAudienceDeckView:
			d.usage.Views++
		case EventAudienceSlideView:
			d.usage.Views++
		case EventServerError:
			d.usage.Errors++
			if len(out.Errors) < 100 {
				actor := ""
				if u := users[event.ActorUserID]; u != nil {
					actor = u.Name
				}
				out.Errors = append(out.Errors, ServerErrorUsage{OccurredAt: event.OccurredAt, Method: event.Method, Path: event.Path, StatusCode: event.StatusCode, ActorName: actor, Deck: event.DeckStorageKey})
			}
		case EventOpenAIUsage:
			d.usage.Tokens += event.TotalTokens
			key := event.Model + "\x00" + event.Source
			u := openAI[key]
			if u == nil {
				u = &OpenAIUsage{Model: event.Model, Source: event.Source}
				openAI[key] = u
			}
			u.Requests++
			u.InputTokens += event.InputTokens
			u.OutputTokens += event.OutputTokens
			u.CachedInputTokens += event.CachedInputTokens
			u.TotalTokens += event.TotalTokens
			if event.OccurredAt.After(u.LastUsedAt) {
				u.LastUsedAt = event.OccurredAt
			}
		}

		if event.VisitorID != "" && event.DeckStorageKey != "" && (event.Kind == EventAudienceDeckView || event.Kind == EventAudienceSlideView) {
			p := presentations[event.DeckStorageKey]
			if p == nil {
				p = &presentationBuild{usage: PresentationUsage{StorageKey: event.DeckStorageKey, Title: titleOrKey(deckTitles, event.DeckStorageKey)}, viewers: map[string]bool{}, slides: map[string]*slideBuild{}}
				presentations[event.DeckStorageKey] = p
			}
			p.viewers[event.VisitorID] = true
			if event.Kind == EventAudienceDeckView {
				p.usage.Views++
			}
			if event.Kind == EventAudienceSlideView {
				p.usage.SlideViews++
				if event.SlideID != "" {
					sl := p.slides[event.SlideID]
					if sl == nil {
						sl = &slideBuild{usage: SlideUsage{ID: event.SlideID}, viewers: map[string]bool{}}
						p.slides[event.SlideID] = sl
					}
					sl.usage.Views++
					sl.viewers[event.VisitorID] = true
				}
			}
			if event.OccurredAt.After(p.usage.LastSeenAt) {
				p.usage.LastSeenAt = event.OccurredAt
			}
		}
	}

	for _, d := range daily {
		d.usage.Viewers = len(d.viewers)
		d.usage.TeamUsers = len(d.team)
		out.Daily = append(out.Daily, d.usage)
	}
	sort.Slice(out.Daily, func(i, j int) bool { return out.Daily[i].Date < out.Daily[j].Date })

	for _, v := range viewers {
		for source := range v.sources {
			v.usage.Sources = append(v.usage.Sources, source)
		}
		sort.Strings(v.usage.Sources)
		for _, deck := range v.decks {
			for slide := range deck.slides {
				deck.usage.Slides = append(deck.usage.Slides, slide)
			}
			sort.Strings(deck.usage.Slides)
			v.usage.Presentations = append(v.usage.Presentations, deck.usage)
		}
		sort.Slice(v.usage.Presentations, func(i, j int) bool {
			return v.usage.Presentations[i].LastSeenAt.After(v.usage.Presentations[j].LastSeenAt)
		})
		out.Viewers = append(out.Viewers, v.usage)
	}
	sort.Slice(out.Viewers, func(i, j int) bool { return out.Viewers[i].LastSeenAt.After(out.Viewers[j].LastSeenAt) })

	for _, p := range presentations {
		p.usage.Viewers = len(p.viewers)
		for _, slide := range p.slides {
			slide.usage.Viewers = len(slide.viewers)
			p.usage.Slides = append(p.usage.Slides, slide.usage)
		}
		sort.Slice(p.usage.Slides, func(i, j int) bool { return p.usage.Slides[i].Views > p.usage.Slides[j].Views })
		out.Presentations = append(out.Presentations, p.usage)
	}
	sort.Slice(out.Presentations, func(i, j int) bool { return out.Presentations[i].LastSeenAt.After(out.Presentations[j].LastSeenAt) })

	auditRows, err := s.db.QueryContext(ctx, `SELECT actor_user_id,COUNT(*),MAX(created_at) FROM vstd_audit_events WHERE team_id=$1 AND actor_user_id IS NOT NULL AND created_at>=$2 GROUP BY actor_user_id`, DefaultTeamID, cutoff)
	if err != nil {
		return out, err
	}
	for auditRows.Next() {
		var id string
		var count int
		var last time.Time
		if err := auditRows.Scan(&id, &count, &last); err != nil {
			auditRows.Close()
			return out, err
		}
		if u := users[id]; u != nil {
			activeUsers[id] = true
			u.Actions = count
			if last.After(u.LastActiveAt) {
				u.LastActiveAt = last
			}
		}
	}
	auditRows.Close()
	out.Summary.ActiveTeamMembers = len(activeUsers)
	for _, usage := range users {
		out.Team = append(out.Team, *usage)
	}
	sort.Slice(out.Team, func(i, j int) bool {
		if out.Team[i].Role != out.Team[j].Role {
			return out.Team[i].Role == "owner"
		}
		return out.Team[i].LastActiveAt.After(out.Team[j].LastActiveAt)
	})
	for _, usage := range openAI {
		out.OpenAI = append(out.OpenAI, *usage)
	}
	sort.Slice(out.OpenAI, func(i, j int) bool { return out.OpenAI[i].TotalTokens > out.OpenAI[j].TotalTokens })
	return out, nil
}

func titleOrKey(titles map[string]string, key string) string {
	if title := strings.TrimSpace(titles[key]); title != "" {
		return title
	}
	return key
}
