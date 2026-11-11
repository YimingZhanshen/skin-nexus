package webapi

import (
	"database/sql"
	"net/http"
	"testing"
)

// The wizard's empty-email branch used to write admin@localhost: an address
// that can never receive mail, yet makes the panel believe the operator has an
// inbox. Empty must stay NULL (the column and every reader already support it),
// and a typed address must pass the same shape rule registration uses.
func TestSetupEmptyEmailWritesNull(t *testing.T) {
	m, h := migratedSetupBackend(t, "")
	rec := postSetup(h, `{"site_name":"Rig","nickname":"admin","email":"","password":"password-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var email sql.NullString
	if err := m.DB().QueryRow(`SELECT email FROM users WHERE nickname = ?`, "admin").Scan(&email); err != nil {
		t.Fatalf("read email: %v", err)
	}
	if email.Valid {
		t.Fatalf("empty wizard email stored %q, want SQL NULL (a placeholder inbox is a lie)", email.String)
	}
}

func TestSetupRejectsMalformedEmail(t *testing.T) {
	for _, addr := range []string{"admin@localhost", "a@b", "not-an-email"} {
		m, h := migratedSetupBackend(t, "")
		rec := postSetup(h, `{"site_name":"Rig","nickname":"admin","email":"`+addr+`","password":"password-1"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("email %q = %d %s, want 400", addr, rec.Code, rec.Body.String())
		}
		if n := countUsers(t, m); n != 0 {
			t.Fatalf("email %q: refused setup created %d users — the one-way door must not half-open", addr, n)
		}
		var done int
		if err := m.DB().QueryRow(`SELECT COUNT(*) FROM options WHERE k = 'setup_done'`).Scan(&done); err != nil {
			t.Fatalf("read marker: %v", err)
		}
		if done != 0 {
			t.Fatalf("email %q: refused setup wrote the setup marker", addr)
		}
	}
}

func TestSetupKeepsAValidEmail(t *testing.T) {
	m, h := migratedSetupBackend(t, "")
	rec := postSetup(h, `{"site_name":"Rig","nickname":"admin","email":"ops@example.com","password":"password-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var email string
	if err := m.DB().QueryRow(`SELECT email FROM users WHERE nickname = ?`, "admin").Scan(&email); err != nil {
		t.Fatalf("read email: %v", err)
	}
	if email != "ops@example.com" {
		t.Fatalf("valid email stored %q, want it verbatim (validation must not clear it)", email)
	}
}
