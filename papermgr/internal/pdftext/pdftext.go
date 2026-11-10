// Package pdftext extracts text from PDFs using the external pdftotext binary.
package pdftext

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrUnavailable indicates pdftotext is not installed.
var ErrUnavailable = errors.New("pdftotext not found; install poppler-utils to enable full-text search")

// Available reports whether pdftotext is on PATH.
func Available() bool {
	_, err := exec.LookPath("pdftotext")
	return err == nil
}

// Extract returns the reading-order text of path and a page count.
func Extract(path string) (string, int, error) {
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", 0, ErrUnavailable
	}
	cmd := exec.Command(bin, path, "-")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", 0, fmt.Errorf("pdftotext failed: %s", msg)
	}
	text := out.String()
	return text, CountPages(text), nil
}

// ExtractFirstPage returns the text of the first page, used for metadata hints.
func ExtractFirstPage(path string) (string, error) {
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", ErrUnavailable
	}
	cmd := exec.Command(bin, "-f", "1", "-l", "1", path, "-")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// CountPages counts form feeds emitted by pdftotext.
func CountPages(text string) int {
	n := strings.Count(text, "\f")
	if n == 0 && strings.TrimSpace(text) != "" {
		return 1
	}
	if strings.HasSuffix(text, "\f") {
		return n
	}
	return n + 1
}
