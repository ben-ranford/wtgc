// Command featureflag validates the repository's declared, temporary feature flags.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var flagName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

type declarationFile struct {
	Flags []declaration `json:"flags"`
}
type declaration struct {
	Name             string   `json:"name"`
	Owner            string   `json:"owner"`
	Issue            string   `json:"issue"`
	RemovalCondition string   `json:"removal_condition"`
	References       []string `json:"references"`
}

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("featureflag", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("file", ".ci/feature-flags.json", "feature-flag declaration file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: featureflag [--file path]")
		return 2
	}
	if err := validate(*path); err != nil {
		fmt.Fprintf(stderr, "feature-flag contract failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(stderr, "Feature-flag contract valid.")
	return 0
}

func validate(path string) error {
	return validateIn(path, ".")
}

func validateIn(path, repositoryPath string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	defer root.Close()
	b, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	var doc declarationFile
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("parse %q: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("declaration file contains multiple JSON values")
	}
	repository, err := os.OpenRoot(repositoryPath)
	if err != nil {
		return fmt.Errorf("open repository root: %w", err)
	}
	defer repository.Close()
	seen := map[string]bool{}
	for _, item := range doc.Flags {
		if !flagName.MatchString(item.Name) {
			return fmt.Errorf("invalid flag name %q", item.Name)
		}
		if seen[item.Name] {
			return fmt.Errorf("duplicate declaration %q", item.Name)
		}
		seen[item.Name] = true
		if strings.TrimSpace(item.Owner) == "" || strings.TrimSpace(item.Issue) == "" || strings.TrimSpace(item.RemovalCondition) == "" {
			return fmt.Errorf("%q must declare owner, issue, and removal_condition", item.Name)
		}
		if len(item.References) == 0 {
			return fmt.Errorf("%q is stale: no source references declared", item.Name)
		}
		found := false
		for _, ref := range item.References {
			if filepath.IsAbs(ref) || strings.Contains(ref, "..") {
				return fmt.Errorf("%q has unsafe reference %q", item.Name, ref)
			}
			source, readErr := repository.ReadFile(ref)
			if readErr != nil {
				return fmt.Errorf("%q is stale: read %q: %w", item.Name, ref, readErr)
			}
			if strings.Contains(string(source), item.Name) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%q is stale: its name is absent from declared source references", item.Name)
		}
	}
	return nil
}
