package plugin

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"papermgr/internal/config"
	"papermgr/internal/logging"
)

func setupPlugin(t *testing.T, name, body string, timeout int) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_STATE_HOME", dir)
	pdir := filepath.Join(dir, "papermgr", "plugins", name)
	if err := os.MkdirAll(filepath.Join(pdir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name = \"" + name + "\"\nversion = \"0.1.0\"\nprotocol = \"papermgr-plugin/v1\"\ncapabilities = [\"metadata\"]\nentry = [\"./bin/run\"]\npriority = 10\ntimeout_seconds = " + itoa(timeout) + "\nrequires = []\n"
	if err := os.WriteFile(filepath.Join(pdir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(pdir, "bin", "run")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return pdir
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	return out
}

func manager() *Manager {
	return NewManager("/tmp/lib", config.DefaultLibrary(), config.Global{LogDir: filepath.Join(os.TempDir(), "papermgr-test-log")}, logging.New(filepath.Join(os.TempDir(), "papermgr-test-log")))
}

func TestMetadataPlugin(t *testing.T) {
	setupPlugin(t, "fake", `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"init"'*) echo '{"jsonrpc":"2.0","id":1,"result":{"ok":true,"protocol":"papermgr-plugin/v1"}}' ;;
    *'"metadata.fetch"'*) echo '{"jsonrpc":"2.0","id":2,"result":{"candidates":[{"fields":{"title":"Attention Is All You Need","year":2017},"confidence":0.9,"source":"fake"}]}}' ;;
  esac
done
`, 5)
	m := manager()
	cands := m.FetchMetadata("/tmp/x.pdf", "abc", map[string]string{})
	if len(cands) != 1 || cands[0].Title != "Attention Is All You Need" || cands[0].Confidence != 0.9 {
		t.Fatalf("candidates = %+v", cands)
	}
}

func TestPluginCrashDoesNotBlock(t *testing.T) {
	setupPlugin(t, "crash", "#!/bin/sh\nexit 1\n", 2)
	m := manager()
	if got := m.FetchMetadata("/tmp/x.pdf", "abc", nil); len(got) != 0 {
		t.Fatalf("expected no candidates, got %+v", got)
	}
}

func TestPluginTimeout(t *testing.T) {
	setupPlugin(t, "slow", "#!/bin/sh\nsleep 5\n", 1)
	m := manager()
	start := nowUnix()
	if got := m.FetchMetadata("/tmp/x.pdf", "abc", nil); len(got) != 0 {
		t.Fatalf("expected no candidates, got %+v", got)
	}
	if nowUnix()-start > 4 {
		t.Fatalf("timeout took too long")
	}
}

func TestProtocolMismatch(t *testing.T) {
	setupPlugin(t, "oldproto", `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"init"'*) echo '{"jsonrpc":"2.0","id":1,"result":{"ok":true,"protocol":"papermgr-plugin/v0"}}' ;;
  esac
done
`, 2)
	m := manager()
	if got := m.FetchMetadata("/tmp/x.pdf", "abc", nil); len(got) != 0 {
		t.Fatalf("expected rejection, got %+v", got)
	}
	infos := m.List(true)
	if len(infos) != 1 || infos[0].Healthy {
		t.Errorf("protocol mismatch should be unhealthy: %+v", infos)
	}
}

func nowUnix() int64 { return time.Now().Unix() }
