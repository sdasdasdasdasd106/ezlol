package logupload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeGitHub keeps refs and file contents in memory.
type fakeGitHub struct {
	mu    sync.Mutex
	refs  map[string]string
	files map[string][]byte
	puts  int
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(401)
		return
	}
	p := r.URL.Path
	switch {
	case r.Method == "GET" && p == "/repos/me/ezlol":
		json.NewEncoder(w).Encode(map[string]string{"default_branch": "main"})
	case r.Method == "GET" && strings.HasPrefix(p, "/repos/me/ezlol/git/ref/heads/"):
		name := strings.TrimPrefix(p, "/repos/me/ezlol/git/ref/heads/")
		sha, ok := f.refs[name]
		if !ok {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": sha}})
	case r.Method == "POST" && p == "/repos/me/ezlol/git/refs":
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		f.refs[strings.TrimPrefix(b["ref"], "refs/heads/")] = b["sha"]
		w.WriteHeader(201)
	case strings.HasPrefix(p, "/repos/me/ezlol/contents/"):
		path := strings.TrimPrefix(p, "/repos/me/ezlol/contents/")
		if r.Method == "GET" {
			b, ok := f.files[path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"sha": gitBlobSHA(b)})
			return
		}
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		if _, exists := f.files[path]; exists && b["sha"] == "" {
			w.WriteHeader(422) // GitHub requires the current sha to update
			return
		}
		data, _ := base64.StdEncoding.DecodeString(b["content"])
		f.files[path] = data
		f.puts++
		w.WriteHeader(201)
	default:
		w.WriteHeader(404)
	}
}

func TestUpload(t *testing.T) {
	gh := &fakeGitHub{refs: map[string]string{"main": "abc"}, files: map[string][]byte{}}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	apiBase = srv.URL

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "shots"), 0o755)
	for _, n := range []string{"ezlol-2026-11-08.jsonl", "ezlol-2026-11-09.jsonl", "ezlol-2026-11-10.jsonl", "ezlol-2026-11-11.jsonl"} {
		os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "shots", "20261111-1-lv1.png"), []byte("png"), 0o644)

	u := &Uploader{Repo: "me/ezlol", Token: "tok"}
	res, err := u.Upload(context.Background(), dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	// newest 3 logs + 1 shot, on a branch created from main
	if res.Uploaded != 4 || gh.refs[Branch] != "abc" || gh.files["logs/ezlol-2026-11-08.jsonl"] != nil || string(gh.files["logs/shots/20261111-1-lv1.png"]) != "png" {
		t.Fatalf("res %+v refs %v files %v", res, gh.refs, len(gh.files))
	}
	// Unchanged files are skipped; a grown log is updated in place.
	os.WriteFile(filepath.Join(dir, "ezlol-2026-11-11.jsonl"), []byte("more"), 0o644)
	res, err = u.Upload(context.Background(), dir, 10)
	if err != nil || res.Uploaded != 1 || res.Skipped != 3 || string(gh.files["logs/ezlol-2026-11-11.jsonl"]) != "more" {
		t.Fatalf("second upload: %+v %v", res, err)
	}
	// A bad token says so.
	if _, err := (&Uploader{Repo: "me/ezlol", Token: "bad"}).Upload(context.Background(), dir, 10); err == nil || !strings.Contains(err.Error(), "令牌") {
		t.Fatalf("bad token: %v", err)
	}
}
