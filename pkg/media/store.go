package media

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// CleanupPolicy controls how the MediaStore treats the underlying file when
// a ref is released or expires.
type CleanupPolicy string

const (
	// CleanupPolicyDeleteOnCleanup means the file is store-managed and may be
	// deleted once the final ref for that path is gone.
	CleanupPolicyDeleteOnCleanup CleanupPolicy = "delete_on_cleanup"
	// CleanupPolicyForgetOnly means the store should only drop ref mappings and
	// must never delete the underlying file.
	CleanupPolicyForgetOnly CleanupPolicy = "forget_only"
)

// MediaMeta holds metadata about a stored media file.
type MediaMeta struct {
	Filename      string
	ContentType   string
	Source        string        // "telegram", "discord", "tool:image-gen", etc.
	CleanupPolicy CleanupPolicy // defaults to CleanupPolicyDeleteOnCleanup
}

// MediaStore manages the lifecycle of media files associated with processing scopes.
type MediaStore interface {
	// Store registers an existing local file under the given scope.
	// Returns a ref identifier (e.g. "media://<id>").
	// Store does not move or copy the file; it only records the mapping.
	// If meta.CleanupPolicy is empty, CleanupPolicyDeleteOnCleanup is assumed.
	Store(localPath string, meta MediaMeta, scope string) (ref string, err error)

	// Resolve returns the local file path for a given ref.
	Resolve(ref string) (localPath string, err error)

	// ResolveWithMeta returns the local file path and metadata for a given ref.
	ResolveWithMeta(ref string) (localPath string, meta MediaMeta, err error)

	// ReleaseAll deletes all files registered under the given scope
	// and removes the mapping entries. File-not-exist errors are ignored.
	ReleaseAll(scope string) error
}

// mediaEntry holds the path and metadata for a stored media file.
type mediaEntry struct {
	path     string
	meta     MediaMeta
	storedAt time.Time
	size     int64
}

type pathRefState struct {
	refCount       int
	deleteEligible bool
}

// MediaCleanerConfig configures the background TTL cleanup and the durable
// media directory.
type MediaCleanerConfig struct {
	Enabled  bool
	MaxAge   time.Duration
	Interval time.Duration

	// Dir is the durable storage directory. Files are copied there and refs
	// are persisted to <Dir>/index.json, so everything survives a restart.
	// Empty means pure in-memory mode (legacy behaviour).
	Dir string
	// MaxTotalBytes caps the total size kept in Dir; the oldest refs are
	// evicted first. Zero means unlimited.
	MaxTotalBytes int64
}

// FileMediaStore manages media refs. Without a Dir it is in-memory only;
// with a Dir it copies every stored file into that directory and persists the
// ref table, so refs keep resolving after a restart for any media kind.
type FileMediaStore struct {
	mu          sync.RWMutex
	refs        map[string]mediaEntry
	scopeToRefs map[string]map[string]struct{}
	refToScope  map[string]string
	refToPath   map[string]string
	pathStates  map[string]pathRefState
	srcPaths    map[string]string // ref -> original path when the file was copied

	dir           string
	maxTotalBytes int64
	saveMu        sync.Mutex

	cleanerCfg MediaCleanerConfig
	stop       chan struct{}
	startOnce  sync.Once
	stopOnce   sync.Once
	nowFunc    func() time.Time // for testing
}

// NewFileMediaStore creates a new FileMediaStore without background cleanup.
func NewFileMediaStore() *FileMediaStore {
	return &FileMediaStore{
		refs:        make(map[string]mediaEntry),
		scopeToRefs: make(map[string]map[string]struct{}),
		refToScope:  make(map[string]string),
		refToPath:   make(map[string]string),
		pathStates:  make(map[string]pathRefState),
		srcPaths:    make(map[string]string),
		nowFunc:     time.Now,
	}
}

// NewFileMediaStoreWithCleanup creates a FileMediaStore with TTL-based background
// cleanup. When cfg.Dir is set the ref table is loaded from disk immediately.
func NewFileMediaStoreWithCleanup(cfg MediaCleanerConfig) *FileMediaStore {
	s := &FileMediaStore{
		refs:          make(map[string]mediaEntry),
		scopeToRefs:   make(map[string]map[string]struct{}),
		refToScope:    make(map[string]string),
		refToPath:     make(map[string]string),
		pathStates:    make(map[string]pathRefState),
		srcPaths:      make(map[string]string),
		dir:           cfg.Dir,
		maxTotalBytes: cfg.MaxTotalBytes,
		cleanerCfg:    cfg,
		stop:          make(chan struct{}),
		nowFunc:       time.Now,
	}
	s.loadIndex()
	return s
}

// Store registers a local file under the given scope. The file must exist.
func (s *FileMediaStore) Store(srcPath string, meta MediaMeta, scope string) (string, error) {
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("media store: %s: %w", srcPath, err)
	}

	ref := "media://" + uuid.New().String()
	meta.CleanupPolicy = normalizeCleanupPolicy(meta.CleanupPolicy)
	if strings.TrimSpace(meta.Filename) == "" {
		meta.Filename = filepath.Base(srcPath)
	}

	storedPath := srcPath
	originalPath := ""
	if s.dir != "" {
		persisted, err := s.persistFile(strings.TrimPrefix(ref, "media://"), srcPath)
		if err != nil {
			// Fall back to the source path: a working ref to a temporary file is
			// better than failing the whole message.
			logger.WarnCF("media", "store: failed to persist file, using source path", map[string]any{
				"path":  srcPath,
				"error": err.Error(),
			})
		} else {
			storedPath = persisted
			if persisted != srcPath {
				originalPath = srcPath
			}
		}
	}

	size := int64(0)
	if info, err := os.Stat(storedPath); err == nil {
		size = info.Size()
	}

	s.mu.Lock()

	s.refs[ref] = mediaEntry{path: storedPath, meta: meta, storedAt: s.nowFunc(), size: size}
	if originalPath != "" {
		s.srcPaths[ref] = originalPath
	}
	if s.scopeToRefs[scope] == nil {
		s.scopeToRefs[scope] = make(map[string]struct{})
	}
	s.scopeToRefs[scope][ref] = struct{}{}
	s.refToScope[ref] = scope
	s.refToPath[ref] = storedPath

	pathState := s.pathStates[storedPath]
	if pathState.refCount == 0 {
		// Copied files belong to the store, so they are always reclaimable;
		// forget_only only protects the caller's original file.
		pathState.deleteEligible = meta.CleanupPolicy == CleanupPolicyDeleteOnCleanup || originalPath != ""
	} else if meta.CleanupPolicy == CleanupPolicyForgetOnly {
		// Be conservative: once a path is borrowed externally, never let this
		// lifecycle auto-delete it even if store-managed refs also exist.
		pathState.deleteEligible = false
	}
	pathState.refCount++
	s.pathStates[storedPath] = pathState
	s.mu.Unlock()

	s.saveIndex()
	s.enforceQuota()

	return ref, nil
}

// Resolve returns the local path for the given ref.
func (s *FileMediaStore) Resolve(ref string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.refs[ref]
	if !ok {
		return "", fmt.Errorf("media store: unknown ref: %s", ref)
	}
	return entry.path, nil
}

// ResolveWithMeta returns the local path and metadata for the given ref.
func (s *FileMediaStore) ResolveWithMeta(ref string) (string, MediaMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.refs[ref]
	if !ok {
		return "", MediaMeta{}, fmt.Errorf("media store: unknown ref: %s", ref)
	}
	return entry.path, entry.meta, nil
}

// ReleaseAll removes all files under the given scope and cleans up mappings.
// Phase 1 (under lock): remove entries from maps.
// Phase 2 (no lock): delete store-managed files from disk once their final
// path ref is gone.
func (s *FileMediaStore) ReleaseAll(scope string) error {
	// Phase 1: collect paths and remove from maps under lock
	var paths []string

	s.mu.Lock()
	refs, ok := s.scopeToRefs[scope]
	if !ok {
		s.mu.Unlock()
		return nil
	}

	for ref := range refs {
		fallbackPath := ""
		if entry, exists := s.refs[ref]; exists {
			fallbackPath = entry.path
		}
		paths = append(paths, s.releaseRefLocked(ref, fallbackPath)...)
	}
	delete(s.scopeToRefs, scope)
	s.mu.Unlock()

	// Phase 2: delete files without holding the lock
	for _, p := range paths {
		removeStoredFile(p)
	}

	s.saveIndex()
	return nil
}

// CleanExpired removes all entries older than MaxAge.
// Phase 1 (under lock): identify expired entries and remove from maps.
// Phase 2 (no lock): delete store-managed files from disk to minimize lock contention.
func (s *FileMediaStore) CleanExpired() int {
	if s.cleanerCfg.MaxAge <= 0 {
		return 0
	}

	// Phase 1: collect expired entries under lock
	s.mu.Lock()
	cutoff := s.nowFunc().Add(-s.cleanerCfg.MaxAge)
	var expired []string
	removed := 0

	for ref, entry := range s.refs {
		if !entry.storedAt.Before(cutoff) {
			continue
		}
		if scope, ok := s.refToScope[ref]; ok {
			if scopeRefs, ok := s.scopeToRefs[scope]; ok {
				delete(scopeRefs, ref)
				if len(scopeRefs) == 0 {
					delete(s.scopeToRefs, scope)
				}
			}
		}
		expired = append(expired, s.releaseRefLocked(ref, entry.path)...)
		removed++
	}
	s.mu.Unlock()

	// Phase 2: delete files without holding the lock
	for _, p := range expired {
		removeStoredFile(p)
	}

	if removed > 0 {
		s.saveIndex()
	}

	return removed
}

func normalizeCleanupPolicy(policy CleanupPolicy) CleanupPolicy {
	switch policy {
	case "", CleanupPolicyDeleteOnCleanup:
		return CleanupPolicyDeleteOnCleanup
	case CleanupPolicyForgetOnly:
		return CleanupPolicyForgetOnly
	default:
		return CleanupPolicyDeleteOnCleanup
	}
}

func (s *FileMediaStore) releaseRefLocked(ref, fallbackPath string) []string {
	entry, hasEntry := s.refs[ref]

	path := fallbackPath
	if storedPath, ok := s.refToPath[ref]; ok {
		path = storedPath
		delete(s.refToPath, ref)
	}

	originalPath := s.srcPaths[ref]
	delete(s.srcPaths, ref)

	delete(s.refs, ref)
	delete(s.refToScope, ref)

	if path == "" {
		return nil
	}

	pathState, ok := s.pathStates[path]
	if !ok {
		return nil
	}
	if pathState.refCount > 1 {
		pathState.refCount--
		s.pathStates[path] = pathState
		return nil
	}

	delete(s.pathStates, path)
	if !pathState.deleteEligible {
		return nil
	}

	paths := []string{path}
	if originalPath != "" && originalPath != path &&
		(!hasEntry || entry.meta.CleanupPolicy == CleanupPolicyDeleteOnCleanup) {
		paths = append(paths, originalPath)
	}
	return paths
}

// Start begins the background cleanup goroutine if cleanup is enabled.
// Safe to call multiple times; only the first call starts the goroutine.
func (s *FileMediaStore) Start() {
	if !s.cleanerCfg.Enabled || s.stop == nil {
		return
	}
	if s.cleanerCfg.Interval <= 0 || s.cleanerCfg.MaxAge <= 0 {
		logger.WarnCF("media", "cleanup: skipped due to invalid config", map[string]any{
			"interval": s.cleanerCfg.Interval.String(),
			"max_age":  s.cleanerCfg.MaxAge.String(),
		})
		return
	}

	s.startOnce.Do(func() {
		logger.InfoCF("media", "cleanup enabled", map[string]any{
			"interval": s.cleanerCfg.Interval.String(),
			"max_age":  s.cleanerCfg.MaxAge.String(),
		})

		go func() {
			ticker := time.NewTicker(s.cleanerCfg.Interval)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					if n := s.CleanExpired(); n > 0 {
						logger.InfoCF("media", "cleanup: removed expired entries", map[string]any{
							"count": n,
						})
					}
				case <-s.stop:
					return
				}
			}
		}()
	})
}

// Stop terminates the background cleanup goroutine.
// Safe to call multiple times; only the first call closes the channel.
func (s *FileMediaStore) Stop() {
	if s.stop == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stop)
	})
}
