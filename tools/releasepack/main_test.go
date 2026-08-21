package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestReleasePackWritesDeterministicArchives(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "wtgc_v1.2.3_linux_amd64")
	if err := os.Mkdir(input, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "wtgc"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "README.md"), []byte("readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, managedMarker), []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}

	firstTar := filepath.Join(root, "first.tar.gz")
	secondTar := filepath.Join(root, "second.tar.gz")
	firstZip := filepath.Join(root, "first.zip")
	secondZip := filepath.Join(root, "second.zip")
	for _, archive := range []string{firstTar, secondTar, firstZip, secondZip} {
		if err := run([]string{"--epoch", "1700000000", input, archive}); err != nil {
			t.Fatalf("releasepack %s: %v", archive, err)
		}
	}

	if hashFile(t, firstTar) != hashFile(t, secondTar) {
		t.Fatal("tar.gz archive hash changed for identical input")
	}
	if hashFile(t, firstZip) != hashFile(t, secondZip) {
		t.Fatal("zip archive hash changed for identical input")
	}
	assertTarEntry(t, firstTar, "wtgc_v1.2.3_linux_amd64/wtgc")
	assertTarEntry(t, firstTar, "wtgc_v1.2.3_linux_amd64/README.md")
	assertZipEntry(t, firstZip, "wtgc_v1.2.3_linux_amd64/wtgc")
	assertZipEntry(t, firstZip, "wtgc_v1.2.3_linux_amd64/README.md")
}

func TestReleasePackRejectsInvalidEpoch(t *testing.T) {
	input := t.TempDir()
	output := filepath.Join(t.TempDir(), "out.zip")
	if err := run([]string{"--epoch", "0", input, output}); err == nil {
		t.Fatal("run error = nil, want invalid epoch error")
	}
}

func TestReleasePackRejectsInvalidInputs(t *testing.T) {
	root := t.TempDir()
	fileInput := filepath.Join(root, "file")
	if err := os.WriteFile(fileInput, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	emptyDir := filepath.Join(root, "empty")
	if err := os.Mkdir(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nonEmptyDir := filepath.Join(root, "non-empty")
	if err := os.Mkdir(nonEmptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmptyDir, "wtgc"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing args", args: nil},
		{name: "bad flag", args: []string{"--bad"}},
		{name: "missing input", args: []string{"--epoch", "1700000000", filepath.Join(root, "missing"), filepath.Join(root, "out.zip")}},
		{name: "file input", args: []string{"--epoch", "1700000000", fileInput, filepath.Join(root, "out.zip")}},
		{name: "empty dir", args: []string{"--epoch", "1700000000", emptyDir, filepath.Join(root, "out.zip")}},
		{name: "unsupported extension", args: []string{"--epoch", "1700000000", nonEmptyDir, filepath.Join(root, "out.tgz")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := run(tt.args); err == nil {
				t.Fatal("run error = nil, want error")
			}
		})
	}
}

func TestOutputTargetRejectsMissingFilename(t *testing.T) {
	root, _, err := outputTarget(t.TempDir() + string(os.PathSeparator))
	if err == nil {
		root.Close()
		t.Fatal("outputTarget error = nil, want missing filename error")
	}
}

func hashFile(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(content)
}

func assertTarEntry(t *testing.T, path, want string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(header.Name) == managedMarker {
			t.Fatal("managed marker was included in tar archive")
		}
		if header.Name == want {
			wantMode := int64(0o644)
			if filepath.Base(want) == "wtgc" {
				wantMode = 0o755
			}
			if header.Mode != wantMode {
				t.Fatalf("tar mode = %o, want %o", header.Mode, wantMode)
			}
			return
		}
	}
	t.Fatalf("tar entry %q not found", want)
}

func assertZipEntry(t *testing.T, path, want string) {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if filepath.Base(file.Name) == managedMarker {
			t.Fatal("managed marker was included in zip archive")
		}
		if file.Name == want {
			wantMode := os.FileMode(0o644)
			if filepath.Base(want) == "wtgc" {
				wantMode = 0o755
			}
			if file.Mode().Perm() != wantMode {
				t.Fatalf("zip mode = %o, want %o", file.Mode().Perm(), wantMode)
			}
			return
		}
	}
	t.Fatalf("zip entry %q not found", want)
}
