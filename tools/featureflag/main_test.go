package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReportsUsageAndContractFailures(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"--unknown"}, &stderr); code != 2 {
		t.Fatalf("unknown flag exit = %d", code)
	}
	stderr.Reset()
	if code := run([]string{"--file", "missing"}, &stderr); code != 1 || !strings.Contains(stderr.String(), "contract failed") {
		t.Fatalf("missing file = (%d, %q)", code, stderr.String())
	}
}

func writeContract(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flags.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestValidateAcceptsNoFlags(t *testing.T) {
	if err := validate(writeContract(t, `{"flags":[]}`)); err != nil {
		t.Fatal(err)
	}
}
func TestValidateRejectsMalformedAndDuplicateDeclarations(t *testing.T) {
	for _, body := range []string{`{"flags":[`, `{"flags":[{"name":"bad","owner":"a","issue":"#1","removal_condition":"x","references":[]},{"name":"bad","owner":"a","issue":"#1","removal_condition":"x","references":[]}]}`} {
		if err := validate(writeContract(t, body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestValidateRejectsTrailingJSONValue(t *testing.T) {
	if err := validate(writeContract(t, `{"flags":[]} {"flags":[]}`)); err == nil {
		t.Fatal("accepted multiple JSON values")
	}
}
func TestValidateRejectsStaleDeclaration(t *testing.T) {
	if err := validate(writeContract(t, `{"flags":[{"name":"retire-me","owner":"owner","issue":"#1","removal_condition":"done","references":["go.mod"]}]}`)); err == nil {
		t.Fatal("accepted stale declaration")
	}
}

func TestValidateInAcceptsDeclaredFlagAndRejectsUnsafeReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature.go"), []byte("// launch-mode is temporary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract := filepath.Join(root, "flags.json")
	body := `{"flags":[{"name":"launch-mode","owner":"team","issue":"#42","removal_condition":"release complete","references":["feature.go"]}]}`
	if err := os.WriteFile(contract, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateIn(contract, root); err != nil {
		t.Fatalf("valid declaration rejected: %v", err)
	}
	missingAfterMatch := `{"flags":[{"name":"launch-mode","owner":"team","issue":"#42","removal_condition":"release complete","references":["feature.go","missing.go"]}]}`
	if err := os.WriteFile(contract, []byte(missingAfterMatch), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateIn(contract, root); err == nil || !strings.Contains(err.Error(), "missing.go") {
		t.Fatalf("missing reference after match err = %v", err)
	}
	unsafeAfterMatch := `{"flags":[{"name":"launch-mode","owner":"team","issue":"#42","removal_condition":"release complete","references":["feature.go","../outside.go"]}]}`
	if err := os.WriteFile(contract, []byte(unsafeAfterMatch), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateIn(contract, root); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe reference after match err = %v", err)
	}
	unsafe := `{"flags":[{"name":"launch-mode","owner":"team","issue":"#42","removal_condition":"release complete","references":["../outside.go"]}]}`
	if err := os.WriteFile(contract, []byte(unsafe), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateIn(contract, root); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe reference err = %v", err)
	}
}

func TestValidateRejectsUnknownFieldsAndIncompleteDeclarations(t *testing.T) {
	for _, body := range []string{
		`{"flags":[],"unexpected":true}`,
		`{"flags":[{"name":"ab","owner":"","issue":"#1","removal_condition":"done","references":["go.mod"]}]}`,
		`{"flags":[{"name":"A","owner":"team","issue":"#1","removal_condition":"done","references":["go.mod"]}]}`,
	} {
		if err := validate(writeContract(t, body)); err == nil {
			t.Fatalf("accepted invalid contract %s", body)
		}
	}
}

func TestRunAcceptsContractAndRejectsExtraArguments(t *testing.T) {
	root := t.TempDir()
	contract := filepath.Join(root, "flags.json")
	if err := os.WriteFile(contract, []byte(`{"flags":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"--file", contract}, &stderr); code != 0 || !strings.Contains(stderr.String(), "valid") {
		t.Fatalf("valid contract = (%d, %q)", code, stderr.String())
	}
	if code := run([]string{"--file", contract, "extra"}, &stderr); code != 2 {
		t.Fatalf("extra argument = %d", code)
	}
}

func TestValidateDeclarationCoversReferenceAndFieldFailures(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("// launch-mode\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for _, item := range []declaration{
		{Name: "a", Owner: "team", Issue: "#1", RemovalCondition: "done", References: []string{"source.go"}},
		{Name: "ab", Owner: "", Issue: "#1", RemovalCondition: "done", References: []string{"source.go"}},
		{Name: "ab", Owner: "team", Issue: "#1", RemovalCondition: "done", References: []string{"missing.go"}},
	} {
		if err := validateDeclaration(item, repository, map[string]bool{}); err == nil {
			t.Fatalf("accepted invalid declaration %#v", item)
		}
	}
	if err := validateDeclaration(declaration{Name: "launch-mode", Owner: "team", Issue: "#1", RemovalCondition: "done", References: []string{"source.go"}}, repository, map[string]bool{}); err != nil {
		t.Fatalf("valid declaration rejected: %v", err)
	}
	seen := map[string]bool{"launch-mode": true}
	if err := validateDeclaration(declaration{Name: "launch-mode", Owner: "team", Issue: "#1", RemovalCondition: "done", References: []string{"source.go"}}, repository, seen); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate declaration accepted: %v", err)
	}
	if err := validateDeclaration(declaration{Name: "retire-me", Owner: "team", Issue: "#1", RemovalCondition: "done", References: []string{"source.go"}}, repository, map[string]bool{}); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("stale declaration accepted: %v", err)
	}
}

func TestDeclarationReadersFailClosedOnInvalidRootsAndFiles(t *testing.T) {
	if _, err := readDeclarations("\x00"); err == nil {
		t.Fatal("readDeclarations accepted invalid root")
	}
	if _, err := readDeclarations("/dev/null/flags.json"); err == nil {
		t.Fatal("readDeclarations accepted a non-directory root")
	}
	if _, err := readDeclarations(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("readDeclarations accepted missing file")
	}
	contract := writeContract(t, `{"flags":[]}`)
	if err := validateIn(contract, "\x00"); err == nil {
		t.Fatal("validateIn accepted invalid repository root")
	}
}
