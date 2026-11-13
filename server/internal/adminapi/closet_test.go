package adminapi

// The closet surface is admin-only like every other panel route; this pins
// the registration itself, not just b.auth in the abstract: a route added
// without the wrapper answers404/200 here instead of403, and the AST gate
// alone would only catch it if someone remembered to look. The store-level
// rename/remove roundtrip lives in the store integration suite (SetClosetItemName
// / RemoveClosetEntry, including the cross-user hijack → ErrNotFound case);
// what is new here is that these three patterns exist and sit behind auth.
import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClosetRoutesRefuseNonAdminSessions(t *testing.T) {
	stubSession(t, func(*http.Request) (int64, bool) { return 0, false })
	h := (&Backend{AdminTok: "sekret"}).Handler()
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/v1/users/1/closet"},
		{http.MethodPut, "/api/admin/v1/users/1/closet/2"},
		{http.MethodDelete, "/api/admin/v1/users/1/closet/2"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403 — a non-admin session must not reach the closet surface",
				c.method, c.path, rec.Code)
		}
	}
}

// The {id}/{cid} guards must answer400 before anything touches the store:
// the Backend here has a nil Store on purpose, so a handler that reached for
// it would panic instead of answering — the same trap that once shipped the
// admin player routes dead ({pid} bound, "id" read; routeparams gate exists
// because of it). This pins the runtime behavior on top of that AST gate.
func TestClosetParamGuardsRunBeforeTheStore(t *testing.T) {
	b := &Backend{} // nil Store on purpose
	cases := []struct {
		name   string
		method string
		path   string
		id     string
		cid    string
	}{
		{"uid not a number", http.MethodGet, "/api/admin/v1/users/abc/closet", "abc", ""},
		{"uid zero", http.MethodGet, "/api/admin/v1/users/0/closet", "0", ""},
		{"entry id not a number", http.MethodPut, "/api/admin/v1/users/1/closet/xyz", "1", "xyz"},
		{"entry id zero", http.MethodDelete, "/api/admin/v1/users/1/closet/0", "1", "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body *httptest.ResponseRecorder
			r := httptest.NewRequest(c.method, c.path, nil)
			r.SetPathValue("id", c.id)
			if c.cid != "" {
				r.SetPathValue("cid", c.cid)
			}
			body = httptest.NewRecorder()
			switch c.method {
			case http.MethodGet:
				b.handleAdminClosetList(body, r)
			case http.MethodPut:
				b.handleAdminClosetRename(body, r)
			case http.MethodDelete:
				b.handleAdminClosetRemove(body, r)
			}
			if body.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400 (guard must fire before the nil store)", body.Code)
			}
		})
	}

	// Valid ids but no JSON body: still a 400, still no store access.
	r := httptest.NewRequest(http.MethodPut, "/api/admin/v1/users/1/closet/2", nil)
	r.SetPathValue("id", "1")
	r.SetPathValue("cid", "2")
	rec := httptest.NewRecorder()
	b.handleAdminClosetRename(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing body = %d, want 400", rec.Code)
	}
}
