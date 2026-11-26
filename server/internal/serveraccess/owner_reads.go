package serveraccess

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

func (c *control) events(w http.ResponseWriter, r *http.Request) {
	serverID, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server id")
		return
	}
	who := saCaller(r)
	if _, err := c.cfg.Store.SAServerByID(r.Context(), who.TenantID, serverID); err != nil {
		writeSAStoreError(w, err)
		return
	}

	now := c.now()
	from, to := now.Add(-24*time.Hour), now
	parseTime := func(name string, dst *time.Time) bool {
		raw := r.URL.Query().Get(name)
		if raw == "" {
			return true
		}
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Event time range must use RFC3339")
			return false
		}
		*dst = parsed.UTC()
		return true
	}
	if !parseTime("from", &from) || !parseTime("to", &to) {
		return
	}
	if from.After(to) || to.Sub(from) > 366*24*time.Hour {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Event time range is invalid or longer than 366 days")
		return
	}
	var before int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Event cursor must be a positive id")
			return
		}
		before = parsed
	}
	size := 50
	if raw := r.URL.Query().Get("size"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Event page size must be positive")
			return
		}
		size = parsed
		if size > 200 {
			size = 200
		}
	}
	rows, err := c.cfg.Store.ListSAJoinEventsPage(r.Context(), who.TenantID, serverID, from, to, before, size+1)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	hasMore := len(rows) > size
	if hasMore {
		rows = rows[:size]
	}
	events := make([]store.SAOwnerJoinEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, store.SAOwnerJoinEvent{
			ID: row.ID, CreatedAt: row.CreatedAt, Outcome: row.Outcome,
			ReasonCode: row.ReasonCode, DurationMS: row.DurationMS, SourceMode: row.SourceMode,
		})
	}
	var nextBefore int64
	if hasMore && len(events) > 0 {
		nextBefore = events[len(events)-1].ID
	}
	writeSAJSON(w, http.StatusOK, map[string]any{
		"events": events, "nextBefore": nextBefore, "hasMore": hasMore,
	})
}

func (c *control) playerLookup(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len([]rune(query)) > 64 {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "A player name or UUID is required")
		return
	}
	player, err := c.cfg.Store.LookupSAPlayer(r.Context(), query)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, player)
}
