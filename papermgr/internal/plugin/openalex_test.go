package plugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"papermgr/internal/config"
	"papermgr/internal/logging"
)

const testWork = `{
  "id": "https://openalex.org/W1",
  "doi": "https://doi.org/10.1234/test",
  "title": "A Test Paper",
  "publication_year": 2020,
  "type": "article",
  "language": "en",
  "cited_by_count": 42,
  "authorships": [{
    "author": {
      "display_name": "Ada Lovelace",
      "orcid": "https://orcid.org/0000-0001-2345-6789"
    },
    "institutions": [{"display_name": "Analytical Engine Lab"}]
  }],
  "biblio": {"volume": "12", "issue": "3", "first_page": "1", "last_page": "9"},
  "primary_location": {
    "landing_page_url": "https://example.org/test",
    "source": {
      "display_name": "Journal of Tests",
      "type": "journal",
      "host_organization_name": "Test Press"
    }
  },
  "abstract_inverted_index": {"Hello": [0], "world": [1]},
  "keywords": [{"display_name": "testing", "score": 0.9}]
}`

// openalexManager installs the bundled OpenAlex plugin into a temporary config
// directory and points it at a fake API server.
func openalexManager(t *testing.T, baseURL string) *Manager {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_STATE_HOME", dir)

	src, err := filepath.Abs("../../plugins/openalex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(src); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultLibrary()
	cfg.Plugins["openalex"] = config.PluginConfig{
		Config: map[string]interface{}{"base_url": baseURL},
	}
	logDir := filepath.Join(dir, "log")
	return NewManager(dir+"/lib", cfg, config.Global{LogDir: logDir}, logging.New(logDir))
}

func TestBundledOpenAlexDOILookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/works/doi:") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(testWork))
	}))
	defer srv.Close()

	cands := openalexManager(t, srv.URL).FetchMetadata("/tmp/x.pdf", "abc", map[string]string{"doi": "10.1234/test"})
	if len(cands) != 1 {
		t.Fatalf("candidates = %+v", cands)
	}
	if cands[0].Source != "openalex" || cands[0].Confidence < 0.9 {
		t.Fatalf("candidate = %+v", cands[0])
	}
	f := cands[0].Fields
	if f["title"] != "A Test Paper" || f["doi"] != "10.1234/test" {
		t.Fatalf("fields = %+v", f)
	}
	if f["abstract"] != "Hello world" {
		t.Fatalf("abstract = %q", f["abstract"])
	}
	venue, _ := f["venue"].(map[string]interface{})
	if venue["name"] != "Journal of Tests" || venue["volume"] != "12" || venue["pages"] != "1-9" {
		t.Fatalf("venue = %+v", venue)
	}
	authors, _ := f["authors"].([]interface{})
	if len(authors) != 1 {
		t.Fatalf("authors = %+v", authors)
	}
	author, _ := authors[0].(map[string]interface{})
	if author["family"] != "Lovelace" || author["orcid"] != "0000-0001-2345-6789" {
		t.Fatalf("author = %+v", author)
	}
}

func TestBundledOpenAlexTitleFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/works/doi:") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/works" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []interface{}{json.RawMessage(testWork)}})
	}))
	defer srv.Close()

	cands := openalexManager(t, srv.URL).FetchMetadata("/tmp/x.pdf", "abc", map[string]string{
		"doi":         "10.9999/missing",
		"title_guess": "A Test Paper",
		"year":        "2020",
	})
	if len(cands) != 1 {
		t.Fatalf("candidates = %+v", cands)
	}
	if cands[0].Fields["title"] != "A Test Paper" || cands[0].Confidence != 0.9 {
		t.Fatalf("candidate = %+v", cands[0])
	}
}
