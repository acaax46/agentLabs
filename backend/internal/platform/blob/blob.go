// Package blob stores artifact bodies behind a narrow interface. MongoDB keeps ownership and
// digests; bytes live here. The filesystem implementation serves one node or a shared volume.
// A network object store (S3, GCS, Azure) implements Store the same way, so moving bodies
// between backends never touches business records.
package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var (
	ErrNotFound   = errors.New("blob not found")
	ErrTooLarge   = errors.New("blob exceeds the size limit")
	ErrExists     = errors.New("blob already exists")
	ErrInvalidKey = errors.New("invalid blob key")
)

// Info describes a stored body. The digest is computed while streaming, never trusted from callers.
type Info struct {
	Key    string
	Size   int64
	SHA256 string
}

// Object is an opened body. Seeking lets HTTP range requests work without buffering.
type Object interface {
	io.ReadSeeker
	io.Closer
}

// Store is create-only: a key is written once and then immutable until deleted.
type Store interface {
	// Put streams r into key and fails with ErrTooLarge when more than maxBytes arrive.
	Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (Info, error)
	Open(ctx context.Context, key string) (Object, error)
	// Delete is idempotent: deleting a missing key succeeds.
	Delete(ctx context.Context, key string) error
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{7,127}$`)

func validKey(key string) error {
	if !keyPattern.MatchString(key) || strings.Contains(key, "..") {
		return ErrInvalidKey
	}
	return nil
}

// ctxReader stops long copies when the caller goes away.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// FS keeps bodies under root with a two-level fan-out so no directory grows unbounded.
type FS struct{ root string }

func NewFS(root string) (*FS, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("blob root directory is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0o700); err != nil {
		return nil, err
	}
	return &FS{root: abs}, nil
}

func (f *FS) location(key string) (dir, file string, err error) {
	if err = validKey(key); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:2])
	dir = filepath.Join(f.root, name[:2], name[2:4])
	return dir, filepath.Join(dir, key), nil
}

func (f *FS) Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (Info, error) {
	dir, file, err := f.location(key)
	if err != nil {
		return Info{}, err
	}
	if maxBytes < 0 {
		return Info{}, fmt.Errorf("negative size limit")
	}
	if err = ctx.Err(); err != nil {
		return Info{}, err
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return Info{}, err
	}
	temp, err := os.CreateTemp(dir, ".put-*")
	if err != nil {
		return Info{}, err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(ctxReader{ctx: ctx, r: r}, maxBytes+1))
	if err != nil {
		_ = temp.Close()
		return Info{}, err
	}
	if size > maxBytes {
		_ = temp.Close()
		return Info{}, ErrTooLarge
	}
	if err = temp.Sync(); err != nil {
		_ = temp.Close()
		return Info{}, err
	}
	if err = temp.Close(); err != nil {
		return Info{}, err
	}
	// A hard link publishes the finished file atomically and refuses to replace an existing key.
	if err = os.Link(tempName, file); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Info{}, ErrExists
		}
		return Info{}, err
	}
	return Info{Key: key, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (f *FS) Open(ctx context.Context, key string) (Object, error) {
	_, file, err := f.location(key)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	opened, err := os.Open(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return opened, nil
}

func (f *FS) Delete(ctx context.Context, key string) error {
	_, file, err := f.location(key)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Memory is an in-process Store for tests and ephemeral development runs.
type Memory struct {
	mu    sync.Mutex
	items map[string][]byte
}

func NewMemory() *Memory { return &Memory{items: map[string][]byte{}} }

type memoryObject struct {
	*bytesReader
}

// bytesReader adapts a byte slice to Object without copying it.
type bytesReader struct {
	data []byte
	off  int64
}

func (b *bytesReader) Read(p []byte) (int, error) {
	if b.off >= int64(len(b.data)) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += int64(n)
	return n, nil
}

func (b *bytesReader) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = b.off + offset
	case io.SeekEnd:
		next = int64(len(b.data)) + offset
	default:
		return 0, fmt.Errorf("invalid whence")
	}
	if next < 0 {
		return 0, fmt.Errorf("negative position")
	}
	b.off = next
	return next, nil
}

func (memoryObject) Close() error { return nil }

func (m *Memory) Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (Info, error) {
	if err := validKey(key); err != nil {
		return Info{}, err
	}
	if maxBytes < 0 {
		return Info{}, fmt.Errorf("negative size limit")
	}
	data, err := io.ReadAll(io.LimitReader(ctxReader{ctx: ctx, r: r}, maxBytes+1))
	if err != nil {
		return Info{}, err
	}
	if int64(len(data)) > maxBytes {
		return Info{}, ErrTooLarge
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.items[key]; exists {
		return Info{}, ErrExists
	}
	m.items[key] = data
	sum := sha256.Sum256(data)
	return Info{Key: key, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}

func (m *Memory) Open(ctx context.Context, key string) (Object, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.items[key]
	if !ok {
		return nil, ErrNotFound
	}
	return memoryObject{&bytesReader{data: data}}, nil
}

func (m *Memory) Delete(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	return nil
}

// Len reports how many bodies are stored; tests use it to prove cleanup ran.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}
