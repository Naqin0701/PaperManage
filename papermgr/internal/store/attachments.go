package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"papermgr/internal/config"
	"papermgr/internal/model"
)

// Attachment is one file under attachments/.
type Attachment struct {
	Kind string `json:"kind"`
	Rel  string `json:"rel"`
	Size int64  `json:"size"`
}

var noteExts = map[string]bool{".md": true, ".tex": true, ".typ": true}

// Attachments lists attachments for a paper.
func (s *Store) Attachments(citekey string) ([]Attachment, error) {
	root := filepath.Join(s.PaperDir(citekey), "attachments")
	var out []Attachment
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		kind := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		info, _ := d.Info()
		var size int64
		if info != nil {
			size = info.Size()
		}
		out = append(out, Attachment{Kind: kind, Rel: rel, Size: size})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, err
}

// Attach copies files under attachments/<kind>. An empty kind infers it.
func (s *Store) Attach(citekey string, files []string, kind string) ([]Attachment, error) {
	p, err := s.Load(citekey)
	if err != nil {
		return nil, err
	}
	var added []Attachment
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			return added, err
		}
		if st.IsDir() {
			return added, fmt.Errorf("%s is a directory", f)
		}
		k := kind
		if k == "" {
			k = inferKind(f)
		}
		if !validKind(k) {
			return added, fmt.Errorf("invalid attachment kind %q", k)
		}
		if k == "note" {
			if notes, _ := listNoteFiles(p.Dir); len(notes) > 0 {
				return added, fmt.Errorf("paper already has a main note (%s); a paper may have at most one (INV-3)", filepath.Base(notes[0]))
			}
		}
		dir := filepath.Join(p.Dir, "attachments", k)
		if k == "web" && strings.HasSuffix(strings.ToLower(f), ".html") {
			// keep as-is
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return added, err
		}
		dst := uniquePath(filepath.Join(dir, sanitizeFilename(filepath.Base(f))))
		if err := copyFile(f, dst, st.Mode().Perm()); err != nil {
			return added, err
		}
		info, _ := os.Stat(dst)
		var size int64
		if info != nil {
			size = info.Size()
		}
		added = append(added, Attachment{Kind: k, Rel: filepath.Join(k, filepath.Base(dst)), Size: size})
	}
	if err := s.SyncPaper(citekey); err != nil {
		return added, err
	}
	s.Log.Info("attach.add", map[string]interface{}{"citekey": citekey, "count": len(files), "kind": kind})
	return added, nil
}

func inferKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".tex", ".typ":
		return "note"
	case ".html", ".htm":
		return "web"
	case ".pdf", ".zip", ".tar", ".gz", ".png", ".jpg", ".jpeg", ".svg", ".csv":
		return "supplement"
	default:
		return "misc"
	}
}

func validKind(k string) bool {
	switch k {
	case "note", "supplement", "web", "misc":
		return true
	}
	return false
}

// AttachWebURL asks a capture plugin for a web snapshot (FR-5.5).
func (s *Store) AttachWebURL(citekey, url string) ([]string, string, error) {
	p, err := s.Load(citekey)
	if err != nil {
		return nil, "", err
	}
	outDir := filepath.Join(p.Dir, "attachments", "web")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, "", err
	}
	files, pluginName, err := s.Plugins.Capture(url, outDir)
	if err != nil {
		return nil, pluginName, fmt.Errorf("%w (install a plugin with the \"capture\" capability; see `papermgr plugin list`)", err)
	}
	meta := map[string]interface{}{"url": url, "captured_at": time.Now().Format(time.RFC3339), "plugin": pluginName}
	b, _ := json.MarshalIndent(meta, "", "  ")
	_ = config.AtomicWrite(filepath.Join(outDir, "meta.json"), append(b, '\n'), 0o644)
	if err := s.SyncPaper(citekey); err != nil {
		return files, pluginName, err
	}
	return files, pluginName, nil
}

// NoteNew creates the single main note for a paper.
func (s *Store) NoteNew(citekey, format string, replace bool) (string, error) {
	p, err := s.Load(citekey)
	if err != nil {
		return "", err
	}
	format = strings.TrimPrefix(strings.ToLower(format), ".")
	switch format {
	case "md", "tex", "typ":
	default:
		return "", fmt.Errorf("unsupported note format %q (use md, tex or typ)", format)
	}
	dir := filepath.Join(p.Dir, "attachments", "note")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	existing, _ := listNoteFiles(p.Dir)
	if len(existing) > 0 && !replace {
		return "", fmt.Errorf("paper already has a main note (%s); use --replace to overwrite", filepath.Base(existing[0]))
	}
	if replace {
		for _, f := range existing {
			_ = os.Remove(f)
		}
	}
	path := filepath.Join(dir, "note."+format)
	content := noteTemplate(p.Meta, format)
	if err := config.AtomicWrite(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	if err := s.SyncPaper(citekey); err != nil {
		return path, err
	}
	s.Log.Info("note.new", map[string]interface{}{"citekey": citekey, "format": format})
	return path, nil
}

func noteTemplate(m model.Meta, format string) string {
	author := authorsText(m.Authors)
	year := ""
	if m.Year != nil {
		year = fmt.Sprint(*m.Year)
	}
	venue := ""
	if m.Venue != nil {
		venue = m.Venue.Name
		if m.Venue.Short != "" {
			venue = m.Venue.Short
		}
	}
	switch format {
	case "tex":
		return fmt.Sprintf(`\documentclass[11pt]{article}
\usepackage{hyperref}
\title{%s}
\author{%s}
\date{%s}
\begin{document}
\maketitle
\noindent\textbf{Venue:} %s \\
\textbf{DOI:} %s \\
\textbf{Citekey:} \texttt{%s}

\section{Summary}

\section{Notes}

\section{Open questions}

\end{document}
`, texEscape(m.Title), texEscape(author), texEscape(year), texEscape(venue), texEscape(m.DOI), texEscape(m.Citekey))
	case "typ":
		return fmt.Sprintf(`#set document(title: "%s", author: "%s")
#set page(margin: 2.5cm)
#set text(size: 11pt)

= %s

*Authors:* %s \
*Year:* %s \
*Venue:* %s \
*DOI:* %s \
*Citekey:* %s

== Summary

== Notes

== Open questions
`, m.Title, author, m.Title, author, year, venue, m.DOI, m.Citekey)
	default:
		return fmt.Sprintf(`# %s

- **Authors:** %s
- **Year:** %s
- **Venue:** %s
- **DOI:** %s
- **Citekey:** `+"`%s`"+`

## Abstract

%s

## Summary

## Notes

## Open questions
`, m.Title, author, year, venue, m.DOI, m.Citekey, m.Abstract)
	}
}

func texEscape(s string) string {
	r := strings.NewReplacer("\\", `\textbackslash{}`, "&", `\&`, "%", `\%`, "$", `\$`, "#", `\#`, "_", `\_`, "{", `\{`, "}", `\}`)
	return r.Replace(s)
}

func listNoteFiles(paperDir string) ([]string, error) {
	dir := filepath.Join(paperDir, "attachments", "note")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if noteExts[strings.ToLower(filepath.Ext(e.Name()))] {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// OpenTarget resolves what `open` should launch.
func (s *Store) OpenTarget(citekey, mode, name string) (string, error) {
	p, err := s.Load(citekey)
	if err != nil {
		return "", err
	}
	switch mode {
	case "", "pdf":
		path := filepath.Join(p.Dir, "paper.pdf")
		if !fileExists(path) {
			return "", errors.New("paper.pdf is missing")
		}
		return path, nil
	case "note":
		notes, _ := listNoteFiles(p.Dir)
		if len(notes) == 0 {
			return "", errors.New("no note yet; run `papermgr note new " + citekey + " --format md`")
		}
		return notes[0], nil
	case "supplement", "misc", "web", "markdown":
		dir := filepath.Join(p.Dir, "attachments", mode)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", fmt.Errorf("no %s attachments", mode)
		}
		if name != "" {
			for _, e := range entries {
				if e.Name() == name {
					return filepath.Join(dir, e.Name()), nil
				}
			}
			return "", fmt.Errorf("attachment %q not found in %s", name, mode)
		}
		if mode == "web" {
			if fileExists(filepath.Join(dir, "index.html")) {
				return filepath.Join(dir, "index.html"), nil
			}
		}
		for _, e := range entries {
			if !e.IsDir() && e.Name() != "meta.json" {
				return filepath.Join(dir, e.Name()), nil
			}
		}
		return "", fmt.Errorf("no files in %s", mode)
	}
	return "", fmt.Errorf("unknown open mode %q", mode)
}

// Convert runs the convert plugin and indexes the output.
func (s *Store) Convert(citekey string) ([]string, string, error) {
	p, err := s.Load(citekey)
	if err != nil {
		return nil, "", err
	}
	outDir := filepath.Join(p.Dir, "attachments", "markdown")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, "", err
	}
	files, pluginName, err := s.Plugins.Convert(filepath.Join(p.Dir, "paper.pdf"), outDir)
	if err != nil {
		p.State.Index.Error = "convert: " + err.Error()
		_ = s.SaveState(p)
		s.Log.Warn("convert.failed", map[string]interface{}{"citekey": citekey, "error": err.Error()})
		return files, pluginName, fmt.Errorf("%w (install a plugin with the \"convert\" capability)", err)
	}
	if err := s.SyncPaper(citekey); err != nil {
		return files, pluginName, err
	}
	s.Log.Info("convert.done", map[string]interface{}{"citekey": citekey, "plugin": pluginName, "files": len(files)})
	return files, pluginName, nil
}
