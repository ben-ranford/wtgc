package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const managedMarker = ".wtgc-managed-output"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("releasepack", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	epoch := flags.Int64("epoch", 0, "source date epoch for archive entries")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		return errors.New("usage: releasepack [--epoch unix-seconds] <input-dir> <output-archive>")
	}
	if *epoch <= 0 {
		return errors.New("--epoch must be a positive Unix timestamp")
	}

	inputDir := flags.Arg(0)
	outputArchive := flags.Arg(1)
	info, err := os.Stat(inputDir)
	if err != nil {
		return fmt.Errorf("stat input directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("input is not a directory: %s", inputDir)
	}
	inputRoot, err := os.OpenRoot(inputDir)
	if err != nil {
		return fmt.Errorf("open input root: %w", err)
	}
	defer inputRoot.Close()

	outputRoot, outputName, err := outputTarget(outputArchive)
	if err != nil {
		return err
	}
	defer outputRoot.Close()

	entries, err := collect(inputDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("input directory has no releasable files: %s", inputDir)
	}

	timestamp := time.Unix(*epoch, 0).UTC()
	switch {
	case strings.HasSuffix(outputArchive, ".tar.gz"):
		return writeTarGz(outputRoot, outputName, inputRoot, entries, timestamp)
	case strings.HasSuffix(outputArchive, ".zip"):
		return writeZip(outputRoot, outputName, inputRoot, entries, timestamp)
	default:
		return fmt.Errorf("unsupported archive extension: %s", outputArchive)
	}
}

type entry struct {
	source string
	name   string
	mode   fs.FileMode
	size   int64
}

func outputTarget(path string) (*os.Root, string, error) {
	dir, name := filepath.Split(path)
	if name == "" {
		return nil, "", fmt.Errorf("output archive has no filename: %s", path)
	}
	if dir == "" {
		dir = "."
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", fmt.Errorf("open output root: %w", err)
	}
	return root, name, nil
}

func collect(inputDir string) ([]entry, error) {
	var entries []entry
	rootName := filepath.Base(inputDir)
	err := filepath.WalkDir(inputDir, func(path string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if dirEntry.IsDir() {
			return nil
		}
		if dirEntry.Name() == managedMarker {
			return nil
		}
		info, err := dirEntry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(inputDir, path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{
			source: rel,
			name:   filepath.ToSlash(filepath.Join(rootName, rel)),
			mode:   normalizedMode(info.Mode()),
			size:   info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect archive entries: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})
	return entries, nil
}

func normalizedMode(mode fs.FileMode) fs.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func writeTarGz(outputRoot *os.Root, outputName string, inputRoot *os.Root, entries []entry, timestamp time.Time) error {
	out, err := outputRoot.Create(outputName)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer out.Close()

	gz, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	gz.ModTime = timestamp
	tw := tar.NewWriter(gz)

	for _, entry := range entries {
		header := &tar.Header{
			Typeflag:   tar.TypeReg,
			Name:       entry.name,
			Mode:       int64(entry.mode.Perm()),
			Size:       entry.size,
			ModTime:    timestamp,
			AccessTime: timestamp,
			ChangeTime: timestamp,
			Uid:        0,
			Gid:        0,
			Format:     tar.FormatPAX,
		}
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write tar header %s: %w", entry.name, err)
		}
		if err := copyFile(tw, inputRoot, entry.source); err != nil {
			return fmt.Errorf("write tar entry %s: %w", entry.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar archive: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip archive: %w", err)
	}
	return nil
}

func writeZip(outputRoot *os.Root, outputName string, inputRoot *os.Root, entries []entry, timestamp time.Time) error {
	out, err := outputRoot.Create(outputName)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	for _, entry := range entries {
		header := &zip.FileHeader{
			Name:   entry.name,
			Method: zip.Deflate,
		}
		header.SetMode(entry.mode)
		header.SetModTime(timestamp)
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("write zip header %s: %w", entry.name, err)
		}
		if err := copyFile(writer, inputRoot, entry.source); err != nil {
			return fmt.Errorf("write zip entry %s: %w", entry.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zip archive: %w", err)
	}
	return nil
}

func copyFile(writer io.Writer, root *os.Root, path string) error {
	in, err := root.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(writer, in)
	return err
}
