package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

func stores(t *testing.T) map[string]Store {
	t.Helper()
	fs, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]Store{"fs": fs, "memory": NewMemory()}
}

func TestStoresShareOneContract(t *testing.T) {
	body := []byte("order_id,amount\nA-1,10.50\n")
	sum := sha256.Sum256(body)
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			info, err := store.Put(ctx, "art_0123456789abcdef", bytes.NewReader(body), 1024)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size != int64(len(body)) || info.SHA256 != hex.EncodeToString(sum[:]) {
				t.Fatalf("info = %+v", info)
			}
			object, err := store.Open(ctx, "art_0123456789abcdef")
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(object)
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("read = %q, %v", got, err)
			}
			// Seeking supports HTTP range responses.
			if _, err = object.Seek(6, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			tail, _ := io.ReadAll(object)
			if !bytes.Equal(tail, body[6:]) {
				t.Fatalf("tail = %q", tail)
			}
			if err = object.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err = store.Put(ctx, "art_0123456789abcdef", strings.NewReader("other"), 1024); !errors.Is(err, ErrExists) {
				t.Fatalf("overwrite = %v", err)
			}
			if err = store.Delete(ctx, "art_0123456789abcdef"); err != nil {
				t.Fatal(err)
			}
			if err = store.Delete(ctx, "art_0123456789abcdef"); err != nil {
				t.Fatalf("second delete = %v", err)
			}
			if _, err = store.Open(ctx, "art_0123456789abcdef"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("open after delete = %v", err)
			}
		})
	}
}

func TestStoresEnforceSizeLimitKeysAndContext(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := store.Put(ctx, "art_toolarge0000001", strings.NewReader("12345"), 4); !errors.Is(err, ErrTooLarge) {
				t.Fatalf("too large = %v", err)
			}
			if _, err := store.Open(ctx, "art_toolarge0000001"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("rejected body was stored: %v", err)
			}
			// A body of exactly the limit is accepted.
			if _, err := store.Put(ctx, "art_exactlimit00001", strings.NewReader("1234"), 4); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []string{"", "short", "../escape-0001", "a/b/c/d/e/f/g/h", "has space 0001", strings.Repeat("a", 200), "art_dot..dot0001"} {
				if _, err := store.Put(ctx, bad, strings.NewReader("x"), 4); !errors.Is(err, ErrInvalidKey) {
					t.Fatalf("key %q accepted: %v", bad, err)
				}
				if _, err := store.Open(ctx, bad); !errors.Is(err, ErrInvalidKey) {
					t.Fatalf("open key %q: %v", bad, err)
				}
				if err := store.Delete(ctx, bad); !errors.Is(err, ErrInvalidKey) {
					t.Fatalf("delete key %q: %v", bad, err)
				}
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := store.Put(cancelled, "art_cancelled000001", strings.NewReader("x"), 4); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled put = %v", err)
			}
			if _, err := store.Open(cancelled, "art_exactlimit00001"); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled open = %v", err)
			}
			if err := store.Delete(cancelled, "art_exactlimit00001"); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled delete = %v", err)
			}
		})
	}
}

func TestFSRejectsEmptyRootAndNegativeLimit(t *testing.T) {
	if _, err := NewFS("  "); err == nil {
		t.Fatal("empty root accepted")
	}
	fs, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.Put(context.Background(), "art_negative0000001", strings.NewReader("x"), -1); err == nil {
		t.Fatal("negative limit accepted")
	}
}

func TestMemoryLenTracksCleanup(t *testing.T) {
	m := NewMemory()
	if _, err := m.Put(context.Background(), "art_len0000000001", strings.NewReader("x"), 8); err != nil {
		t.Fatal(err)
	}
	if m.Len() != 1 {
		t.Fatalf("len = %d", m.Len())
	}
	if err := m.Delete(context.Background(), "art_len0000000001"); err != nil || m.Len() != 0 {
		t.Fatalf("len after delete = %d, %v", m.Len(), err)
	}
}
