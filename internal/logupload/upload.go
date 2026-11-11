// Package logupload pushes the local test logs to a branch of the GitHub
// repository, so a test session can be read without sending files by hand.
//
// TESTING AID: meant for the test phase only. Remove this package, its
// settings (GitHubToken, AutoUpload), the /api/logs/upload route and the
// settings section once testing is done.
package logupload

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Branch receives the logs; pushes to it do not trigger the build workflow.
const Branch = "test-logs"

// apiBase is the GitHub REST endpoint (a variable so tests can point it elsewhere).
var apiBase = "https://api.github.com"

// Result says what an upload did.
type Result struct {
	Uploaded int    `json:"uploaded"`
	Skipped  int    `json:"skipped"` // unchanged since the last upload
	URL      string `json:"url"`
}

// Uploader talks to the GitHub REST API with a fine-grained token that can
// write the repository's contents.
type Uploader struct {
	Repo  string // owner/name
	Token string
	HTTP  *http.Client
}

// Upload sends the newest log files (last 3 days) and the newest screenshots
// (up to maxShots) from dir to <Branch>:logs/.
func (u *Uploader) Upload(ctx context.Context, dir string, maxShots int) (Result, error) {
	var res Result
	if u.Token == "" {
		return res, errors.New("还没有填 GitHub 令牌")
	}
	if !strings.Contains(u.Repo, "/") {
		return res, errors.New("不知道要上传到哪个仓库")
	}
	if u.HTTP == nil {
		u.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	if err := u.ensureBranch(ctx); err != nil {
		return res, err
	}
	files := newest(dir, ".jsonl", 3)
	for _, f := range newest(filepath.Join(dir, "shots"), ".png", maxShots) {
		files = append(files, f)
	}
	if len(files) == 0 {
		return res, errors.New("日志文件夹是空的")
	}
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		changed, err := u.put(ctx, "logs/"+filepath.ToSlash(rel), f)
		if err != nil {
			return res, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if changed {
			res.Uploaded++
		} else {
			res.Skipped++
		}
	}
	res.URL = fmt.Sprintf("https://github.com/%s/tree/%s/logs", u.Repo, Branch)
	return res, nil
}

// newest returns up to n files with the extension, newest name last.
func newest(dir, ext string, n int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // names start with a date / timestamp
	if len(names) > n {
		names = names[len(names)-n:]
	}
	out := make([]string, 0, len(names))
	for _, nm := range names {
		out = append(out, filepath.Join(dir, nm))
	}
	return out
}

func (u *Uploader) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+u.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := u.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return res.StatusCode, errors.New("GitHub 拒绝了这个令牌（过期了，或者没有这个仓库的 Contents 读写权限）")
	}
	if out != nil && res.StatusCode < 300 {
		_ = json.Unmarshal(data, out)
	}
	if res.StatusCode >= 300 && res.StatusCode != http.StatusNotFound {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		return res.StatusCode, fmt.Errorf("GitHub %d: %s", res.StatusCode, e.Message)
	}
	return res.StatusCode, nil
}

// ensureBranch creates the log branch from the default branch if needed.
func (u *Uploader) ensureBranch(ctx context.Context) error {
	code, err := u.do(ctx, http.MethodGet, "/repos/"+u.Repo+"/git/ref/heads/"+Branch, nil, nil)
	if err != nil {
		return err
	}
	if code != http.StatusNotFound {
		return nil
	}
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if code, err := u.do(ctx, http.MethodGet, "/repos/"+u.Repo, nil, &repo); err != nil || code == http.StatusNotFound {
		if err == nil {
			err = errors.New("找不到仓库 " + u.Repo + "（令牌没有这个仓库的权限？）")
		}
		return err
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if _, err := u.do(ctx, http.MethodGet, "/repos/"+u.Repo+"/git/ref/heads/"+repo.DefaultBranch, nil, &ref); err != nil {
		return err
	}
	_, err = u.do(ctx, http.MethodPost, "/repos/"+u.Repo+"/git/refs", map[string]string{"ref": "refs/heads/" + Branch, "sha": ref.Object.SHA}, nil)
	return err
}

// gitBlobSHA is the id git gives a file's content, which the contents API
// reports, so unchanged files are not uploaded again.
func gitBlobSHA(b []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// put creates or updates one file on the branch; false = it was already up to date.
func (u *Uploader) put(ctx context.Context, path, file string) (bool, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	var cur struct {
		SHA string `json:"sha"`
	}
	escaped := strings.ReplaceAll(path, " ", "%20")
	code, err := u.do(ctx, http.MethodGet, "/repos/"+u.Repo+"/contents/"+escaped+"?ref="+Branch, nil, &cur)
	if err != nil {
		return false, err
	}
	if code != http.StatusNotFound && cur.SHA == gitBlobSHA(b) {
		return false, nil
	}
	body := map[string]string{"message": "logs: " + filepath.Base(file), "content": base64.StdEncoding.EncodeToString(b), "branch": Branch}
	if code != http.StatusNotFound && cur.SHA != "" {
		body["sha"] = cur.SHA
	}
	_, err = u.do(ctx, http.MethodPut, "/repos/"+u.Repo+"/contents/"+escaped, body, nil)
	return err == nil, err
}
