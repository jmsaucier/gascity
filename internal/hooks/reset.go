package hooks

import (
	"bytes"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/bootstrap/packs/core"
	"github.com/gastownhall/gascity/internal/fsys"
)

// ResetAndInstallWithResolver removes Gas City–managed hook artifacts for the
// listed providers (same resolution rules as InstallWithResolver), then runs
// InstallWithResolver so fresh embedded content is written.
//
// Overlay-backed providers (codex, gemini, opencode, copilot, cursor, pi, omp):
// deletes each file under workDir that the pack overlay would install.
//
// Claude: deletes only cityDir/.gc/settings.json (the GC-projected runtime file).
// User-authored .claude/settings.json and hooks/claude.json are not removed.
//
// When any resolved provider family is "pi", the installed
// .pi/extensions/gc-hooks.js must match the embedded core overlay byte-for-byte
// after reinstall (Pi has a single overlay file; this catches drift or partial writes).
//
// For pi, after the overlay install, files under $HOME/.pi are merged into
// workDir/.pi (see mergeUserPiIntoWorkDir) so project-local Pi config matches
// the user's global ~/.pi tree except for Gas City–managed gc-hooks.js.
func ResetAndInstallWithResolver(fs fsys.FS, cityDir, workDir string, providers []string, resolve FamilyResolver) error {
	if err := removeManagedHookFiles(fs, cityDir, workDir, providers, resolve); err != nil {
		return err
	}
	if err := InstallWithResolver(fs, cityDir, workDir, providers, resolve); err != nil {
		return err
	}
	if err := mergeUserPiAfterPiHookReset(fs, workDir, providers, resolve); err != nil {
		return err
	}
	piSeen := false
	for _, p := range providers {
		if resolveFamily(resolve, p) != "pi" || piSeen {
			continue
		}
		piSeen = true
		if err := verifyPiOverlayInstalled(fs, workDir); err != nil {
			return err
		}
	}
	return nil
}

func mergeUserPiAfterPiHookReset(fs fsys.FS, workDir string, providers []string, resolve FamilyResolver) error {
	piSeen := false
	for _, p := range providers {
		if resolveFamily(resolve, p) != "pi" || piSeen {
			continue
		}
		piSeen = true
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("pi hooks merge: user home: %w", err)
		}
		userPi := filepath.Join(home, ".pi")
		if err := mergeUserPiIntoWorkDir(fs, userPi, workDir); err != nil {
			return fmt.Errorf("pi hooks merge: %w", err)
		}
		return nil
	}
	return nil
}

// verifyPiOverlayInstalled checks workDir/.pi/extensions/gc-hooks.js matches
// overlay/per-provider/pi in the embedded core pack (same source as installOverlayManaged).
func verifyPiOverlayInstalled(fs fsys.FS, workDir string) error {
	if strings.TrimSpace(workDir) == "" {
		return fmt.Errorf("pi hooks: workDir is empty")
	}
	embedPath := path.Join("overlay", "per-provider", "pi", ".pi", "extensions", "gc-hooks.js")
	want, err := iofs.ReadFile(core.PackFS, embedPath)
	if err != nil {
		return fmt.Errorf("pi hooks: read embedded overlay %s: %w", embedPath, err)
	}
	dst := filepath.Join(workDir, ".pi", "extensions", "gc-hooks.js")
	got, err := fs.ReadFile(dst)
	if err != nil {
		return fmt.Errorf("pi hooks: read installed %s: %w", dst, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("pi hooks: installed %s does not match embedded overlay (got %d bytes, want %d bytes)",
			dst, len(got), len(want))
	}
	return nil
}

func removeManagedHookFiles(fs fsys.FS, cityDir, workDir string, providers []string, resolve FamilyResolver) error {
	if err := ValidateWithResolver(providers, resolve); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, p := range providers {
		family := resolveFamily(resolve, p)
		if seen[family] {
			continue
		}
		seen[family] = true
		switch family {
		case "claude":
			if err := removeClaudeManagedRuntime(fs, cityDir); err != nil {
				return fmt.Errorf("removing claude hooks: %w", err)
			}
		case "codex", "gemini", "opencode", "copilot", "cursor", "pi", "omp":
			if err := removeOverlayManagedFiles(fs, workDir, family); err != nil {
				return fmt.Errorf("removing %s hooks: %w", family, err)
			}
		default:
			return fmt.Errorf("unsupported hook provider %q", family)
		}
	}
	return nil
}

func removeClaudeManagedRuntime(fs fsys.FS, cityDir string) error {
	if strings.TrimSpace(cityDir) == "" {
		return nil
	}
	dst := filepath.Join(cityDir, ".gc", "settings.json")
	if err := fs.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", dst, err)
	}
	return nil
}

func removeOverlayManagedFiles(fs fsys.FS, workDir, provider string) error {
	if strings.TrimSpace(workDir) == "" {
		return nil
	}
	base := path.Join("overlay", "per-provider", provider)
	if _, err := iofs.Stat(core.PackFS, base); err != nil {
		return fmt.Errorf("provider overlay %q: %w", provider, err)
	}
	return iofs.WalkDir(core.PackFS, base, func(name string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == base || d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(name, base+"/")
		dst := filepath.Join(workDir, filepath.FromSlash(rel))
		if rmErr := fs.Remove(dst); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return fmt.Errorf("%s: %w", dst, rmErr)
		}
		return nil
	})
}
