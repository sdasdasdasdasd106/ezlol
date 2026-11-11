package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/aarlint/ezlol/internal/settings"
)

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	m := s.settings.Get().Masked()
	m["ocrAvailable"] = s.ocr.Available()
	m["ocrLang"] = s.ocr.Lang()
	m["logDir"] = s.tlog.Dir()
	m["uploadRepo"] = s.repo
	m["lastUpload"] = s.lastUpload()
	m["aiUsage"] = s.ai.Usage.Snapshot()
	m["version"] = s.version
	writeJSON(w, 200, m)
}

// putSettings applies a partial update.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OCR         *bool   `json:"ocr"`
		AutoScan    *bool   `json:"autoScan"`
		UpdateCheck *bool   `json:"updateCheck"`
		TestLog     *bool   `json:"testLog"`
		LogShots    *bool   `json:"logShots"`
		GitHubToken *string `json:"githubToken"`
		AutoUpload  *bool   `json:"autoUpload"`
		DeepSeekKey *string `json:"deepseekKey"`
		AIReview    *bool   `json:"aiReview"`
		AIResearch  *bool   `json:"aiResearch"`
		AIText      *bool   `json:"aiText"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "请求格式不对")
		return
	}
	err := s.settings.Update(func(st *settings.Settings) {
		if body.OCR != nil {
			st.OCR = *body.OCR
		}
		if body.AutoScan != nil {
			st.AutoScan = *body.AutoScan
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
		if body.GitHubToken != nil {
			st.GitHubToken = strings.TrimSpace(*body.GitHubToken)
		}
		if body.AutoUpload != nil {
			st.AutoUpload = *body.AutoUpload
		}
		if body.DeepSeekKey != nil {
			st.DeepSeekKey = strings.TrimSpace(*body.DeepSeekKey)
		}
		if body.AIReview != nil {
			st.AIReview = *body.AIReview
		}
		if body.AIResearch != nil {
			st.AIResearch = *body.AIResearch
		}
		if body.AIText != nil {
			st.AIText = *body.AIText
		}
	})
	if err != nil {
		writeErr(w, 500, "保存设置失败："+err.Error())
		return
	}
	cur := s.settings.Get()
	s.log.Info("settings updated", "ocr", cur.OCR, "testLog", cur.TestLog)
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
