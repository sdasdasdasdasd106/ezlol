package api

import (
	"net/http"
	"strings"

	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/ddragon"
	"github.com/aarlint/ezlol/internal/live"
)

// livePlayer is the little the augment assistant needs about each player.
type livePlayer struct {
	Name     string           `json:"name"`
	Champion ddragon.Champion `json:"champion"`
	Team     string           `json:"team"`
	IsDead   bool             `json:"isDead"`
	Level    int              `json:"level"`
	IsMe     bool             `json:"isMe"`
}

// liveResponse is what the in-game augment assistant polls.
type liveResponse struct {
	InGame   bool          `json:"inGame"`
	GameTime float64       `json:"gameTime"`
	MapID    int           `json:"mapId"`
	Mode     string        `json:"mode"` // aram | arena | sr
	Me       *livePlayer   `json:"me,omitempty"`
	Players  []livePlayer  `json:"-"`
	Offer    *augmentOffer `json:"offer,omitempty"`
	OCR      string        `json:"ocr"`     // available | disabled | unavailable | error text
	OCRLang  string        `json:"ocrLang"` // language the OCR helper reads, e.g. zh-Hans-CN
}

// game reports your champion and level in the running game, plus the augment
// offer currently on screen (if any), ranked best first.
func (s *Server) game(w http.ResponseWriter, r *http.Request) {
	gd, err := s.live.Get(r.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	if gd == nil {
		writeJSON(w, 200, liveResponse{InGame: false})
		return
	}
	d := s.dd.Data()
	resp := liveResponse{InGame: true, GameTime: gd.GameData.GameTime, MapID: gd.GameData.MapNumber}
	switch {
	case gd.GameData.MapNumber == 30:
		resp.Mode = "arena"
	case gd.GameData.MapNumber == 12:
		resp.Mode = "aram"
	default:
		resp.Mode = builds.QueueTag(s.watcher.Status().QueueID)
	}
	meName := gd.ActivePlayer.RiotIDName
	if meName == "" {
		meName = gd.ActivePlayer.SummonerName
	}
	for _, p := range gd.AllPlayers {
		lp := livePlayer{Name: firstNonEmpty(p.RiotIDName, p.SummonerName), Team: p.Team, IsDead: p.IsDead, Level: p.Level}
		lp.IsMe = lp.Name == meName
		lp.Champion = resolveChampion(d, p)
		resp.Players = append(resp.Players, lp)
		if lp.IsMe {
			me := lp
			resp.Me = &me
		}
	}
	resp.Offer, resp.OCR = s.detectOffer(r.Context(), &resp)
	resp.OCRLang = s.ocr.Lang()
	writeJSON(w, 200, resp)
}

// resolveChampion maps the live "rawChampionName" (game_character_displayname_KaiSa)
// onto a Data Dragon champion. The raw name is language independent, so this
// works with a Chinese client too.
func resolveChampion(d *ddragon.Data, p live.Player) ddragon.Champion {
	c := ddragon.Champion{Name: p.ChampionName}
	if d == nil {
		return c
	}
	raw := p.RawChampionName
	if i := strings.LastIndex(raw, "_"); i >= 0 {
		raw = raw[i+1:]
	}
	if found, ok := d.ChampionByKey(raw); ok {
		return found
	}
	norm := strings.NewReplacer("'", "", " ", "", ".", "", "&", "").Replace(p.ChampionName)
	if found, ok := d.ChampionByKey(norm); ok {
		return found
	}
	for _, ch := range d.Champions {
		if strings.EqualFold(ch.Name, p.ChampionName) {
			return ch
		}
	}
	return c
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
