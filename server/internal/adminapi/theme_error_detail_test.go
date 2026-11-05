package adminapi

// A theme pack that fails validation is reported as "invalid value" with the
// wrapped detail in the body. Some of those details are not about the pack at
// all: the staging step wraps os.MkdirTemp failures, so the operator's browser
// used to receive a server filesystem path (`mkdir C:\Users\<name>\AppData\Local\Temp\
// skintheme-4290567848: Access is denied`) — and a permission problem on the
// host reads exactly like "your zip is broken", sending them to re-package a
// file that was fine. The sentence belongs in the log, the decision belongs in
// the response.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"skin-nexus/server/internal/thememkt"
)

func TestThemeValidationDetailStaysOutOfTheResponse(t *testing.T) {
	staged := fmt.Errorf("%w: %v", thememkt.ErrBadThemePkg,
		errors.New(`mkdir C:\Users\operator\AppData\Local\Temp\skintheme-4290567848: Access is denied`))

	b := &Backend{}
	r := httptest.NewRequest(http.MethodPost, "/api/admin/v1/themes/upload", nil)
	rec := httptest.NewRecorder()
	b.themeMarketError(r, rec, staged)

	body := rec.Body.String()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want the same 409 the operator already gets for a bad pack", rec.Code)
	}
	for _, leak := range []string{"skintheme-", "AppData", `C:\`, "Access is denied", "/tmp/"} {
		if strings.Contains(body, leak) {
			t.Errorf("the response carries the internal detail %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, "invalid_value") {
		t.Errorf("the response lost its machine-readable code: %s", body)
	}
}

// A pack that really is bad still has to be told apart from a server problem:
// this branch is the operator's only feedback, so it stays a 409, not a 500.
func TestThemePackRejectionsKeepTheirStatus(t *testing.T) {
	b := &Backend{}
	cases := []struct {
		err  error
		want int
	}{
		{fmt.Errorf("%w: theme.json missing", thememkt.ErrBadThemePkg), http.StatusConflict},
		{thememkt.ErrUnverified, http.StatusConflict},
		{thememkt.ErrNotFound, http.StatusConflict},
		{thememkt.ErrMarketOff, http.StatusNotFound},
		{errors.New("registry unreachable"), http.StatusBadGateway},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		b.themeMarketError(httptest.NewRequest(http.MethodPost, "/api/admin/v1/themes/market", nil), rec, c.err)
		if rec.Code != c.want {
			t.Errorf("%v → %d, want %d (%s)", c.err, rec.Code, c.want, rec.Body.String())
		}
	}
}
