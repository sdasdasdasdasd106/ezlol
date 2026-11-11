package api

// TESTING AID: uploading the test logs to GitHub. Remove this file together
// with internal/logupload and the GitHubToken / AutoUpload settings.

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/aarlint/ezlol/internal/logupload"
)

type uploadState struct {
	mu      sync.Mutex
	running bool
	last    map[string]any // outcome of the last upload, shown in Settings
	inGame  bool           // to notice a game ending
}

func (s *Server) lastUpload() map[string]any {
	s.upload.mu.Lock()
	defer s.upload.mu.Unlock()
	return s.upload.last
}

// runUpload uploads the logs once (not concurrently) and remembers the outcome.
func (s *Server) runUpload(ctx context.Context, why string) (logupload.Result, error) {
	s.upload.mu.Lock()
	if s.upload.running {
		s.upload.mu.Unlock()
		return logupload.Result{}, errBusy
	}
	s.upload.running = true
	s.upload.mu.Unlock()

	u := &logupload.Uploader{Repo: s.repo, Token: s.settings.Get().GitHubToken}
	res, err := u.Upload(ctx, s.tlog.Dir(), 15)
	out := map[string]any{"at": time.Now().Format("2006-01-02 15:04:05"), "why": why, "uploaded": res.Uploaded, "skipped": res.Skipped, "url": res.URL}
	if err != nil {
		out["error"] = err.Error()
		s.log.Warn("log upload", "err", err)
	}
	s.upload.mu.Lock()
	s.upload.running = false
	s.upload.last = out
	s.upload.mu.Unlock()
	return res, err
}

type busyError struct{}

func (busyError) Error() string { return "正在上传，请稍等" }

var errBusy = busyError{}

func (s *Server) uploadLogs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	res, err := s.runUpload(ctx, "manual")
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

// noteInGame uploads the logs in the background when a game ends, if enabled.
func (s *Server) noteInGame(in bool) {
	s.upload.mu.Lock()
	ended := s.upload.inGame && !in
	s.upload.inGame = in
	s.upload.mu.Unlock()
	cfg := s.settings.Get()
	if !ended || !cfg.AutoUpload || cfg.GitHubToken == "" {
		return
	}
	go func() {
		// Give the last log lines a moment, then upload.
		time.Sleep(5 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_, _ = s.runUpload(ctx, "game-end")
	}()
}
