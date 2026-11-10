package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"

	"github.com/aarlint/ezlol/internal/ddragon"
	"github.com/aarlint/ezlol/internal/lcu"
)

// infoCache memoises per-champion tactical info from the client for the process lifetime.
type infoCache struct {
	mu sync.Mutex
	m  map[int]*lcu.ChampionInfo
}

func (ic *infoCache) get(ctx context.Context, c *lcu.Client, id int) *lcu.ChampionInfo {
	if c == nil || id == 0 {
		return nil
	}
	ic.mu.Lock()
	if ic.m == nil {
		ic.m = map[int]*lcu.ChampionInfo{}
	}
	if v, ok := ic.m[id]; ok {
		ic.mu.Unlock()
		return v
	}
	ic.mu.Unlock()
	v, err := c.ChampionInfo(ctx, id)
	if err != nil {
		return nil
	}
	ic.mu.Lock()
	ic.m[id] = v
	ic.mu.Unlock()
	return v
}

func (s *Server) championInfo(w http.ResponseWriter, r *http.Request) {
	d := s.dd.Data()
	key := r.PathValue("key")
	var champ ddragon.Champion
	var ok bool
	if id, err := strconv.Atoi(key); err == nil {
		champ, ok = d.Champions[id]
	} else {
		champ, ok = d.ChampionByKey(key)
	}
	if !ok {
		writeErr(w, 404, "找不到这个英雄")
		return
	}
	info := s.info.get(r.Context(), s.watcher.Client(), champ.ID)
	if info == nil {
		writeJSON(w, 200, map[string]any{"champion": champ})
		return
	}
	writeJSON(w, 200, map[string]any{"champion": champ, "info": info})
}
