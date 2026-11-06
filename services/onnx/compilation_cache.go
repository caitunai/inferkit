package onnx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	compilationCacheEntries = 8
	compilationCacheTTL     = 30 * 24 * time.Hour
)

// ErrCache indicates unavailable, contended or full native compilation storage.
var ErrCache = errors.New("ONNX compilation cache unavailable")

// CompilationCache protects one model directory from cleanup until Close.
// Finish releases the short compilation lock after session creation.
type CompilationCache struct {
	lease     *os.File
	build     *os.File
	directory string
}

// AcquireCompilationCache uses the system user cache, never the deployment tree.
// Only CoreML callers should opt in; failures may safely fall back to no cache.
// The caller must hold an ONNX Lease until its session and cache are closed.
func AcquireCompilationCache(data []byte, units string) (*CompilationCache, error) {
	if !ort.IsInitialized() {
		return nil, errors.Join(ErrCache, ErrRuntime)
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, errors.Join(ErrCache, err)
	}
	system, err := compilationCachePlatform()
	if err != nil {
		return nil, err
	}
	key := compilationCacheKey(data, ort.GetVersion(), system, units)
	return acquireCompilationCache(filepath.Join(base, "inferkit", "onnx", "coreml"), key, time.Now())
}

func compilationCacheKey(data []byte, version, system, units string) string {
	model := sha256.Sum256(data)
	// Include the actual EP options; ORT's own in-memory model keys alone do
	// not detect changed weights with identical graph names.
	parts := []string{"v1:optall:sequential", hex.EncodeToString(model[:]), version, system, runtime.GOOS, runtime.GOARCH}
	options := (Backend{ComputeUnits: units}).coreMLOptions()
	for _, key := range slices.Sorted(maps.Keys(options)) {
		parts = append(parts, key, options[key])
	}
	identity := strings.Join(parts, "\x00")
	key := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(key[:])
}

// Directory is the isolated directory passed to the CoreML execution provider.
func (c *CompilationCache) Directory() string { return c.directory }

// Finish allows another process to initialize from the same compiled model.
func (c *CompilationCache) Finish() error {
	if c.build == nil {
		return nil
	}
	err := c.build.Close()
	c.build = nil
	if err != nil {
		return errors.Join(ErrCache, err)
	}
	return nil
}

// Close releases both locks. The engine must destroy its session first.
func (c *CompilationCache) Close() error {
	err := c.Finish()
	if c.lease != nil {
		err = errors.Join(err, c.lease.Close())
		c.lease = nil
	}
	if err != nil {
		return errors.Join(ErrCache, err)
	}
	return nil
}

func acquireCompilationCache(directory, key string, now time.Time) (*CompilationCache, error) {
	if !isCompilationKey(key) {
		return nil, ErrCache
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, errors.Join(ErrCache, err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.Join(ErrCache, err)
	}
	defer closeCacheResource(root)
	guard, err := lockCacheFile(root, ".root.lock", true)
	if err != nil {
		return nil, err
	}
	defer closeCacheResource(guard)
	if err = maintainCompilationCache(root, key, now); err != nil {
		return nil, err
	}
	return openCompilationCache(root, key, now)
}

func openCompilationCache(root *os.Root, key string, now time.Time) (*CompilationCache, error) {
	if err := root.MkdirAll(key, 0o700); err != nil {
		return nil, errors.Join(ErrCache, err)
	}
	lease, err := lockCacheFile(root, filepath.Join(key, ".lease"), false)
	if err != nil {
		return nil, err
	}
	c := &CompilationCache{directory: filepath.Join(root.Name(), key), lease: lease}
	c.build, err = lockCacheFile(root, filepath.Join(key, ".build"), true)
	if err == nil {
		err = root.Chtimes(filepath.Join(key, ".lease"), now, now)
	}
	if err != nil {
		return nil, errors.Join(ErrCache, err, c.Close())
	}
	return c, nil
}

type compilationEntry struct {
	used time.Time
	name string
}

func compilationEntryInfo(root *os.Root, entry fs.DirEntry) (compilationEntry, error) {
	info, err := root.Stat(filepath.Join(entry.Name(), ".lease"))
	if errors.Is(err, os.ErrNotExist) {
		info, err = entry.Info() // Recover a directory left by interrupted creation.
	}
	if err != nil {
		return compilationEntry{}, errors.Join(ErrCache, err)
	}
	return compilationEntry{name: entry.Name(), used: info.ModTime()}, nil
}

func compilationEntries(root *os.Root) ([]compilationEntry, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, errors.Join(ErrCache, err)
	}
	defer closeCacheResource(directory)
	entries, err := directory.ReadDir(compilationCacheEntries + 2)
	if err != nil || len(entries) > compilationCacheEntries+1 {
		return nil, errors.Join(ErrCache, err)
	}
	result := make([]compilationEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !isCompilationKey(entry.Name()) {
			continue
		}
		info, statErr := compilationEntryInfo(root, entry)
		if statErr != nil {
			return nil, errors.Join(ErrCache, statErr)
		}
		result = append(result, info)
	}
	slices.SortFunc(result, func(a, b compilationEntry) int { return a.used.Compare(b.used) })
	return result, nil
}

func maintainCompilationCache(root *os.Root, key string, now time.Time) error {
	entries, err := compilationEntries(root)
	if err != nil {
		return err
	}
	exists := slices.ContainsFunc(entries, func(e compilationEntry) bool { return e.name == key })
	count := len(entries)
	for _, entry := range entries {
		if entry.keep(key, now, exists, count) {
			continue
		}
		removed, removeErr := removeCompilationEntry(root, entry.name)
		if removeErr != nil {
			return removeErr
		}
		if removed {
			count--
		}
	}
	if !exists && count >= compilationCacheEntries {
		return ErrCache
	}
	return nil
}

func (e compilationEntry) keep(key string, now time.Time, exists bool, count int) bool {
	return e.name == key || now.Sub(e.used) < compilationCacheTTL && (exists || count < compilationCacheEntries)
}

func removeCompilationEntry(root *os.Root, name string) (bool, error) {
	lease, err := lockCacheFile(root, filepath.Join(name, ".lease"), true)
	if err != nil {
		return false, nil // A live process or inaccessible entry must not be removed.
	}
	defer closeCacheResource(lease)
	if err = root.RemoveAll(name); err != nil {
		return false, errors.Join(ErrCache, err)
	}
	return true, nil
}

func isCompilationKey(name string) bool {
	if len(name) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil
}

func closeCacheResource(resource interface{ Close() error }) {
	if err := resource.Close(); err != nil {
		log.Warn().Err(err).Msg("close ONNX compilation cache resource")
	}
}
