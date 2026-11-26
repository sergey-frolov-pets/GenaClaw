package media

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	indexFileName = "index.json"
	filesDirName  = "files"
	maxNameLen    = 120
)

// indexRecord is the on-disk representation of a single media ref.
// It is written to <dir>/index.json so that refs keep resolving after a
// restart or a reload, for every media kind (image, audio, video, document,
// archive, ...), not only for images.
type indexRecord struct {
	Ref           string    `json:"ref"`
	Path          string    `json:"path"`
	SrcPath       string    `json:"src_path,omitempty"`
	Filename      string    `json:"filename,omitempty"`
	ContentType   string    `json:"content_type,omitempty"`
	Source        string    `json:"source,omitempty"`
	CleanupPolicy string    `json:"cleanup_policy,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	StoredAt      time.Time `json:"stored_at"`
	Size          int64     `json:"size,omitempty"`
}

func (s *FileMediaStore) indexFile() string {
	return filepath.Join(s.dir, indexFileName)
}

// persistFile copies srcPath into the durable store directory and returns the
// stored path. Files that already live inside the store directory are kept in
// place (no double copy). When the store has no directory the source path is
// returned unchanged (pure in-memory mode).
func (s *FileMediaStore) persistFile(refID, srcPath string) (string, error) {
	if s.dir == "" {
		return srcPath, nil
	}
	if insideDir(s.dir, srcPath) {
		return srcPath, nil
	}

	destDir := filepath.Join(s.dir, filesDirName, refID)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", fmt.Errorf("create media dir: %w", err)
	}

	dest := filepath.Join(destDir, sanitizeFilename(filepath.Base(srcPath)))
	if err := copyFileAtomic(srcPath, dest); err != nil {
		return "", err
	}
	return dest, nil
}

func insideDir(dir, path string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	return strings.HasPrefix(absPath, absDir+string(os.PathSeparator))
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, string(os.PathSeparator), "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\x00", "")
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	if len(name) > maxNameLen {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		keep := maxNameLen - len(ext)
		if keep < 1 {
			keep = 1
		}
		name = name[:keep] + ext
	}
	return name
}

func copyFileAtomic(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	tmp := dest + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close dest: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename dest: %w", err)
	}
	return nil
}

// loadIndex restores refs persisted by a previous process. Entries whose file
// disappeared are dropped silently: a missing file is worse than no ref.
func (s *FileMediaStore) loadIndex() {
	if s.dir == "" {
		return
	}

	raw, err := os.ReadFile(s.indexFile())
	if err != nil {
		if !os.IsNotExist(err) {
			logger.WarnCF("media", "index: failed to read", map[string]any{
				"path":  s.indexFile(),
				"error": err.Error(),
			})
		}
		return
	}

	var records []indexRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		logger.WarnCF("media", "index: corrupt, starting empty", map[string]any{
			"path":  s.indexFile(),
			"error": err.Error(),
		})
		return
	}

	loaded, dropped := 0, 0
	s.mu.Lock()
	for _, rec := range records {
		if rec.Ref == "" || rec.Path == "" {
			dropped++
			continue
		}
		info, err := os.Stat(rec.Path)
		if err != nil || info.IsDir() {
			dropped++
			continue
		}
		policy := normalizeCleanupPolicy(CleanupPolicy(rec.CleanupPolicy))
		size := rec.Size
		if size <= 0 {
			size = info.Size()
		}
		s.refs[rec.Ref] = mediaEntry{
			path:     rec.Path,
			meta:     MediaMeta{Filename: rec.Filename, ContentType: rec.ContentType, Source: rec.Source, CleanupPolicy: policy},
			storedAt: rec.StoredAt,
			size:     size,
		}
		if rec.Scope != "" {
			if s.scopeToRefs[rec.Scope] == nil {
				s.scopeToRefs[rec.Scope] = make(map[string]struct{})
			}
			s.scopeToRefs[rec.Scope][rec.Ref] = struct{}{}
			s.refToScope[rec.Ref] = rec.Scope
		}
		s.refToPath[rec.Ref] = rec.Path
		if rec.SrcPath != "" && rec.SrcPath != rec.Path {
			s.srcPaths[rec.Ref] = rec.SrcPath
		}
		s.pathStates[rec.Path] = pathRefState{
			refCount:       1,
			deleteEligible: policy == CleanupPolicyDeleteOnCleanup || rec.SrcPath != "",
		}
		loaded++
	}
	s.mu.Unlock()

	if loaded > 0 || dropped > 0 {
		logger.InfoCF("media", "index loaded", map[string]any{
			"dir":     s.dir,
			"loaded":  loaded,
			"dropped": dropped,
		})
	}
}

// saveIndex persists the current ref table atomically.
func (s *FileMediaStore) saveIndex() {
	if s.dir == "" {
		return
	}

	s.mu.RLock()
	records := make([]indexRecord, 0, len(s.refs))
	for ref, entry := range s.refs {
		records = append(records, indexRecord{
			Ref:           ref,
			Path:          entry.path,
			SrcPath:       s.srcPaths[ref],
			Filename:      entry.meta.Filename,
			ContentType:   entry.meta.ContentType,
			Source:        entry.meta.Source,
			CleanupPolicy: string(entry.meta.CleanupPolicy),
			Scope:         s.refToScope[ref],
			StoredAt:      entry.storedAt,
			Size:          entry.size,
		})
	}
	s.mu.RUnlock()

	sort.Slice(records, func(i, j int) bool { return records[i].StoredAt.Before(records[j].StoredAt) })

	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		logger.WarnCF("media", "index: marshal failed", map[string]any{"error": err.Error()})
		return
	}

	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		logger.WarnCF("media", "index: mkdir failed", map[string]any{"dir": s.dir, "error": err.Error()})
		return
	}

	tmp := s.indexFile() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		logger.WarnCF("media", "index: write failed", map[string]any{"path": tmp, "error": err.Error()})
		return
	}
	if err := os.Rename(tmp, s.indexFile()); err != nil {
		logger.WarnCF("media", "index: rename failed", map[string]any{"path": s.indexFile(), "error": err.Error()})
	}
}

// enforceQuota evicts the oldest refs until the total stored size fits
// MaxTotalBytes. Without a limit it is a no-op.
func (s *FileMediaStore) enforceQuota() {
	if s.maxTotalBytes <= 0 || s.dir == "" {
		return
	}

	type victim struct {
		ref      string
		storedAt time.Time
	}

	s.mu.Lock()
	var total int64
	for _, entry := range s.refs {
		total += entry.size
	}
	if total <= s.maxTotalBytes {
		s.mu.Unlock()
		return
	}

	victims := make([]victim, 0, len(s.refs))
	for ref, entry := range s.refs {
		victims = append(victims, victim{ref: ref, storedAt: entry.storedAt})
	}
	sort.Slice(victims, func(i, j int) bool { return victims[i].storedAt.Before(victims[j].storedAt) })

	var removePaths []string
	for _, v := range victims {
		if total <= s.maxTotalBytes {
			break
		}
		entry := s.refs[v.ref]
		scope := s.refToScope[v.ref]
		paths := s.releaseRefLocked(v.ref, entry.path)
		removePaths = append(removePaths, paths...)
		total -= entry.size
		if scope != "" {
			if scopeRefs, ok := s.scopeToRefs[scope]; ok {
				delete(scopeRefs, v.ref)
				if len(scopeRefs) == 0 {
					delete(s.scopeToRefs, scope)
				}
			}
		}
	}
	s.mu.Unlock()

	for _, p := range removePaths {
		removeStoredFile(p)
	}
	if len(removePaths) > 0 {
		logger.InfoCF("media", "quota: evicted oldest media", map[string]any{
			"max_bytes": s.maxTotalBytes,
			"files":     len(removePaths),
		})
	}
	s.saveIndex()
}

func removeStoredFile(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logger.WarnCF("media", "release: failed to remove file", map[string]any{
			"path":  path,
			"error": err.Error(),
		})
		return
	}
	// Best effort: drop the per-ref directory once it is empty.
	dir := filepath.Dir(path)
	if filepath.Base(filepath.Dir(dir)) == filesDirName {
		os.Remove(dir)
	}
}
