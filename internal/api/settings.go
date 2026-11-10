package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aarlint/ezlol/internal/settings"
)

var validPlatform = regexp.MustCompile(`^[a-z]{2,4}[0-9]?$`)

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	m := s.settings.Get().Masked()
	m["ocrAvailable"] = s.ocr.Available()
	m["ocrLang"] = s.ocr.Lang()
	m["logDir"] = s.tlog.Dir()
	m["version"] = s.version
	writeJSON(w, 200, m)
}

// putSettings applies a partial update. The Riot key is accepted as
// "riotApiKey" (empty string clears it) and is never echoed back or logged.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RiotAPIKey    *string `json:"riotApiKey"`
		Platform      *string `json:"platform"`
		MatchesPerRun *int    `json:"matchesPerRun"`
		AutoCompile   *bool   `json:"autoCompile"`
		OCR           *bool   `json:"ocr"`
		UpdateCheck   *bool   `json:"updateCheck"`
		TestLog       *bool   `json:"testLog"`
		LogShots      *bool   `json:"logShots"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "请求格式不对")
		return
	}
	if body.Platform != nil && !validPlatform.MatchString(*body.Platform) {
		writeErr(w, 400, "服务器代码格式不对，例如 sg2、ph2、th2、tw2、vn2")
		return
	}
	if body.MatchesPerRun != nil && (*body.MatchesPerRun < 10 || *body.MatchesPerRun > 5000) {
		writeErr(w, 400, "每次统计的对局数必须在 10 到 5000 之间")
		return
	}
	if body.RiotAPIKey != nil {
		k := strings.TrimSpace(*body.RiotAPIKey)
		if k != "" && (len(k) < 20 || strings.ContainsAny(k, " \n\t")) {
			writeErr(w, 400, "这看起来不像 Riot API key（应以 RGAPI- 开头）")
			return
		}
		body.RiotAPIKey = &k
	}
	err := s.settings.Update(func(st *settings.Settings) {
		if body.RiotAPIKey != nil {
			st.RiotAPIKey = *body.RiotAPIKey
		}
		if body.Platform != nil {
			st.Platform = *body.Platform
		}
		if body.MatchesPerRun != nil {
			st.MatchesPerRun = *body.MatchesPerRun
		}
		if body.AutoCompile != nil {
			st.AutoCompile = *body.AutoCompile
		}
		if body.OCR != nil {
			st.OCR = *body.OCR
		}
		if body.UpdateCheck != nil {
			st.UpdateCheck = *body.UpdateCheck
		}
		if body.TestLog != nil {
			st.TestLog = *body.TestLog
		}
		if body.LogShots != nil {
			st.LogShots = *body.LogShots
		}
	})
	if err != nil {
		writeErr(w, 500, "保存设置失败："+err.Error())
		return
	}
	cur := s.settings.Get()
	s.compiler.Configure(cur.RiotAPIKey, cur.Platform, cur.MatchesPerRun)
	s.log.Info("settings updated", "keySet", cur.RiotAPIKey != "", "platform", cur.Platform, "ocr", cur.OCR)
	s.getSettings(w, r)
}

func (s *Server) getUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.update.Info())
}

func (s *Server) checkUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, 200, s.update.Check(ctx))
}
