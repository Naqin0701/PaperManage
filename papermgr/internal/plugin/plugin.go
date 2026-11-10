// Package plugin implements external process plugins speaking JSON-RPC 2.0
// over stdio. stdout carries protocol frames only; stderr is logged verbatim.
package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"papermgr/internal/config"
	"papermgr/internal/logging"
	"papermgr/internal/model"
)

// Protocol is the required protocol version.
const Protocol = "papermgr-plugin/v1"

// Manifest is plugin.toml.
type Manifest struct {
	Name           string                 `toml:"name"`
	Version        string                 `toml:"version"`
	Protocol       string                 `toml:"protocol"`
	Capabilities   []string               `toml:"capabilities"`
	Entry          []string               `toml:"entry"`
	Priority       int                    `toml:"priority"`
	TimeoutSeconds int                    `toml:"timeout_seconds"`
	Requires       []string               `toml:"requires"`
	Config         map[string]interface{} `toml:"config"`
	Persistent     bool                   `toml:"persistent"`
	Dir            string                 `toml:"-"`
}

// Info is a plugin list row.
type Info struct {
	Manifest
	Enabled  bool
	Healthy  bool
	Missing  []string
	Protocol string
	Error    string
}

// Manager discovers and invokes plugins.
type Manager struct {
	root   string
	cfg    config.Library
	global config.Global
	log    *logging.Logger
	mu     sync.Mutex
	items  []*Manifest
}

// NewManager loads the global plugin directory.
func NewManager(root string, cfg config.Library, global config.Global, log *logging.Logger) *Manager {
	m := &Manager{root: root, cfg: cfg, global: global, log: log}
	m.load()
	return m
}

func (m *Manager) load() {
	dir := config.PluginsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var out []*Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), "plugin.toml")
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var mf Manifest
		if err := toml.Unmarshal(data, &mf); err != nil {
			m.log.Warn("plugin.bad_manifest", map[string]interface{}{"path": p, "error": err.Error()})
			continue
		}
		mf.Dir = filepath.Join(dir, e.Name())
		out = append(out, &mf)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].Name < out[j].Name
	})
	m.items = out
}

// Reload rescans the plugin directory.
func (m *Manager) Reload() { m.load() }

// enabled reports library-level enable/disable state (default enabled).
func (m *Manager) enabled(mf *Manifest) bool {
	if pc, ok := m.cfg.Plugins[mf.Name]; ok && pc.Enabled != nil {
		return *pc.Enabled
	}
	return true
}

// pluginConfig merges manifest config under library config.
func (m *Manager) pluginConfig(mf *Manifest) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range mf.Config {
		out[k] = v
	}
	if pc, ok := m.cfg.Plugins[mf.Name]; ok {
		for k, v := range pc.Config {
			out[k] = v
		}
	}
	return out
}

// List returns all discovered plugins with health information.
func (m *Manager) List(checkHealth bool) []Info {
	var out []Info
	for _, mf := range m.items {
		info := Info{Manifest: *mf, Enabled: m.enabled(mf)}
		if !protocolOK(mf) {
			info.Error = "unsupported protocol " + mf.Protocol
		}
		for _, req := range mf.Requires {
			if _, err := exec.LookPath(req); err != nil {
				info.Missing = append(info.Missing, req)
			}
		}
		info.Healthy = info.Enabled && info.Error == "" && len(info.Missing) == 0
		if checkHealth && info.Healthy {
			res, err := m.call(mf, "health", map[string]interface{}{}, 15*time.Second, true)
			if err != nil {
				info.Healthy = false
				info.Error = err.Error()
			} else {
				var h struct {
					OK      bool     `json:"ok"`
					Missing []string `json:"missing"`
				}
				if json.Unmarshal(res, &h) == nil {
					info.Healthy = h.OK && len(h.Missing) == 0
					info.Missing = h.Missing
				}
			}
		}
		out = append(out, info)
	}
	return out
}

// FetchMetadata invokes every metadata plugin and returns candidates.
func (m *Manager) FetchMetadata(pdfPath, sha string, hints map[string]string) []model.LinkCandidate {
	var out []model.LinkCandidate
	for _, mf := range m.items {
		if !m.enabled(mf) || !hasCap(mf, "metadata") || !protocolOK(mf) || !requiresOK(mf) {
			continue
		}
		params := map[string]interface{}{
			"pdf_path": pdfPath,
			"sha256":   sha,
			"hints":    hints,
			"config":   m.pluginConfig(mf),
		}
		res, err := m.call(mf, "metadata.fetch", params, m.timeout(mf, "metadata"), true)
		if err != nil {
			m.log.Warn("plugin.metadata_failed", map[string]interface{}{"plugin": mf.Name, "error": err.Error()})
			continue
		}
		var resp struct {
			Candidates []struct {
				Fields     map[string]interface{} `json:"fields"`
				Confidence float64                `json:"confidence"`
				Source     string                 `json:"source"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(res, &resp); err != nil {
			m.log.Warn("plugin.bad_response", map[string]interface{}{"plugin": mf.Name, "error": err.Error()})
			continue
		}
		for _, c := range resp.Candidates {
			if c.Confidence <= 0 {
				c.Confidence = 0.5
			}
			title, _ := c.Fields["title"].(string)
			out = append(out, model.LinkCandidate{
				Source: mf.Name, Priority: mf.Priority, Confidence: c.Confidence,
				Fields: c.Fields, Title: title,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out
}

// ExtractText uses an extract-capability plugin if installed.
func (m *Manager) ExtractText(pdfPath string) (string, int, bool) {
	for _, mf := range m.items {
		if !m.enabled(mf) || !hasCap(mf, "extract") || !protocolOK(mf) || !requiresOK(mf) {
			continue
		}
		res, err := m.call(mf, "extract.text", map[string]interface{}{"pdf_path": pdfPath}, m.timeout(mf, "extract"), true)
		if err != nil {
			m.log.Warn("plugin.extract_failed", map[string]interface{}{"plugin": mf.Name, "error": err.Error()})
			continue
		}
		var resp struct {
			Text  string `json:"text"`
			Pages int    `json:"pages"`
		}
		if json.Unmarshal(res, &resp) == nil {
			return resp.Text, resp.Pages, true
		}
	}
	return "", 0, false
}

// Convert runs the highest-priority convert plugin. Returns produced paths.
func (m *Manager) Convert(pdfPath, outDir string) ([]string, string, error) {
	for _, mf := range m.items {
		if !m.enabled(mf) || !hasCap(mf, "convert") || !protocolOK(mf) || !requiresOK(mf) {
			continue
		}
		res, err := m.call(mf, "convert.markdown", map[string]interface{}{
			"pdf_path": pdfPath, "out_dir": outDir,
		}, m.timeout(mf, "convert"), true)
		if err != nil {
			return nil, mf.Name, err
		}
		var resp struct {
			Files  []string `json:"files"`
			Method string   `json:"method"`
		}
		if err := json.Unmarshal(res, &resp); err != nil {
			return nil, mf.Name, err
		}
		return resp.Files, mf.Name, nil
	}
	return nil, "", errors.New("no convert plugin installed (try installing a docling/convert plugin)")
}

// Capture runs a capture plugin.
func (m *Manager) Capture(url, outDir string) ([]string, string, error) {
	for _, mf := range m.items {
		if !m.enabled(mf) || !hasCap(mf, "capture") || !protocolOK(mf) || !requiresOK(mf) {
			continue
		}
		res, err := m.call(mf, "capture.web", map[string]interface{}{
			"url": url, "out_dir": outDir,
		}, m.timeout(mf, "capture"), true)
		if err != nil {
			return nil, mf.Name, err
		}
		var resp struct {
			Files []string `json:"files"`
			URL   string   `json:"url"`
		}
		if err := json.Unmarshal(res, &resp); err != nil {
			return nil, mf.Name, err
		}
		return resp.Files, mf.Name, nil
	}
	return nil, "", errors.New("no capture plugin installed; install one to snapshot web pages")
}

func (m *Manager) timeout(mf *Manifest, cap string) time.Duration {
	if mf.TimeoutSeconds > 0 {
		return time.Duration(mf.TimeoutSeconds) * time.Second
	}
	switch cap {
	case "metadata":
		return 30 * time.Second
	case "extract":
		return 120 * time.Second
	case "convert":
		return 1800 * time.Second
	case "capture":
		return 60 * time.Second
	}
	return 60 * time.Second
}

func (m *Manager) call(mf *Manifest, method string, params interface{}, timeout time.Duration, handshake bool) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := mf.Entry
	if len(args) == 0 {
		return nil, errors.New("manifest has no entry")
	}
	bin := args[0]
	if strings.HasPrefix(bin, "./") || strings.HasPrefix(bin, "../") || filepath.IsAbs(bin) {
		bin = filepath.Join(mf.Dir, bin)
	}
	cmd := exec.CommandContext(ctx, bin, args[1:]...)
	cmd.Dir = mf.Dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin: %w", err)
	}
	var stderrBuf bytes.Buffer
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			stderrBuf.WriteString(line + "\n")
			m.log.Info("plugin.stderr", map[string]interface{}{"plugin": mf.Name, "line": line})
		}
	}()
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	r := bufio.NewReader(stdout)
	id := 1
	if handshake {
		if _, err := m.roundTrip(ctx, r, stdin, id, "init", map[string]interface{}{
			"library_root": m.root, "config": m.pluginConfig(mf), "protocol": Protocol,
		}, &stderrBuf, mf.Name); err != nil {
			return nil, err
		}
		id++
	}
	raw, err := m.roundTrip(ctx, r, stdin, id, method, params, &stderrBuf, mf.Name)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("plugin %s timed out after %s", mf.Name, timeout)
		}
		return nil, err
	}
	return raw, nil
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (m *Manager) roundTrip(ctx context.Context, r *bufio.Reader, w interface{ Write([]byte) (int, error) },
	id int, method string, params interface{}, stderrBuf *bytes.Buffer, name string) (json.RawMessage, error) {
	req := map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	b, _ := json.Marshal(req)
	if _, err := w.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadBytes('\n')
		ch <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil && len(bytes.TrimSpace(res.line)) == 0 {
			return nil, fmt.Errorf("plugin %s closed stdout: %v", name, res.err)
		}
		line := bytes.TrimSpace(res.line)
		if len(line) == 0 {
			return nil, fmt.Errorf("plugin %s sent empty frame", name)
		}
		var resp rpcResp
		if err := json.Unmarshal(line, &resp); err != nil {
			return nil, fmt.Errorf("plugin %s sent invalid JSON: %v", name, err)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("plugin %s error %d: %s", name, resp.Error.Code, resp.Error.Message)
		}
		if method == "init" {
			var handshake struct {
				OK       bool   `json:"ok"`
				Protocol string `json:"protocol"`
			}
			if err := json.Unmarshal(resp.Result, &handshake); err != nil {
				return nil, fmt.Errorf("plugin %s invalid init result", name)
			}
			if !handshake.OK {
				return nil, fmt.Errorf("plugin %s refused init", name)
			}
			if handshake.Protocol != Protocol {
				return nil, fmt.Errorf("plugin %s protocol %q != %q", name, handshake.Protocol, Protocol)
			}
		}
		return resp.Result, nil
	}
}

func hasCap(mf *Manifest, cap string) bool {
	for _, c := range mf.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

func protocolOK(mf *Manifest) bool { return mf.Protocol == Protocol }

func requiresOK(mf *Manifest) bool {
	for _, req := range mf.Requires {
		if _, err := exec.LookPath(req); err != nil {
			return false
		}
	}
	return true
}
