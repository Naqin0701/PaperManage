package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"papermgr/internal/config"
	"papermgr/internal/model"
)

// listOptions controls the human, TSV and fzf output of search-like commands.
type listOptions struct {
	Sort      string
	Limit     int
	NoTags    bool
	TSV       bool
	FZF       bool
	FZFMulti  bool
	NoPreview bool
}

var sortKeys = map[string]bool{
	"relevance": true, "year-": true, "year+": true,
	"added-": true, "added+": true, "title+": true, "title-": true,
	"updated-": true, "updated+": true,
}

func addListFlags(cmd *cobra.Command, opts *listOptions, defaultSort string) {
	cmd.Flags().StringVar(&opts.Sort, "sort", defaultSort, "sort key (relevance, year-, year+, added-, added+, title+, title-)")
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "maximum number of results (0 = all)")
	cmd.Flags().BoolVar(&opts.TSV, "tsv", false, "tab-separated output (no header, no truncation)")
	cmd.Flags().BoolVar(&opts.NoTags, "no-tags", false, "hide the tags column in human output")
	cmd.Flags().BoolVar(&opts.FZF, "fzf", defaultFZFEnabled(), "pick results interactively with fzf")
	cmd.Flags().BoolVar(&opts.FZFMulti, "fzf-multi", false, "allow multiple selections in fzf")
	cmd.Flags().BoolVar(&opts.NoPreview, "no-preview", false, "disable the fzf preview window")
}

func defaultFZFEnabled() bool {
	if os.Getenv("CI") != "" || !stdoutIsTTY() {
		return false
	}
	g, err := config.LoadGlobal()
	return err == nil && g.FZF
}

func listQuery(expr string, opts listOptions) (string, error) {
	if opts.Sort != "" && !sortKeys[opts.Sort] {
		return "", fmt.Errorf("unknown sort key %q", opts.Sort)
	}
	expr = strings.TrimSpace(expr)
	if !hasSortField(expr) && opts.Sort != "" {
		if expr == "" {
			expr = "sort:" + opts.Sort
		} else {
			expr += " sort:" + opts.Sort
		}
	}
	if expr == "" {
		expr = "sort:relevance"
	}
	return expr, nil
}

func hasSortField(expr string) bool {
	for _, f := range strings.Fields(expr) {
		if strings.HasPrefix(strings.ToLower(f), "sort:") {
			return true
		}
	}
	return false
}

func applyLimit(hits []model.SearchHit, limit int) ([]model.SearchHit, int) {
	total := len(hits)
	if limit > 0 && total > limit {
		return hits[:limit], total
	}
	return hits, total
}

func prepareOutput(cmd *cobra.Command, opts *listOptions) error {
	if cmd.Flags().Changed("fzf") && opts.FZF && (opts.TSV || flagJSON) {
		return errors.New("--fzf cannot be combined with --tsv or --json")
	}
	if flagJSON || opts.TSV {
		opts.FZF = false
	}
	if opts.FZF && !hasFZF() {
		if cmd.Flags().Changed("fzf") {
			return errors.New("fzf not found in PATH")
		}
		opts.FZF = false
		fmt.Fprintln(os.Stderr, "papermgr: fzf not found; falling back to the table")
	}
	return nil
}

func hasFZF() bool {
	_, err := exec.LookPath("fzf")
	return err == nil
}

func outputHits(hits []model.SearchHit, total int, root string, opts listOptions) error {
	if flagJSON {
		return printJSON(hits)
	}
	if opts.FZF {
		return runFZF(hits, total, root, opts)
	}
	if opts.TSV || !stdoutIsTTY() {
		printHitsTSV(hits)
		printHitCount(total, len(hits))
		return nil
	}
	printHitsPretty(hits, opts)
	printHitCount(total, len(hits))
	return nil
}

func printHitCount(total, shown int) {
	switch {
	case total == 0:
		fmt.Fprintln(os.Stderr, "0 result(s)")
	case total > shown:
		fmt.Fprintf(os.Stderr, "showing %d of %d result(s)\n", shown, total)
	default:
		fmt.Fprintf(os.Stderr, "%d result(s)\n", total)
	}
}

func printHitsTSV(hits []model.SearchHit) {
	for _, h := range hits {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n",
			h.Citekey, yearOf(h), hitStatus(h.Tags), cleanField(h.Title), cleanField(strings.Join(h.Tags, ",")))
	}
}

// cleanField keeps one line per record for TSV and fzf input.
func cleanField(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func yearOf(h model.SearchHit) string {
	if h.Year == nil {
		return ""
	}
	return fmt.Sprint(*h.Year)
}

// rowLayout is the column plan shared by the table header and rows.
type rowLayout struct {
	width    int
	titleW   int
	citekeyW int
	tagsW    int
	prefixW  int
}

func makeLayout(width int, opts listOptions, fzf bool) rowLayout {
	const prefixW = 9 // status + note + space + 4-char year + gap
	showTags := !opts.NoTags
	lay := rowLayout{width: width, prefixW: prefixW}
	if !fzf && width >= 72 {
		lay.citekeyW = clampInt(width/4, 22, 40)
	}
	if showTags && width >= 108 {
		lay.tagsW = clampInt(width/5, 18, 30)
	}
	for {
		lay.titleW = width - prefixW - gapFor(lay.citekeyW) - gapFor(lay.tagsW)
		if lay.titleW >= 16 || (lay.citekeyW == 0 && lay.tagsW == 0) {
			break
		}
		if lay.tagsW > 0 {
			lay.tagsW = 0
			continue
		}
		lay.citekeyW = 0
	}
	if lay.titleW < 8 {
		lay.titleW = 8
	}
	return lay
}

func gapFor(w int) int {
	if w > 0 {
		return w + 2
	}
	return 0
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type cliStyles struct {
	color  bool
	accent lipgloss.Style
	dim    lipgloss.Style
	green  lipgloss.Style
	yellow lipgloss.Style
	cyan   lipgloss.Style
}

func newCLIStyles(color bool) cliStyles {
	if !color {
		return cliStyles{}
	}
	return cliStyles{
		color:  true,
		accent: lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")),
		dim:    lipgloss.NewStyle().Foreground(lipgloss.Color("#737aa2")),
		green:  lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a")),
		yellow: lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68")),
		cyan:   lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff")),
	}
}

func (s cliStyles) paint(st lipgloss.Style, text string) string {
	if !s.color || text == "" {
		return text
	}
	return st.Render(text)
}

func printHitsPretty(hits []model.SearchHit, opts listOptions) {
	width := terminalWidth()
	st := newCLIStyles(useColor())
	lay := makeLayout(width, opts, false)
	if len(hits) > 1 && width >= 48 {
		fmt.Println(prettyHeader(lay, st))
	}
	for _, h := range hits {
		fmt.Println(prettyRow(h, lay, st))
	}
}

func prettyHeader(lay rowLayout, st cliStyles) string {
	var b strings.Builder
	b.WriteString("   " + st.paint(st.dim, "YEAR") + "  ")
	b.WriteString(st.paint(st.dim, padPlain("TITLE", lay.titleW)))
	if lay.citekeyW > 0 {
		b.WriteString("  " + st.paint(st.dim, padPlain("CITEKEY", lay.citekeyW)))
	}
	if lay.tagsW > 0 {
		b.WriteString("  " + st.paint(st.dim, padPlain("TAGS", lay.tagsW)))
	}
	return ansi.Truncate(b.String(), lay.width, "…")
}

func prettyRow(h model.SearchHit, lay rowLayout, st cliStyles) string {
	glyph, glyphStyle := glyphFor(hitStatus(h.Tags))
	var b strings.Builder
	b.WriteString(st.paint(glyphStyle, glyph))
	if h.HasNote {
		b.WriteString(st.paint(st.green, "✎"))
	} else {
		b.WriteString(" ")
	}
	b.WriteString(" ")
	b.WriteString(st.paint(st.dim, fmt.Sprintf("%4s", yearOf(h))))
	b.WriteString("  ")

	title := truncateDisplay(cleanField(h.Title), lay.titleW)
	b.WriteString(title)
	if lay.citekeyW > 0 {
		if pad := lay.titleW - ansi.StringWidth(title); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString("  ")
		key := ellipsisMiddle(h.Citekey, lay.citekeyW)
		b.WriteString(st.paint(st.accent, key))
		if pad := lay.citekeyW - ansi.StringWidth(key); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
	}
	if lay.tagsW > 0 {
		b.WriteString("  ")
		tags := cleanField(tagText(h.Tags))
		b.WriteString(st.paint(st.cyan, truncateDisplay(tags, lay.tagsW)))
	}
	return ansi.Truncate(b.String(), lay.width, "…")
}

func hitStatus(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, "status/") {
			return strings.TrimPrefix(t, "status/")
		}
	}
	return ""
}

func glyphFor(status string) (string, lipgloss.Style) {
	switch status {
	case "to-read":
		return "●", lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68"))
	case "reading":
		return "◐", lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff"))
	case "read":
		return "✓", lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a"))
	case "skim":
		return "◦", lipgloss.NewStyle().Foreground(lipgloss.Color("#737aa2"))
	case "":
		return " ", lipgloss.NewStyle()
	default:
		return "•", lipgloss.NewStyle().Foreground(lipgloss.Color("#737aa2"))
	}
}

func tagText(tags []string) string {
	if len(tags) == 0 {
		return "—"
	}
	var parts []string
	for _, t := range tags {
		parts = append(parts, "#"+t)
	}
	return strings.Join(parts, " ")
}

func terminalWidth() int {
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return 100
}

func stdoutIsTTY() bool {
	return term.IsTerminal(os.Stdout.Fd())
}

func useColor() bool {
	if !stdoutIsTTY() {
		return false
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return true
}

func padPlain(s string, width int) string {
	s = truncateDisplay(s, width)
	if n := width - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func truncateDisplay(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

func ellipsisMiddle(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width < 6 {
		return string(r[:width-1]) + "…"
	}
	left := (width - 1) / 2
	right := width - 1 - left
	return string(r[:left]) + "…" + string(r[len(r)-right:])
}

func runFZF(hits []model.SearchHit, total int, root string, opts listOptions) error {
	if len(hits) == 0 {
		printHitCount(total, 0)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	width := terminalWidth()
	st := newCLIStyles(useColor())
	lay := makeLayout(width, opts, true)
	var in bytes.Buffer
	for _, h := range hits {
		fmt.Fprintf(&in, "%s\t%s\n", h.Citekey, prettyRow(h, lay, st))
	}

	args := []string{
		"--ansi",
		"--delimiter=\t",
		"--with-nth=2..",
		"--height=90%",
		"--reverse",
	}
	if opts.FZFMulti {
		args = append(args, "--multi", "--header=tab: mark   enter: print marked   esc: cancel")
	} else {
		args = append(args, "--header=enter: print citekey   esc: cancel")
	}
	if !opts.NoPreview {
		preview := fmt.Sprintf("%s --library %s show {1}", shellQuote(exe), shellQuote(root))
		args = append(args, "--preview", preview, "--preview-window=right:60%:wrap")
	}

	cmd := exec.Command("fzf", args...)
	cmd.Stdin = &in
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && (exitErr.ExitCode() == 130 || exitErr.ExitCode() == 1) {
			return nil // cancelled or no match
		}
		return err
	}
	selected := parseFZFSelection(out.String())
	if len(selected) == 0 {
		return nil
	}
	byKey := make(map[string]model.SearchHit, len(hits))
	for _, h := range hits {
		byKey[h.Citekey] = h
	}
	if flagJSON {
		outHits := make([]model.SearchHit, 0, len(selected))
		for _, ck := range selected {
			if h, ok := byKey[ck]; ok {
				outHits = append(outHits, h)
			}
		}
		return printJSON(outHits)
	}
	for _, ck := range selected {
		fmt.Println(ck)
	}
	fmt.Fprintf(os.Stderr, "%d selected\n", len(selected))
	return nil
}

func parseFZFSelection(out string) []string {
	var keys []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		if i := strings.IndexByte(line, '\t'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(ansi.Strip(line))
		if line != "" {
			keys = append(keys, line)
		}
	}
	return keys
}

func shellQuote(s string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
