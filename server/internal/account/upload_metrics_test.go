package account

// upload_metrics_test.go — the upload counter is recorded at the one place
// every upload path funnels through, so a rejection the panel shows a user
// and a rejection the plugin bridge shows a launcher are the same number.
//
// The service below has no store: ensure() refuses before anything else, which
// is exactly the early-return this test needs to prove the counter moves on
// the failure path without standing up a database.

import (
	"context"
	"testing"

	"skin-nexus/server/internal/metrics"
)

func TestRejectedUploadIsCounted(t *testing.T) {
	before := metrics.Uploads.Value("error")
	s := &Service{}
	if _, _, e := s.UploadTexture(context.Background(), 1, "skin", "", "probe", nil, CallMeta{}); e == nil {
		t.Fatal("a service with no store must refuse the upload")
	}
	if got := metrics.Uploads.Value("error"); got != before+1 {
		t.Fatalf("uploads error counter = %d, want %d — the refusal was not counted", got, before+1)
	}
}
