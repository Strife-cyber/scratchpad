package testrunner

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed templates/*.yml
var initTemplates embed.FS

type InitOptions struct {
	Dir   string
	Force bool
}

// scaffoldTargets lists the templates and their destination filenames. The
// "web" target is implied by the login and scrape suites.
var scaffoldTargets = []struct {
	Template string
	Dest     string
}{
	{Template: "templates/login.yml", Dest: "login.yml"},
	{Template: "templates/scrape.yml", Dest: "scrape.yml"},
	{Template: "templates/android.yml", Dest: "android.yml"},
}

// RunInit scaffolds example suites (login/scrape/android) into the given
// directory. It refuses to overwrite existing files unless Force is set.
func RunInit(opts InitOptions) error {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	if !opts.Force {
		for _, t := range scaffoldTargets {
			dest := filepath.Join(dir, t.Dest)
			if _, err := os.Stat(dest); err == nil {
				return fmt.Errorf("%w: %s", ErrInitExists, dest)
			}
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	for _, t := range scaffoldTargets {
		data, err := fs.ReadFile(initTemplates, t.Template)
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, t.Dest)
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "wrote %s\n", dest)
	}
	fmt.Fprintln(os.Stdout, "run: scratchpad-cli run -i <file> --dry-run   (validate only)")
	return nil
}
