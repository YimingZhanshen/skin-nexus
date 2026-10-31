package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"skin-nexus/server/internal/store"
)

type fakePreviewAuthStore struct {
	sessionUID    int64
	sessionErr    error
	permission    int
	permissionErr error
	owner         int64
	ownerErr      error
}

func (f fakePreviewAuthStore) SessionUser(context.Context, string) (int64, error) {
	return f.sessionUID, f.sessionErr
}

func (f fakePreviewAuthStore) UserPermission(context.Context, int64) (int, error) {
	return f.permission, f.permissionErr
}

func (f fakePreviewAuthStore) TextureOwner(context.Context, int64) (int64, error) {
	return f.owner, f.ownerErr
}

func TestPrivatePreviewAllowed(t *testing.T) {
	cases := []struct {
		name   string
		cookie string
		st     fakePreviewAuthStore
		want   bool
	}{
		{"anonymous", "", fakePreviewAuthStore{sessionErr: store.ErrNotFound}, false},
		{"invalid session", "expired", fakePreviewAuthStore{sessionErr: store.ErrNotFound}, false},
		{"owner", "session", fakePreviewAuthStore{sessionUID: 7, owner: 7}, true},
		{"admin", "session", fakePreviewAuthStore{sessionUID: 8, permission: 1, owner: 7}, true},
		{"super admin", "session", fakePreviewAuthStore{sessionUID: 9, permission: 2, owner: 7}, true},
		{"other user", "session", fakePreviewAuthStore{sessionUID: 8, owner: 7}, false},
		{"owner when permission lookup fails", "session", fakePreviewAuthStore{sessionUID: 7, permissionErr: errors.New("db"), owner: 7}, true},
		{"owner lookup fails", "session", fakePreviewAuthStore{sessionUID: 7, ownerErr: store.ErrNotFound}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/preview/42", nil)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: skinSessionCookie, Value: tc.cookie})
			}
			if got := privatePreviewAllowed(tc.st, r, 42); got != tc.want {
				t.Fatalf("privatePreviewAllowed() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPreviewCacheControl(t *testing.T) {
	if got := previewCacheControl(true); got != "private, no-store" {
		t.Fatalf("private preview cache = %q, want %q", got, "private, no-store")
	}
	if got := previewCacheControl(false); got != "public, max-age=31536000, immutable" {
		t.Fatalf("public preview cache = %q", got)
	}
}
