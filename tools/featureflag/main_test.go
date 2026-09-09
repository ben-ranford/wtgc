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
