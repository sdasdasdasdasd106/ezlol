package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/lcu"
)

// applyRunes writes a rune page from the build view into the League client.
// Body: {"championId": 103, "page": <builds.RunePage>}.
func (s *Server) applyRunes(w http.ResponseWriter, r *http.Request) {
	c := s.watcher.Client()
	if c == nil {
		writeErr(w, 503, "英雄联盟客户端没开")
		return
	}
	var body struct {
		ChampionID int             `json:"championId"`
		Page       builds.RunePage `json:"page"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "请求格式不对")
		return
	}
	if len(body.Page.Perks) != 6 || len(body.Page.Shards) != 3 || body.Page.Primary.ID == 0 || body.Page.Secondary.ID == 0 {
		writeErr(w, 400, "符文页不完整")
		return
	}
	d := s.dd.Data()
	name := "ezlol"
	if d != nil {
		if ch, ok := d.Champions[body.ChampionID]; ok {
			name = "ezlol " + ch.Name
		}
	}
	ids := make([]int, 0, 9)
	for _, p := range body.Page.Perks {
		ids = append(ids, p.ID)
	}
	for _, sh := range body.Page.Shards {
		ids = append(ids, sh.ID)
	}
	page := lcu.RunePage{Name: name, PrimaryStyleID: body.Page.Primary.ID, SubStyleID: body.Page.Secondary.ID, SelectedPerkIDs: ids}
	out, err := c.ApplyRunePage(r.Context(), "ezlol", page)
	if err != nil {
		writeErr(w, 502, fmt.Sprintf("client refused rune page: %v", err))
		return
	}
	s.log.Info("rune page applied", "name", name)
	writeJSON(w, 200, map[string]any{"ok": true, "page": out})
}
