package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/fsys"
)

// mergeUserPiIntoWorkDir copies and merges files from userPiRoot (typically
// $HOME/.pi) into workDir/.pi so Pi sees project-local settings, models, and
// other config when the agent cwd is workDir. Gas City–managed
// .pi/extensions/gc-hooks.js is never read from userPiRoot (the embedded
// install remains authoritative). The agent/sessions tree under userPiRoot is
// skipped to avoid copying large session logs.
//
// JSON files are deep-merged with keys from the user tree overlaying existing
// keys in workDir so ~/.pi/agent/settings.json supplements or overrides
// project files without dropping unrelated keys already under workDir/.pi.
func mergeUserPiIntoWorkDir(fs fsys.FS, userPiRoot, workDir string) error {
	userPiRoot = filepath.Clean(userPiRoot)
	workDir = filepath.Clean(workDir)
	if userPiRoot == "" || workDir == "" {
		return nil
	}
	if _, err := fs.Stat(userPiRoot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat user pi dir %s: %w", userPiRoot, err)
	}
	return walkMergeUserPi(fs, userPiRoot, userPiRoot, workDir)
}

func walkMergeUserPi(fs fsys.FS, userPiRoot, current, workDir string) error {
	entries, err := fs.ReadDir(current)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", current, err)
	}
	for _, e := range entries {
		name := e.Name()
		abs := filepath.Join(current, name)
		rel, err := filepath.Rel(userPiRoot, abs)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}
		relSlash := filepath.ToSlash(rel)
		if shouldSkipPiMergePath(relSlash) {
			continue
		}
		if e.IsDir() {
			if err := walkMergeUserPi(fs, userPiRoot, abs, workDir); err != nil {
				return err
			}
			continue
		}
		dst := filepath.Join(workDir, ".pi", rel)
		if strings.EqualFold(filepath.Ext(name), ".json") {
			if err := mergeUserPiJSONFile(fs, abs, dst); err != nil {
				return fmt.Errorf("merge pi json %s -> %s: %w", abs, dst, err)
			}
			continue
		}
		if err := copyUserPiNonJSONFile(fs, abs, dst); err != nil {
			return fmt.Errorf("copy pi file %s -> %s: %w", abs, dst, err)
		}
	}
	return nil
}

func shouldSkipPiMergePath(relSlash string) bool {
	if relSlash == "extensions/gc-hooks.js" {
		return true
	}
	if relSlash == "agent/sessions" || strings.HasPrefix(relSlash, "agent/sessions/") {
		return true
	}
	return false
}

func mergeUserPiJSONFile(fs fsys.FS, srcPath, dstPath string) error {
	src, err := fs.ReadFile(srcPath)
	if err != nil {
		return err
	}
	dst, err := fs.ReadFile(dstPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	merged, err := mergePiJSONObjectBytes(dst, src)
	if err != nil {
		return err
	}
	return writePiMergedFile(fs, dstPath, merged, piFilePerm(dstPath))
}

func copyUserPiNonJSONFile(fs fsys.FS, srcPath, dstPath string) error {
	data, err := fs.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return writePiMergedFile(fs, dstPath, data, piFilePerm(dstPath))
}

func piFilePerm(dstPath string) os.FileMode {
	if strings.Contains(strings.ToLower(filepath.Base(dstPath)), "auth") {
		return 0o600
	}
	return 0o644
}

func writePiMergedFile(fs fsys.FS, dstPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dstPath)
	if err := fs.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := fs.WriteFile(dstPath, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", dstPath, err)
	}
	return nil
}

// mergePiJSONObjectBytes returns JSON bytes where keys from home overlay keys
// in dest (home wins at each object level; nested maps are merged recursively).
// Non-object values from home replace dest. Arrays from home replace dest entirely.
func mergePiJSONObjectBytes(dest, home []byte) ([]byte, error) {
	home = bytes.TrimSpace(home)
	if len(home) == 0 {
		return dest, nil
	}
	homeMap, err := parseJSONObject(home, "home")
	if err != nil {
		return nil, err
	}
	if homeMap == nil {
		return dest, nil
	}
	dest = bytes.TrimSpace(dest)
	if len(dest) == 0 {
		return formatMergedJSON(homeMap)
	}
	destMap, err := parseJSONObject(dest, "dest")
	if err != nil {
		return nil, err
	}
	if destMap == nil {
		return formatMergedJSON(homeMap)
	}
	merged := mergeJSONObjectHomeWins(destMap, homeMap)
	return formatMergedJSON(merged)
}

func parseJSONObject(raw []byte, label string) (map[string]any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("unmarshal %s json: %w", label, err)
	}
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s json: expected object at root", label)
	}
	return m, nil
}

func formatMergedJSON(m map[string]any) ([]byte, error) {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func mergeJSONObjectHomeWins(dest, home map[string]any) map[string]any {
	out := make(map[string]any, len(dest)+len(home))
	for k, v := range dest {
		out[k] = v
	}
	for k, v := range home {
		if dv, ok := out[k]; ok {
			if dm, dOk := dv.(map[string]any); dOk {
				if hm, hOk := v.(map[string]any); hOk {
					out[k] = mergeJSONObjectHomeWins(dm, hm)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}
