package hooks

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/fsys"
)

func TestMergeUserPiIntoWorkDirSkipsMissingUserPi(t *testing.T) {
	fs := fsys.NewFake()
	fs.Dirs["/work"] = true
	if err := mergeUserPiIntoWorkDir(fs, "/no/such/.pi", "/work"); err != nil {
		t.Fatal(err)
	}
}

func TestMergeUserPiIntoWorkDirCopiesSettingsAndPreservesGCHooks(t *testing.T) {
	fs := fakeWorkCity(t)
	fs.Dirs["/home/.pi"] = true
	fs.Dirs["/home/.pi/agent"] = true
	fs.Files["/home/.pi/agent/settings.json"] = []byte(`{
  "defaultProvider": "ollama",
  "ollama": { "baseUrl": "http://192.168.1.78:11434" }
}
`)

	if err := Install(fs, "/city", "/work", []string{"pi"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	hooksPath := "/work/.pi/extensions/gc-hooks.js"
	embedded := string(fs.Files[hooksPath])

	if err := mergeUserPiIntoWorkDir(fs, "/home/.pi", "/work"); err != nil {
		t.Fatalf("mergeUserPiIntoWorkDir: %v", err)
	}
	if string(fs.Files[hooksPath]) != embedded {
		t.Fatal("gc-hooks.js must not be replaced by user pi merge")
	}
	got, ok := fs.Files["/work/.pi/agent/settings.json"]
	if !ok {
		t.Fatal("expected /work/.pi/agent/settings.json after merge")
	}
	s := string(got)
	if !strings.Contains(s, `"defaultProvider": "ollama"`) || !strings.Contains(s, "192.168.1.78") {
		t.Fatalf("settings merge unexpected: %s", s)
	}
}

func TestMergeUserPiIntoWorkDirSkipsGcHooksFromUser(t *testing.T) {
	fs := fakeWorkCity(t)
	fs.Dirs["/home/.pi"] = true
	fs.Dirs["/home/.pi/extensions"] = true
	fs.Files["/home/.pi/extensions/gc-hooks.js"] = []byte(`/* user should not win */`)

	if err := Install(fs, "/city", "/work", []string{"pi"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	want := string(fs.Files["/work/.pi/extensions/gc-hooks.js"])
	if err := mergeUserPiIntoWorkDir(fs, "/home/.pi", "/work"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := string(fs.Files["/work/.pi/extensions/gc-hooks.js"]); got != want {
		t.Fatalf("user gc-hooks.js must be ignored; got len %d want len %d", len(got), len(want))
	}
}

func TestMergeUserPiIntoWorkDirSkipsSessionsTree(t *testing.T) {
	fs := fakeWorkCity(t)
	fs.Dirs["/home/.pi"] = true
	fs.Dirs["/home/.pi/agent"] = true
	fs.Dirs["/home/.pi/agent/sessions"] = true
	fs.Dirs["/home/.pi/agent/sessions/sub"] = true
	fs.Files["/home/.pi/agent/sessions/sub/x.jsonl"] = []byte(`{}`)

	if err := Install(fs, "/city", "/work", []string{"pi"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mergeUserPiIntoWorkDir(fs, "/home/.pi", "/work"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, ok := fs.Files["/work/.pi/agent/sessions/sub/x.jsonl"]; ok {
		t.Fatal("sessions tree should not be copied to work dir")
	}
}

func TestMergeUserPiJSONDeepMerge(t *testing.T) {
	fs := fakeWorkCity(t)
	fs.Dirs["/home/.pi"] = true
	fs.Dirs["/home/.pi/agent"] = true
	fs.Files["/home/.pi/agent/settings.json"] = []byte(`{"ollama":{"baseUrl":"http://home:11434"},"packages":["npm:a"]}` + "\n")

	if err := Install(fs, "/city", "/work", []string{"pi"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	fs.Files["/work/.pi/agent/settings.json"] = []byte(`{"defaultProvider":"ollama","ollama":{"timeout":99}}` + "\n")

	if err := mergeUserPiIntoWorkDir(fs, "/home/.pi", "/work"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	got := string(fs.Files["/work/.pi/agent/settings.json"])
	if !strings.Contains(got, `"defaultProvider": "ollama"`) ||
		!strings.Contains(got, "http://home:11434") ||
		!strings.Contains(got, `"timeout": 99`) ||
		!strings.Contains(got, "npm:a") {
		t.Fatalf("deep merge lost keys: %s", got)
	}
}

func TestResetAndInstallPiMergesFromHomePi(t *testing.T) {
	t.Setenv("HOME", "/fakehome")
	fs := fakeWorkCity(t)
	fs.Dirs["/fakehome"] = true
	fs.Dirs["/fakehome/.pi"] = true
	fs.Dirs["/fakehome/.pi/agent"] = true
	fs.Files["/fakehome/.pi/agent/settings.json"] = []byte(`{"ollama":{"baseUrl":"http://ollama.example:11434"}}` + "\n")

	if err := ResetAndInstallWithResolver(fs, "/city", "/work", []string{"pi"}, nil); err != nil {
		t.Fatalf("ResetAndInstallWithResolver: %v", err)
	}
	settings, ok := fs.Files["/work/.pi/agent/settings.json"]
	if !ok {
		t.Fatal("expected merged settings under work .pi/agent")
	}
	s := string(settings)
	if !strings.Contains(s, "ollama.example") {
		t.Fatalf("settings should contain home ollama url; got: %s", s)
	}
	if !strings.Contains(string(fs.Files["/work/.pi/extensions/gc-hooks.js"]), "Gas City hooks for Pi") {
		t.Fatal("embedded gc-hooks should remain after reset+merge")
	}
}

func fakeWorkCity(t *testing.T) *fsys.Fake {
	t.Helper()
	fs := fsys.NewFake()
	fs.Dirs["/city"] = true
	fs.Dirs["/work"] = true
	return fs
}
