// Package lcu talks to the local League Client (LCU) REST API.
//
// The client exposes an HTTPS server on 127.0.0.1 with a per-launch port and
// password. Both are discoverable from the lockfile in the install directory or
// from the LeagueClientUx process arguments.
package lcu

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Creds is what we need to reach the client.
type Creds struct {
	Port     int
	Password string
}

// ErrNotRunning is returned when no client can be found.
var ErrNotRunning = errors.New("league client not running")

var defaultLockfiles = []string{
	"/Applications/League of Legends.app/Contents/LoL/lockfile",
	"C:\\Riot Games\\League of Legends\\lockfile",
	"D:\\Riot Games\\League of Legends\\lockfile",
	"C:\\Program Files\\Riot Games\\League of Legends\\lockfile",
}

// Discover finds the running client's port and password.
func Discover() (Creds, error) {
	paths := []string{}
	if p := os.Getenv("EZLOL_LOCKFILE"); p != "" {
		paths = append(paths, p)
	}
	paths = append(paths, defaultLockfiles...)
	for _, p := range paths {
		if c, err := readLockfile(p); err == nil {
			return c, nil
		}
	}
	// Reading process command lines spawns wmic / PowerShell on Windows: do it
	// at most every 10 seconds, not on every one-second poll.
	procMu.Lock()
	due := time.Since(procLast) >= 10*time.Second
	if due {
		procLast = time.Now()
	}
	procMu.Unlock()
	if due {
		if c, err := fromProcess(); err == nil {
			return c, nil
		}
	}
	return Creds{}, ErrNotRunning
}

var (
	procMu   sync.Mutex
	procLast time.Time
)

// readLockfile parses "name:pid:port:password:protocol".
func readLockfile(path string) (Creds, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Creds{}, err
	}
	parts := strings.Split(strings.TrimSpace(string(b)), ":")
	if len(parts) < 5 {
		return Creds{}, fmt.Errorf("lockfile %s: unexpected format", path)
	}
	port, err := strconv.Atoi(parts[2])
	if err != nil {
		return Creds{}, fmt.Errorf("lockfile %s: bad port: %w", path, err)
	}
	return Creds{Port: port, Password: parts[3]}, nil
}

var (
	rePort  = regexp.MustCompile(`--app-port=(\d+)`)
	reToken = regexp.MustCompile(`--remoting-auth-token=([^\s"]+)`)
)

// parseProcessList extracts credentials from process command lines.
func parseProcessList(out string) (Creds, error) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "LeagueClientUx") {
			continue
		}
		pm := rePort.FindStringSubmatch(line)
		tm := reToken.FindStringSubmatch(line)
		if pm == nil || tm == nil {
			continue
		}
		port, _ := strconv.Atoi(pm[1])
		return Creds{Port: port, Password: tm[1]}, nil
	}
	return Creds{}, ErrNotRunning
}

// Client is an authenticated LCU HTTP client.
type Client struct {
	creds Creds
	http  *http.Client
}

// New builds a client. The LCU presents a self-signed certificate issued by
// Riot's local CA, so verification is disabled; the connection is loopback only.
func New(c Creds) *Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback self-signed cert
		DialContext:     (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
	}
	return &Client{creds: c, http: &http.Client{Transport: tr, Timeout: 5 * time.Second}}
}

// APIError carries the LCU's error payload.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("lcu: %d %s", e.Status, e.Message) }

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("https://127.0.0.1:%d%s", c.creds.Port, path), rdr)
	if err != nil {
		return err
	}
	req.SetBasicAuth("riot", c.creds.Password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 400 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		return &APIError{Status: res.StatusCode, Message: e.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Get performs a GET and decodes JSON into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// Post performs a POST.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

// GameflowPhase returns e.g. "None", "Lobby", "Matchmaking", "ReadyCheck",
// "ChampSelect", "InProgress", "EndOfGame".
func (c *Client) GameflowPhase(ctx context.Context) (string, error) {
	var s string
	err := c.Get(ctx, "/lol-gameflow/v1/gameflow-phase", &s)
	return s, err
}

// ReadyCheck mirrors /lol-matchmaking/v1/ready-check.
type ReadyCheck struct {
	State          string  `json:"state"`          // Invalid, InProgress, EveryoneReady, ...
	PlayerResponse string  `json:"playerResponse"` // None, Accepted, Declined
	Timer          float64 `json:"timer"`
}

// ReadyCheckState returns the current ready check, or nil when not in one.
func (c *Client) ReadyCheckState(ctx context.Context) (*ReadyCheck, error) {
	var rc ReadyCheck
	err := c.Get(ctx, "/lol-matchmaking/v1/ready-check", &rc)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rc, nil
}

// AcceptReadyCheck accepts the queue pop.
func (c *Client) AcceptReadyCheck(ctx context.Context) error {
	return c.Post(ctx, "/lol-matchmaking/v1/ready-check/accept", nil, nil)
}

// Summoner is the logged-in account.
type Summoner struct {
	GameName      string `json:"gameName"`
	TagLine       string `json:"tagLine"`
	SummonerID    int64  `json:"summonerId"`
	PUUID         string `json:"puuid"`
	ProfileIconID int    `json:"profileIconId"`
	SummonerLevel int    `json:"summonerLevel"`
}

// CurrentSummoner returns the logged-in summoner.
func (c *Client) CurrentSummoner(ctx context.Context) (*Summoner, error) {
	var s Summoner
	if err := c.Get(ctx, "/lol-summoner/v1/current-summoner", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GameVersion returns the full client version string, e.g. "16.17.8104348+...".
func (c *Client) GameVersion(ctx context.Context) (string, error) {
	var s string
	err := c.Get(ctx, "/lol-patch/v1/game-version", &s)
	return s, err
}

// ChampSelectPick returns the champion id the local player has picked or hovered
// in champ select, 0 when not in champ select.
func (c *Client) ChampSelectPick(ctx context.Context) (int, string, error) {
	sess, err := c.ChampSelectSession(ctx)
	if err != nil || sess == nil {
		return 0, "", err
	}
	id, pos := sess.Pick()
	return id, pos, nil
}

// RecommendedPage is one of Riot's in-client recommended rune pages.
type RecommendedPage struct {
	Position             string `json:"position"`
	IsDefaultPosition    bool   `json:"isDefaultPosition"`
	PrimaryPerkStyleID   int    `json:"primaryPerkStyleId"`
	SecondaryPerkStyleID int    `json:"secondaryPerkStyleId"`
	Perks                []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"perks"`
	SummonerSpellIDs []int `json:"summonerSpellIds"`
}

// RecommendedRunes returns Riot's recommended pages for a champion/position on a
// map (11 = Summoner's Rift, 12 = Howling Abyss). Position is TOP, JUNGLE,
// MIDDLE, BOTTOM, UTILITY or NONE.
func (c *Client) RecommendedRunes(ctx context.Context, championID int, position string, mapID int) ([]RecommendedPage, error) {
	var pages []RecommendedPage
	path := fmt.Sprintf("/lol-perks/v1/recommended-pages/champion/%d/position/%s/map/%d", championID, position, mapID)
	if err := c.Get(ctx, path, &pages); err != nil {
		return nil, err
	}
	return pages, nil
}

// GameflowSession is the trimmed /lol-gameflow/v1/session.
type GameflowSession struct {
	Phase string `json:"phase"`
	Map   struct {
		ID       int    `json:"id"`
		GameMode string `json:"gameMode"`
		Name     string `json:"name"`
	} `json:"map"`
	GameData struct {
		Queue struct {
			ID          int    `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"queue"`
	} `json:"gameData"`
}

// Session returns the current gameflow session, or nil when idle.
func (c *Client) Session(ctx context.Context) (*GameflowSession, error) {
	var s GameflowSession
	err := c.Get(ctx, "/lol-gameflow/v1/session", &s)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ChampSelect is the trimmed /lol-champ-select/v1/session.
type ChampSelect struct {
	LocalPlayerCellID int  `json:"localPlayerCellId"`
	BenchEnabled      bool `json:"benchEnabled"`
	// RerollsRemaining is nil when the session does not carry it.
	RerollsRemaining *int `json:"rerollsRemaining"`
	BenchChampions   []struct {
		ChampionID int `json:"championId"`
	} `json:"benchChampions"`
	MyTeam []struct {
		CellID             int    `json:"cellId"`
		ChampionID         int    `json:"championId"`
		ChampionPickIntent int    `json:"championPickIntent"`
		AssignedPosition   string `json:"assignedPosition"`
		Spell1ID           int    `json:"spell1Id"`
		Spell2ID           int    `json:"spell2Id"`
		SummonerID         int64  `json:"summonerId"`
		GameName           string `json:"gameName"`
	} `json:"myTeam"`
	TheirTeam []struct {
		CellID     int `json:"cellId"`
		ChampionID int `json:"championId"`
	} `json:"theirTeam"`
	Timer struct {
		AdjustedTimeLeftInPhase int    `json:"adjustedTimeLeftInPhase"`
		Phase                   string `json:"phase"`
	} `json:"timer"`
	Trades []Trade `json:"trades"`
}

// Pick returns the local player's champion (the pick, else the hovered intent)
// and assigned position; 0 when the player has none yet.
func (s *ChampSelect) Pick() (int, string) {
	for _, m := range s.MyTeam {
		if m.CellID == s.LocalPlayerCellID {
			id := m.ChampionID
			if id == 0 {
				id = m.ChampionPickIntent
			}
			return id, strings.ToUpper(m.AssignedPosition)
		}
	}
	return 0, ""
}

// BenchIDs lists the champions on the ARAM bench, in client order.
func (s *ChampSelect) BenchIDs() []int {
	out := make([]int, 0, len(s.BenchChampions))
	for _, b := range s.BenchChampions {
		if b.ChampionID != 0 {
			out = append(out, b.ChampionID)
		}
	}
	return out
}

// ChampSelectSession returns the champ select session, or nil when not in one.
func (c *Client) ChampSelectSession(ctx context.Context) (*ChampSelect, error) {
	var s ChampSelect
	err := c.Get(ctx, "/lol-champ-select/v1/session", &s)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SwapWithBench swaps your champion with one on the ARAM bench.
func (c *Client) SwapWithBench(ctx context.Context, championID int) error {
	return c.Post(ctx, fmt.Sprintf("/lol-champ-select/v1/session/bench/swap/%d", championID), nil, nil)
}

// Reroll spends a reroll in ARAM champ select.
func (c *Client) Reroll(ctx context.Context) error {
	return c.Post(ctx, "/lol-champ-select/v1/session/my-selection/reroll", nil, nil)
}

// Mastery is one champion mastery row.
type Mastery struct {
	ChampionID     int    `json:"championId"`
	ChampionLevel  int    `json:"championLevel"`
	ChampionPoints int    `json:"championPoints"`
	HighestGrade   string `json:"highestGrade"`
}

// ChampionMastery returns the logged-in player's mastery list.
func (c *Client) ChampionMastery(ctx context.Context) ([]Mastery, error) {
	var out []Mastery
	err := c.Get(ctx, "/lol-champion-mastery/v1/local-player/champion-mastery", &out)
	return out, err
}

// HistoryGame is a trimmed match-history row for the local player.
type HistoryGame struct {
	GameID       int64 `json:"gameId"`
	QueueID      int   `json:"queueId"`
	GameCreation int64 `json:"gameCreation"`
	GameDuration int   `json:"gameDuration"`
	Participants []struct {
		ChampionID int `json:"championId"`
		Stats      struct {
			Win     bool `json:"win"`
			Kills   int  `json:"kills"`
			Deaths  int  `json:"deaths"`
			Assists int  `json:"assists"`
		} `json:"stats"`
	} `json:"participants"`
}

// MatchHistory returns the local player's recent games (newest first). The
// client only includes the local player's participant in each row.
func (c *Client) MatchHistory(ctx context.Context, puuid string, count int) ([]HistoryGame, error) {
	var res struct {
		Games struct {
			Games []HistoryGame `json:"games"`
		} `json:"games"`
	}
	path := fmt.Sprintf("/lol-match-history/v1/products/lol/%s/matches?begIndex=0&endIndex=%d", puuid, count)
	if err := c.Get(ctx, path, &res); err != nil {
		return nil, err
	}
	return res.Games.Games, nil
}

// ChampionInfo is Riot's tactical/playstyle metadata from the client's game data.
type ChampionInfo struct {
	ID           int      `json:"id"`
	Roles        []string `json:"roles"`
	TacticalInfo struct {
		Style      int    `json:"style"`      // 0 = full auto-attacker .. 10 = full caster
		Difficulty int    `json:"difficulty"` // 1..3
		DamageType string `json:"damageType"` // kMagic | kPhysical | kMixed
		AttackType string `json:"attackType"` // melee | ranged
	} `json:"tacticalInfo"`
	PlaystyleInfo struct {
		Damage       int `json:"damage"`
		Durability   int `json:"durability"`
		CrowdControl int `json:"crowdControl"`
		Mobility     int `json:"mobility"`
		Utility      int `json:"utility"`
	} `json:"playstyleInfo"`
}

// ChampionInfo reads /lol-game-data/assets/v1/champions/{id}.json.
func (c *Client) ChampionInfo(ctx context.Context, id int) (*ChampionInfo, error) {
	var ci ChampionInfo
	if err := c.Get(ctx, fmt.Sprintf("/lol-game-data/assets/v1/champions/%d.json", id), &ci); err != nil {
		return nil, err
	}
	return &ci, nil
}

// EOGStats is the trimmed post-game stats block.
type EOGStats struct {
	GameLength  int       `json:"gameLength"`
	GameMode    string    `json:"gameMode"`
	QueueID     int       `json:"queueId"`
	LocalPlayer EOGPlayer `json:"localPlayer"`
	Teams       []EOGTeam `json:"teams"`
}

// EOGTeam is one side of the scoreboard.
type EOGTeam struct {
	IsWinningTeam bool        `json:"isWinningTeam"`
	TeamID        int         `json:"teamId"`
	Players       []EOGPlayer `json:"players"`
}

// EOGPlayer is one scoreboard row; Stats keys are Riot's upper-snake names
// (CHAMPIONS_KILLED, NUM_DEATHS, ASSISTS, TOTAL_DAMAGE_DEALT_TO_CHAMPIONS, GOLD_EARNED, ...).
type EOGPlayer struct {
	ChampionID    int            `json:"championId"`
	GameName      string         `json:"gameName"`
	SummonerName  string         `json:"summonerName"`
	IsLocalPlayer bool           `json:"isLocalPlayer"`
	TeamID        int            `json:"teamId"`
	Stats         map[string]any `json:"stats"`
	Spell1ID      int            `json:"spell1Id"`
	Spell2ID      int            `json:"spell2Id"`
	Items         []int          `json:"items"`
}

// EndOfGameStats returns the post-game block, or nil when none is available.
func (c *Client) EndOfGameStats(ctx context.Context) (*EOGStats, error) {
	var s EOGStats
	err := c.Get(ctx, "/lol-end-of-game/v1/eog-stats-block", &s)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Trade is a champ-select trade slot with a teammate.
type Trade struct {
	ID     int    `json:"id"`
	CellID int    `json:"cellId"`
	State  string `json:"state"` // AVAILABLE, BUSY, INVALID, RECEIVED, SENT, DECLINED, CANCELLED
}

// Trades lists possible champion swaps in champ select. Current clients expose
// them as /session/champion-swaps (the session object also carries them as "trades").
func (c *Client) Trades(ctx context.Context) ([]Trade, error) {
	var out []Trade
	err := c.Get(ctx, "/lol-champ-select/v1/session/champion-swaps", &out)
	return out, err
}

// RequestTrade asks a teammate to swap champions.
func (c *Client) RequestTrade(ctx context.Context, tradeID int) error {
	return c.Post(ctx, fmt.Sprintf("/lol-champ-select/v1/session/champion-swaps/%d/request", tradeID), nil, nil)
}

// AcceptTrade accepts an incoming trade.
func (c *Client) AcceptTrade(ctx context.Context, tradeID int) error {
	return c.Post(ctx, fmt.Sprintf("/lol-champ-select/v1/session/champion-swaps/%d/accept", tradeID), nil, nil)
}

// RunePage is a client rune page (subset).
type RunePage struct {
	ID              int    `json:"id,omitempty"`
	Name            string `json:"name"`
	PrimaryStyleID  int    `json:"primaryStyleId"`
	SubStyleID      int    `json:"subStyleId"`
	SelectedPerkIDs []int  `json:"selectedPerkIds"`
	Current         bool   `json:"current"`
	IsEditable      bool   `json:"isEditable,omitempty"`
	IsDeletable     bool   `json:"isDeletable,omitempty"`
}

// RunePages lists the player's pages.
func (c *Client) RunePages(ctx context.Context) ([]RunePage, error) {
	var out []RunePage
	err := c.Get(ctx, "/lol-perks/v1/pages", &out)
	return out, err
}

// DeleteRunePage removes a page.
func (c *Client) DeleteRunePage(ctx context.Context, id int) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/lol-perks/v1/pages/%d", id), nil, nil)
}

// CreateRunePage adds a page and makes it current.
func (c *Client) CreateRunePage(ctx context.Context, p RunePage) (*RunePage, error) {
	var out RunePage
	p.Current = true
	if err := c.Post(ctx, "/lol-perks/v1/pages", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApplyRunePage replaces any page whose name starts with prefix (pages ezlol
// created earlier), then creates the given page as the current one. It never
// deletes pages the player made.
func (c *Client) ApplyRunePage(ctx context.Context, prefix string, p RunePage) (*RunePage, error) {
	pages, err := c.RunePages(ctx)
	if err != nil {
		return nil, err
	}
	for _, pg := range pages {
		if pg.IsDeletable && strings.HasPrefix(pg.Name, prefix) {
			_ = c.DeleteRunePage(ctx, pg.ID)
		}
	}
	out, err := c.CreateRunePage(ctx, p)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
		// At the page cap. Never delete the player's own pages; ask them instead.
		return nil, fmt.Errorf("rune page limit reached: delete a page in the client and try again")
	}
	return out, err
}

// GameflowPlayer is one entry of the gameflow session's team list. In Arena the
// teamParticipantId is the sub-team (1..N) the player fights for.
type GameflowPlayer struct {
	ChampionID int    `json:"championId"`
	PUUID      string `json:"puuid"`
	SummonerID int64  `json:"summonerId"`
	Skin       int    `json:"lastSelectedSkinIndex"`
	Team       int    `json:"teamParticipantId"`
}

// GameflowPlayers returns every player in the current game with their sub-team.
func (c *Client) GameflowPlayers(ctx context.Context) ([]GameflowPlayer, error) {
	var s struct {
		GameData struct {
			GameID  int64            `json:"gameId"`
			TeamOne []GameflowPlayer `json:"teamOne"`
			TeamTwo []GameflowPlayer `json:"teamTwo"`
		} `json:"gameData"`
	}
	if err := c.Get(ctx, "/lol-gameflow/v1/session", &s); err != nil {
		return nil, err
	}
	out := append([]GameflowPlayer{}, s.GameData.TeamOne...)
	seen := map[string]bool{}
	for _, p := range out {
		seen[p.PUUID] = true
	}
	for _, p := range s.GameData.TeamTwo {
		if !seen[p.PUUID] {
			out = append(out, p)
		}
	}
	return out, nil
}

// SummonerName resolves a puuid to "gameName" via the client.
func (c *Client) SummonerName(ctx context.Context, puuid string) (string, error) {
	var s struct {
		GameName string `json:"gameName"`
	}
	if err := c.Get(ctx, "/lol-summoner/v2/summoners/puuid/"+puuid, &s); err != nil {
		return "", err
	}
	return s.GameName, nil
}
