package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func newDurableStore(dir string, maxAge time.Duration) *FileMediaStore {
	return NewFileMediaStoreWithCleanup(MediaCleanerConfig{
		Enabled:       false,
		MaxAge:        maxAge,
		Interval:      time.Minute,
		Dir:           dir,
		MaxTotalBytes: 0,
	})
}

// Any media kind must survive a restart: not only images but documents,
// archives, audio and video.
func TestDurableStoreKeepsAllMediaKindsAcrossRestart(t *testing.T) {
	storeDir := t.TempDir()
	srcDir := t.TempDir()

	cases := []struct {
		name        string
		contentType string
		body        []byte
	}{
		{"photo.png", "image/png", []byte("\x89PNG\r\n\x1a\n fake")},
		{"report.pdf", "application/pdf", []byte("%PDF-1.4 report")},
		{"archive.zip", "application/zip", []byte("PK\x03\x04 zip")},
		{"voice.ogg", "audio/ogg", []byte("OggS voice")},
		{"clip.mp4", "video/mp4", []byte("\x00\x00\x00\x18ftypmp42")},
		{"notes.txt", "text/plain", []byte("plain text")},
	}

	store := newDurableStore(storeDir, time.Hour)
	refs := make(map[string]string, len(cases))
	for _, c := range cases {
		src := writeTestFile(t, srcDir, c.name, c.body)
		ref, err := store.Store(src, MediaMeta{
			Filename:    c.name,
			ContentType: c.contentType,
			Source:      "telegram",
		}, "scope-1")
		if err != nil {
			t.Fatalf("Store(%s) error = %v", c.name, err)
		}
		refs[c.name] = ref
	}

	// The temporary sources are gone; only the durable copies remain.
	for _, c := range cases {
		if err := os.Remove(filepath.Join(srcDir, c.name)); err != nil {
			t.Fatalf("remove source %s: %v", c.name, err)
		}
	}

	reopened := newDurableStore(storeDir, time.Hour)
	for _, c := range cases {
		path, meta, err := reopened.ResolveWithMeta(refs[c.name])
		if err != nil {
			t.Fatalf("ResolveWithMeta(%s) after restart error = %v", c.name, err)
		}
		if meta.ContentType != c.contentType {
			t.Errorf("%s content type = %q, want %q", c.name, meta.ContentType, c.contentType)
		}
		if meta.Filename != c.name {
			t.Errorf("%s filename = %q, want %q", c.name, meta.Filename, c.name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: read stored file: %v", c.name, err)
		}
		if string(data) != string(c.body) {
			t.Errorf("%s: stored bytes differ", c.name)
		}
		if !strings.HasPrefix(path, storeDir) {
			t.Errorf("%s: stored outside durable dir: %s", c.name, path)
		}
	}
}

func TestDurableStoreWritesIndexFile(t *testing.T) {
	storeDir := t.TempDir()
	src := writeTestFile(t, t.TempDir(), "doc.docx", []byte("docx"))

	store := newDurableStore(storeDir, time.Hour)
	if _, err := store.Store(src, MediaMeta{Filename: "doc.docx", ContentType: "application/vnd.openxmlformats"}, "s"); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(storeDir, indexFileName))
	if err != nil {
		t.Fatalf("index file missing: %v", err)
	}
	if !strings.Contains(string(raw), "doc.docx") {
		t.Errorf("index does not mention the file: %s", raw)
	}
}

func TestDurableStoreCleanupDeletesCopyAndSource(t *testing.T) {
	storeDir := t.TempDir()
	src := writeTestFile(t, t.TempDir(), "old.png", []byte("png"))

	store := newDurableStore(storeDir, 10*time.Minute)
	store.nowFunc = func() time.Time { return time.Now().Add(-time.Hour) }
	ref, err := store.Store(src, MediaMeta{Filename: "old.png"}, "s")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	storedPath, err := store.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	store.nowFunc = time.Now
	if got := store.CleanExpired(); got != 1 {
		t.Fatalf("CleanExpired() = %d, want 1", got)
	}
	if _, err := os.Stat(storedPath); !os.IsNotExist(err) {
		t.Errorf("stored copy still exists (err=%v)", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("original source still exists (err=%v)", err)
	}
	if _, err := store.Resolve(ref); err == nil {
		t.Errorf("ref still resolves after cleanup")
	}

	reopened := newDurableStore(storeDir, 10*time.Minute)
	if _, err := reopened.Resolve(ref); err == nil {
		t.Errorf("ref resurrected from index after cleanup")
	}
}

func TestDurableStoreForgetOnlyKeepsOriginal(t *testing.T) {
	storeDir := t.TempDir()
	src := writeTestFile(t, t.TempDir(), "keep.bin", []byte("keep"))

	store := newDurableStore(storeDir, 10*time.Minute)
	store.nowFunc = func() time.Time { return time.Now().Add(-time.Hour) }
	ref, err := store.Store(src, MediaMeta{Filename: "keep.bin", CleanupPolicy: CleanupPolicyForgetOnly}, "s")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	storedPath, err := store.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	store.nowFunc = time.Now
	if got := store.CleanExpired(); got != 1 {
		t.Fatalf("CleanExpired() = %d, want 1", got)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("original file must survive forget_only cleanup: %v", err)
	}
	if _, err := os.Stat(storedPath); !os.IsNotExist(err) {
		t.Errorf("store copy should be reclaimed (err=%v)", err)
	}
}

func TestDurableStoreQuotaEvictsOldest(t *testing.T) {
	storeDir := t.TempDir()
	srcDir := t.TempDir()
	body := make([]byte, 64*1024)

	store := NewFileMediaStoreWithCleanup(MediaCleanerConfig{
		Enabled:       false,
		MaxAge:        time.Hour,
		Interval:      time.Minute,
		Dir:           storeDir,
		MaxTotalBytes: 150 * 1024, // room for two files, not three
	})

	var refs []string
	base := time.Now().Add(-3 * time.Minute)
	for i, name := range []string{"one.bin", "two.bin", "three.bin"} {
		src := writeTestFile(t, srcDir, name, body)
		store.nowFunc = func() time.Time { return base.Add(time.Duration(i) * time.Minute) }
		ref, err := store.Store(src, MediaMeta{Filename: name}, "s")
		if err != nil {
			t.Fatalf("Store(%s) error = %v", name, err)
		}
		refs = append(refs, ref)
	}

	if _, err := store.Resolve(refs[0]); err == nil {
		t.Errorf("oldest ref must be evicted by the quota")
	}
	for _, ref := range refs[1:] {
		if _, err := store.Resolve(ref); err != nil {
			t.Errorf("ref %s should survive the quota: %v", ref, err)
		}
	}
}
