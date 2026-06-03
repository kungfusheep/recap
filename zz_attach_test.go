package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagAttachPersist(t *testing.T) {
	path, err := pasteClipboardImage()
	if err != nil { t.Fatalf("paste: %v", err) }
	wantDir := filepath.Dir(path)
	if !strings.HasSuffix(wantDir, "/attachments") {
		t.Fatalf("not in attachments dir: %s", path)
	}
	if strings.Contains(path, "/T/") || strings.Contains(path, "var/folders") {
		t.Fatalf("still in TMPDIR: %s", path)
	}
	t.Logf("persisted screenshot at: %s", path)
}
