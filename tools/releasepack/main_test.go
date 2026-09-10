package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestReleasePackCoversCollectionAndWriterFailures(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	if err := os.Mkdir(input, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "plain"), []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := collect(input)
	if err != nil || len(entries) != 1 || entries[0].name != "input/plain" {
		t.Fatalf("collect=%#v err=%v", entries, err)
	}
	if _, _, err := outputTarget(filepath.Join(root, "missing", "archive.zip")); err == nil {
		t.Fatal("accepted missing output directory")
	}
	localRoot, localName, err := outputTarget("archive.zip")
	if err != nil || localName != "archive.zip" {
		t.Fatalf("relative output target = (%q, %v)", localName, err)
	}
	localRoot.Close()
	if err := os.Symlink(filepath.Join(input, "plain"), filepath.Join(input, "linked")); err == nil {
		entries, err = collect(input)
		if err != nil || len(entries) != 1 {
			t.Fatalf("collect symlink=%#v err=%v", entries, err)
		}
	}
	inputRoot, err := os.OpenRoot(input)
	if err != nil {
		t.Fatal(err)
	}
	defer inputRoot.Close()
	outputRoot, _, err := outputTarget(filepath.Join(root, "archive.zip"))
	if err != nil {
		t.Fatal(err)
	}
	outputRoot.Close()
	when := time.Unix(1700000000, 0).UTC()
	if err := writeZip(outputRoot, "closed.zip", inputRoot, entries, when); err == nil {
		t.Fatal("writeZip accepted closed output root")
	}
	if err := writeTarGz(outputRoot, "closed.tar.gz", inputRoot, entries, when); err == nil {
		t.Fatal("writeTarGz accepted closed output root")
	}
	if err := copyFile(io.Discard, inputRoot, "missing"); err == nil {
		t.Fatal("copyFile accepted missing file")
	}
	if normalizedMode(0o755) != 0o755 || normalizedMode(0o644) != 0o644 {
		t.Fatal("normalizedMode changed expected modes")
	}
}

func TestRunCoversOutputAndArchiveFailures(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	if err := os.Mkdir(input, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "wtgc"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--epoch", "1700000000", input, filepath.Join(root, "missing", "out.zip")}); err == nil {
		t.Fatal("accepted missing archive output root")
	}
	if err := run([]string{"--epoch", "1700000000", input, filepath.Join(root, "out.tar.gz")}); err != nil {
		t.Fatalf("tar archive failed: %v", err)
	}
	if err := run([]string{"--epoch", "1700000000", input, filepath.Join(root, "out.zip")}); err != nil {
		t.Fatalf("zip archive failed: %v", err)
	}
	if err := run([]string{"--epoch", "not-a-time", input, filepath.Join(root, "out.zip")}); err == nil {
		t.Fatal("accepted invalid epoch flag")
	}
	if _, err := collect(filepath.Join(root, "missing")); err == nil {
		t.Fatal("collect accepted a missing directory")
	}
	inputRoot, err := os.OpenRoot(input)
	if err != nil {
		t.Fatal(err)
	}
	defer inputRoot.Close()
	outputRoot, name, err := outputTarget(filepath.Join(root, "manual.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer outputRoot.Close()
	bad := []entry{{source: "missing", name: "input/missing", mode: 0o644, size: 1}}
	when := time.Unix(1700000000, 0).UTC()
	if err := writeTarGz(outputRoot, "missing.tar.gz", inputRoot, bad, when); err == nil {
		t.Fatal("tar writer accepted missing input entry")
	}
	if err := writeZip(outputRoot, name, inputRoot, bad, when); err == nil {
		t.Fatal("zip writer accepted missing input entry")
	}
	good := []entry{{source: "wtgc", name: "input/wtgc", mode: 0o755, size: int64(len("binary"))}}
	if err := writeTarGzTo(failingWriter{}, inputRoot, good, when); err == nil {
		t.Fatal("tar writer accepted failing output")
	}
	if err := writeZipTo(failingWriter{}, inputRoot, good, when); err == nil {
		t.Fatal("zip writer accepted failing output")
	}
	large := pseudoRandomBytes(128 << 10)
	if err := os.WriteFile(filepath.Join(input, "large"), large, 0o644); err != nil {
		t.Fatal(err)
	}
	largeEntry := []entry{{source: "large", name: "input/large", mode: 0o644, size: int64(len(large))}}
	if err := writeTarGzTo(&limitedWriter{limit: 2}, inputRoot, largeEntry, when); err == nil {
		t.Fatal("tar writer accepted delayed write failure")
	}
	if err := writeZipTo(&limitedWriter{limit: 2}, inputRoot, largeEntry, when); err == nil {
		t.Fatal("zip writer accepted delayed write failure")
	}
	tarWrites := &countingWriter{}
	if err := writeTarGzTo(tarWrites, inputRoot, largeEntry, when); err != nil {
		t.Fatalf("count tar writes: %v", err)
	}
	if err := writeTarGzTo(&limitedWriter{limit: tarWrites.writes}, inputRoot, largeEntry, when); err == nil {
		t.Fatal("tar writer accepted a final-write failure")
	}
	zipWrites := &countingWriter{}
	if err := writeZipTo(zipWrites, inputRoot, largeEntry, when); err != nil {
		t.Fatalf("count zip writes: %v", err)
	}
	if err := writeZipTo(&limitedWriter{limit: zipWrites.writes}, inputRoot, largeEntry, when); err == nil {
		t.Fatal("zip writer accepted a final-write failure")
	}
	if err := writeTarEntries(&fakeTarWriter{headerErr: errors.New("header")}, inputRoot, good, when); err == nil {
		t.Fatal("tar entries accepted header failure")
	}
	if err := writeTarEntries(&fakeTarWriter{closeErr: errors.New("close")}, inputRoot, good, when); err == nil {
		t.Fatal("tar entries accepted close failure")
	}
	if err := writeZipEntries(&fakeZipWriter{headerErr: errors.New("header")}, inputRoot, good, when); err == nil {
		t.Fatal("zip entries accepted header failure")
	}
	if err := writeZipEntries(&fakeZipWriter{closeErr: errors.New("close")}, inputRoot, good, when); err == nil {
		t.Fatal("zip entries accepted close failure")
	}
}

func TestCommandAndCollectionErrorBoundaries(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	if err := os.Mkdir(input, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "wtgc"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := execute([]string{"--epoch", "0", input, filepath.Join(root, "out.zip")}, &stderr); code != 1 || !strings.Contains(stderr.String(), "positive") {
		t.Fatalf("execute error = (%d, %q)", code, stderr.String())
	}
	stderr.Reset()
	if code := execute([]string{"--epoch", "1700000000", input, filepath.Join(root, "out.zip")}, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("execute success = (%d, %q)", code, stderr.String())
	}
	sentinel := errors.New("collection failed")
	if err := runWith([]string{"--epoch", "1700000000", input, filepath.Join(root, "other.zip")}, func(string) ([]entry, error) { return nil, sentinel }, os.OpenRoot); !errors.Is(err, sentinel) {
		t.Fatalf("runWith error = %v", err)
	}
	if err := runWith([]string{"--epoch", "1700000000", input, filepath.Join(root, "another.zip")}, collect, func(string) (*os.Root, error) { return nil, sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("runWith open root error = %v", err)
	}
	info, err := os.Stat(filepath.Join(input, "wtgc"))
	if err != nil {
		t.Fatal(err)
	}
	if item, include, err := collectEntry(input, filepath.Join(input, "wtgc"), fakeDirEntry{name: "wtgc", info: info}, nil); err != nil || !include || item.source != "wtgc" {
		t.Fatalf("collect regular = (%+v, %t, %v)", item, include, err)
	}
	if _, include, err := collectEntry(input, input, fakeDirEntry{name: "input", dir: true}, nil); err != nil || include {
		t.Fatalf("collect directory = (%t, %v)", include, err)
	}
	if _, include, err := collectEntry(input, filepath.Join(input, managedMarker), fakeDirEntry{name: managedMarker}, nil); err != nil || include {
		t.Fatalf("collect marker = (%t, %v)", include, err)
	}
	if _, _, err := collectEntry(input, filepath.Join(input, "bad"), fakeDirEntry{name: "bad", infoErr: sentinel}, nil); !errors.Is(err, sentinel) {
		t.Fatalf("collect info error = %v", err)
	}
	if _, _, err := collectEntry(input, filepath.Join(input, "bad"), fakeDirEntry{name: "bad"}, sentinel); !errors.Is(err, sentinel) {
		t.Fatalf("collect walk error = %v", err)
	}
	if _, _, err := collectEntryWith(input, filepath.Join(input, "bad"), fakeDirEntry{name: "bad", info: info}, nil, func(string, string) (string, error) { return "", sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("collect relative error = %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("injected write failure") }

type limitedWriter struct {
	limit, writes int
}

func (writer *limitedWriter) Write(data []byte) (int, error) {
	writer.writes++
	if writer.writes >= writer.limit {
		return 0, errors.New("injected delayed write failure")
	}
	return len(data), nil
}

type countingWriter struct{ writes int }

func (writer *countingWriter) Write(data []byte) (int, error) {
	writer.writes++
	return len(data), nil
}

func pseudoRandomBytes(size int) []byte {
	data := make([]byte, size)
	seed := uint32(1)
	for index := range data {
		seed = seed*1664525 + 1013904223
		data[index] = byte(seed >> 24)
	}
	return data
}

type fakeTarWriter struct {
	headerErr, closeErr error
}

func (writer *fakeTarWriter) Write(data []byte) (int, error) { return len(data), nil }
func (writer *fakeTarWriter) WriteHeader(*tar.Header) error  { return writer.headerErr }
func (writer *fakeTarWriter) Close() error                   { return writer.closeErr }

type fakeZipWriter struct {
	headerErr, closeErr error
}

func (writer *fakeZipWriter) CreateHeader(*zip.FileHeader) (io.Writer, error) {
	if writer.headerErr != nil {
		return nil, writer.headerErr
	}
	return io.Discard, nil
}
func (writer *fakeZipWriter) Close() error { return writer.closeErr }

type fakeDirEntry struct {
	name    string
	dir     bool
	info    fs.FileInfo
	infoErr error
}

func (entry fakeDirEntry) Name() string               { return entry.name }
func (entry fakeDirEntry) IsDir() bool                { return entry.dir }
func (entry fakeDirEntry) Type() fs.FileMode          { return 0 }
func (entry fakeDirEntry) Info() (fs.FileInfo, error) { return entry.info, entry.infoErr }

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
			if filepath.Base(want) == "wtgc" && runtime.GOOS != "windows" {
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
			if filepath.Base(want) == "wtgc" && runtime.GOOS != "windows" {
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
