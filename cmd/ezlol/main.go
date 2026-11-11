// Command ezlol runs the ARAM Mayhem build and augment assistant and its web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/aarlint/ezlol/internal/api"
	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/ddragon"
	"github.com/aarlint/ezlol/internal/settings"
	"github.com/aarlint/ezlol/internal/testlog"
	"github.com/aarlint/ezlol/internal/update"
	"github.com/aarlint/ezlol/internal/watcher"
	"github.com/aarlint/ezlol/web"
)

// version is set at build time with -ldflags "-X main.version=1.0.1".
var version = "dev"

// repo is the GitHub repository whose releases the update check follows; CI
// sets it to the repository that built the binary (-X main.repo=owner/name).
var repo = "aarlint/ezlol"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	defaultData, _ := os.UserConfigDir()
	defaultData = filepath.Join(defaultData, "ezlol")

	addr := flag.String("addr", env("EZLOL_ADDR", "127.0.0.1:7331"), "listen address (loopback only by default)")
	dataDir := flag.String("data", env("EZLOL_DATA_DIR", defaultData), "data directory")
	dev := flag.String("dev", env("EZLOL_DEV", ""), "proxy UI to a Vite dev server, e.g. http://localhost:5173")
	noOpen := flag.Bool("no-open", os.Getenv("EZLOL_NO_OPEN") != "", "do not open the browser on start")
	debug := flag.Bool("debug", os.Getenv("EZLOL_DEBUG") != "", "debug logging")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Error("data dir", "err", err)
		os.Exit(1)
	}

	dd := ddragon.New(filepath.Join(*dataDir, "ddragon"))
	if err := dd.Load(ctx); err != nil {
		log.Error("data dragon load failed", "err", err)
		os.Exit(1)
	}
	log.Info("ezlol", "version", version)
	log.Info("data dragon loaded", "version", dd.Data().Version, "champions", len(dd.Data().Champions))

	cfg := settings.Load(filepath.Join(*dataDir, "settings.json"))

	upd := update.New(repo, version)
	go upd.Run(ctx, 6*time.Hour, func() bool { return cfg.Get().UpdateCheck })

	community := builds.NewCommunity(filepath.Join(*dataDir, "community"))
	tlog := testlog.New(filepath.Join(*dataDir, "logs"), func() bool { return cfg.Get().TestLog }, func() bool { return cfg.Get().LogShots })
	tlog.Log("start", map[string]any{"version": version, "os": runtime.GOOS})
	w := watcher.New(log, time.Second, cfg)
	go w.Run(ctx)

	// Refresh Data Dragon daily so a new patch is picked up without a restart.
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := dd.Load(ctx); err != nil {
					log.Warn("data dragon refresh", "err", err)
				}
			}
		}
	}()

	var ui fs.FS
	if *dev == "" {
		sub, err := fs.Sub(web.Dist, "dist")
		if err == nil {
			if _, err := fs.Stat(sub, "index.html"); err == nil {
				ui = sub
			}
		}
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.New(log, w, dd, community, cfg, upd, tlog, repo, version, ui, *dev).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Error("listen", "addr", *addr, "err", err)
		os.Exit(1)
	}
	url := "http://" + ln.Addr().String()
	log.Info("ezlol listening", "url", url)
	if !*noOpen {
		openBrowser(url)
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "bye")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
