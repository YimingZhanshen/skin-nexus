package ygg

import (
	"net/http"
)

// ScopedHasJoinedHandler serves the standard hasJoined wire on a route chosen
// by the caller. It is intentionally separate from Handler so legacy routes and
// DTOs remain frozen. All deny/miss/fault outcomes use the standard empty 204.
func (a *API) ScopedHasJoinedHandler(policy VerificationPolicy, observe func(VerificationResult, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		query := r.URL.Query()
		result, err := a.svc.ScopedHasJoined(r.Context(), query.Get("username"), query.Get("serverId"), query.Get("ip"), policy)
		if observe != nil {
			observe(result, err)
		}
		if err != nil || len(result.Profile) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(result.Profile)
	})
}
