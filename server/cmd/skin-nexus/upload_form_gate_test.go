package main

// upload_form_gate_test.go: the upload card's two interaction contracts at
// source level, the way hash_plate_gate_test reads .vue files.
//
//  1. The arm-model badge describes a PICKED file. Showing「正在识别手臂模型…」
//     before any file exists is a promise about nothing — the badge appears
//     only once a file is in play (three states: hidden → checking → result).
//  2. The drop zone is the one file control. The choose-file button lives
//     INSIDE the zone (keyboard fallback, stop-click so the zone handler does
//     not double-fire) — the old full-width button stacked under the zone read
//     as a second, redundant control.

import (
	"strings"
	"testing"
)

func TestUploadModelBadgeGatedOnFile(t *testing.T) {
	src := webAsset(t, "web", "src", "pages", "DashboardPage.vue")
	if !strings.Contains(src, "uploadType === 'skin' && uploadFile") {
		t.Error("the model badge must be gated on a picked file (uploadType skin AND uploadFile) — it describes the file, not the form")
	}
	if strings.Contains(src, `v-if="uploadType === 'skin'" class="muted model-badge"`) {
		t.Error("the model badge is back to its ungated form — it shows before any file is picked")
	}
}

func TestUploadFileControlsMerged(t *testing.T) {
	src := webAsset(t, "web", "src", "pages", "DashboardPage.vue")
	if n := strings.Count(src, `class="secondary pick-file"`); n != 1 {
		t.Errorf("want exactly one choose-file control (inside the drop zone), found %d — stacked controls read as two ways to do one thing", n)
	}
	if !strings.Contains(src, `@click.stop="pickFile"`) {
		t.Error("the in-zone choose-file button must stop propagation — the zone itself also opens the picker")
	}
}
