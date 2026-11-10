// Command ezlol runs the queue watcher, build service and web UI.
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
	"strconv"
	"syscall"
	"time"

	"github.com/aarlint/ezlol/internal/api"
	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/ddragon"
	"github.com/aarlint/ezlol/internal/settings"
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
	platform := flag.String("platform", env("EZLOL_PLATFORM", "sg2"), "Riot platform for build compilation (sg2, ph2, th2, tw2, vn2, ...)")
	matches := flag.Int("matches", envInt("EZLOL_MATCHES_PER_RUN", 200), "new matches per compile run")
	dev := flag.String("dev", env("EZLOL_DEV", ""), "proxy UI to a Vite dev server, e.g. http://localhost:5173")
	noOpen := flag.Bool("no-open", os.Getenv("EZLOL_NO_OPEN") != "", "do not open the browser on start")
	autoCompile := flag.Bool("auto-compile", os.Getenv("EZLOL_AUTO_COMPILE") != "", "start a compile run on boot when a key is set")
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

	// Settings file: UI-managed values; env vars / flags override for this run only.
	cfg := settings.Load(filepath.Join(*dataDir, "settings.json"))
	if k := os.Getenv("RIOT_API_KEY"); k != "" {
		_ = cfg.Update(func(s *settings.Settings) { s.RiotAPIKey = k })
	}
	if os.Getenv("EZLOL_PLATFORM") != "" {
		_ = cfg.Update(func(s *settings.Settings) { s.Platform = *platform })
	}
	if os.Getenv("EZLOL_MATCHES_PER_RUN") != "" {
		_ = cfg.Update(func(s *settings.Settings) { s.MatchesPerRun = *matches })
	}
	if *autoCompile {
		_ = cfg.Update(func(s *settings.Settings) { s.AutoCompile = true })
	}

	store, err := builds.NewStore(filepath.Join(*dataDir, "builds"))
	if err != nil {
		log.Error("build store", "err", err)
		os.Exit(1)
	}
	sc := cfg.Get()
	comp := builds.NewCompiler(builds.CompilerConfig{
		APIKey:        sc.RiotAPIKey,
		Platform:      sc.Platform,
		MatchesPerRun: sc.MatchesPerRun,
		Timeline:      true,
	}, store, dd, log)
	if comp.HasKey() {
		log.Info("riot api key present; ranked build compilation enabled", "platform", sc.Platform)
		if sc.AutoCompile {
			if err := comp.Start(ctx, 0); err != nil {
				log.Warn("auto compile", "err", err)
			}
		}
	} else {
		log.Info("no Riot API key; ranked compile disabled (set it in Settings). ARAM builds and runes need no key.")
	}

	upd := update.New(repo, version)
	go upd.Run(ctx, 6*time.Hour, func() bool { return cfg.Get().UpdateCheck })

	community := builds.NewCommunity(filepath.Join(*dataDir, "community"))
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
		Handler:           api.New(log, w, dd, store, comp, community, cfg, upd, version, ui, *dev).Handler(),
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
		comp.Stop()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
	if err := store.Save(); err != nil {
		log.Error("save builds", "err", err)
	}
	fmt.Fprintln(os.Stderr, "bye")
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
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
