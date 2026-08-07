package testrunner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunInit_ScaffoldsFiles(t *testing.T) {
	dir := t.TempDir()
	if err := RunInit(InitOptions{Dir: dir}); err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}
	for _, name := range []string{"login.yml", "scrape.yml", "android.yml"} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestRunInit_ScaffoldedSuitesValidate(t *testing.T) {
	dir := t.TempDir()
	if err := RunInit(InitOptions{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"login.yml", "scrape.yml", "android.yml"} {
		path := filepath.Join(dir, name)
		errs, err := ValidateSuiteYAMLFile(path)
		if err != nil {
			t.Fatalf("%s: parse error: %v", name, err)
		}
		if len(errs) != 0 {
			t.Fatalf("%s: %d validation errors: %v", name, len(errs), errs)
		}
	}
}

func TestRunInit_RefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := RunInit(InitOptions{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	err := RunInit(InitOptions{Dir: dir})
	if !errors.Is(err, ErrInitExists) {
		t.Fatalf("err = %v, want ErrInitExists", err)
	}
}

func TestRunInit_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := RunInit(InitOptions{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "login.yml")
	if err := os.WriteFile(path, []byte("overwritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RunInit(InitOptions{Dir: dir, Force: true}); err != nil {
		t.Fatalf("force overwrite should succeed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "overwritten\n" {
		t.Error("file was not overwritten")
	}
}
