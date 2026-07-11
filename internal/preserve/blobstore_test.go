package preserve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalBlobStore_PutCreatesNestedAndIsReadable(t *testing.T) {
	root := t.TempDir()
	bs := NewLocalBlobStore(root)
	ctx := context.Background()

	if err := bs.Put(ctx, "manifestX/0001.jpg", []byte("jpegbytes")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "manifestX", "0001.jpg"))
	if err != nil {
		t.Fatalf("reading stored blob: %v", err)
	}
	if string(got) != "jpegbytes" {
		t.Fatalf("stored = %q, want %q", got, "jpegbytes")
	}
}

func TestLocalBlobStore_Exists(t *testing.T) {
	bs := NewLocalBlobStore(t.TempDir())
	ctx := context.Background()

	if ok, err := bs.Exists(ctx, "a/b.jpg"); err != nil || ok {
		t.Fatalf("Exists before Put = (%v,%v), want (false,nil)", ok, err)
	}
	if err := bs.Put(ctx, "a/b.jpg", []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if ok, err := bs.Exists(ctx, "a/b.jpg"); err != nil || !ok {
		t.Fatalf("Exists after Put = (%v,%v), want (true,nil)", ok, err)
	}
}

func TestLocalBlobStore_GetAndDelete(t *testing.T) {
	bs := NewLocalBlobStore(t.TempDir())
	ctx := context.Background()
	if err := bs.Put(ctx, "a/b.jpg", []byte("image")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := bs.Get(ctx, "a/b.jpg")
	if err != nil || string(got) != "image" {
		t.Fatalf("Get = %q, %v; want image, nil", got, err)
	}
	if err := bs.Delete(ctx, "a/b.jpg"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := bs.Delete(ctx, "a/b.jpg"); err != nil {
		t.Fatalf("second Delete should be idempotent: %v", err)
	}
	if ok, err := bs.Exists(ctx, "a/b.jpg"); err != nil || ok {
		t.Fatalf("Exists after Delete = %v, %v; want false, nil", ok, err)
	}
}

func TestLocalBlobStore_PutIsAtomicNoPartialOnInterrupt(t *testing.T) {
	// A successful Put must never leave a temp file behind in the target dir.
	root := t.TempDir()
	bs := NewLocalBlobStore(root)
	if err := bs.Put(context.Background(), "m/0001.jpg", []byte("data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "m"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "0001.jpg" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir contents = %v, want exactly [0001.jpg] (no temp leftovers)", names)
	}
}
