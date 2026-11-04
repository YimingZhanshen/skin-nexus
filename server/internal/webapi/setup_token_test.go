package webapi

// The wizard is mounted before any account exists, so for a container that is
// already answering on the public interface it used to be a race nobody won:
// whoever POSTed first simply became the super admin. These tests pin the two
// halves of the fix — the token that has to come along, and the claim that makes
// a racing loser answer the same way the one-way door always has.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/store"
)

func migratedSetupBackend(t *testing.T, token string) (*store.Mysql, http.Handler) {
	t.Helper()
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "setup.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := migrate.ApplySQLDirTracked(m.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return m, (&Backend{Store: m, SetupToken: token}).Handler()
}

func postSetup(h http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func TestSetupRequiresTheInstallToken(t *testing.T) {
	m, h := migratedSetupBackend(t, "letmein")

	rec := postSetup(h, `{"site_name":"Rig","nickname":"admin","password":"password-1","setup_token":"nope"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if body.Code != "setup.token_bad" {
		t.Fatalf("code = %q, want setup.token_bad", body.Code)
	}
	if n := countUsers(t, m); n != 0 {
		t.Fatalf("a refused setup created %d users; nothing may be written before the token passes", n)
	}

	rec = postSetup(h, `{"site_name":"Rig","nickname":"admin","password":"password-1"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing token = %d, want 403", rec.Code)
	}

	rec = postSetup(h, `{"site_name":"Rig","nickname":"admin","password":"password-1","setup_token":"letmein"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("right token = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if n := countUsers(t, m); n != 1 {
		t.Fatalf("users = %d, want the one bootstrap admin", n)
	}

	// One-way door, token or not.
	rec = postSetup(h, `{"nickname":"second","password":"password-2","setup_token":"letmein"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second completed setup = %d, want 404", rec.Code)
	}
}

// A site with no token configured is a site whose wizard is open to the network.
func TestSetupWithoutATokenStaysOpenButSaysSo(t *testing.T) {
	_, h := migratedSetupBackend(t, "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/setup/status", nil))
	var st map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode status: %v (%s)", err, rec.Body.String())
	}
	if st["needs_setup"] != true {
		t.Fatalf("needs_setup = %v, want true", st["needs_setup"])
	}
	if st["token_required"] != false {
		t.Fatalf("token_required = %v, want false when no token exists", st["token_required"])
	}
	if strings.Contains(rec.Body.String(), "letmein") {
		t.Fatal("the status endpoint must never echo a token")
	}
}

func TestSetupConcurrentClaimsProduceOneAdmin(t *testing.T) {
	m, h := migratedSetupBackend(t, "letmein")

	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = postSetup(h, `{"site_name":"Rig","nickname":"admin","password":"password-1","setup_token":"letmein"}`).Code
		}(i)
	}
	wg.Wait()

	if n := countUsers(t, m); n != 1 {
		t.Fatalf("users = %d after four racing setups, want exactly 1 (codes %v)", n, codes)
	}
	var supers int
	if err := m.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM users WHERE permission >= 2`).Scan(&supers); err != nil {
		t.Fatal(err)
	}
	if supers != 1 {
		t.Fatalf("super admins = %d, want 1", supers)
	}
	ok := 0
	for _, c := range codes {
		if c == http.StatusOK {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d of four racing setups reported success, want exactly 1 (codes %v)", ok, codes)
	}
}

func countUsers(t *testing.T, m *store.Mysql) int {
	t.Helper()
	var n int
	if err := m.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}
