package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type entry struct {
	name string
	body string
	typ  byte
}

func tgz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: e.typ}
		if e.typ == tar.TypeSymlink {
			hdr.Linkname, hdr.Size = "/bin/sh", 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.typ != tar.TypeSymlink {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const goodBinary = "#!/bin/sh\necho 'memors-mcp 9.9.9'\n"

// fakeGitHub serves a release API and its assets over TLS.
type fakeGitHub struct {
	ts      *httptest.Server
	archive []byte
	sums    string
	noSums  bool
	extra   map[string]http.HandlerFunc
}

func newFake(t *testing.T, archive []byte) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{archive: archive, extra: map[string]http.HandlerFunc{}}
	f.sums = sumLine(archive) + strings.Repeat("0", 64) + "  memors-mcp_9.9.9_linux_amd64.tar.gz\n"
	f.ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := f.extra[r.URL.Path]; h != nil {
			h(w, r)
			return
		}
		switch r.URL.Path {
		case "/api":
			if r.Header.Get("User-Agent") == "" {
				http.Error(w, "no user agent", http.StatusForbidden)
				return
			}
			assets := []Asset{{Name: "memors-mcp_9.9.9_darwin_arm64.tar.gz", URL: f.ts.URL + "/dl/archive", Size: int64(len(f.archive))}}
			if !f.noSums {
				assets = append(assets, Asset{Name: "checksums.txt", URL: f.ts.URL + "/dl/sums", Size: int64(len(f.sums))})
			}
			_ = json.NewEncoder(w).Encode(Release{Tag: "v9.9.9", PageURL: "https://github.com/x/y/releases/v9.9.9", Assets: assets})
		case "/dl/archive":
			_, _ = w.Write(f.archive)
		case "/dl/sums":
			_, _ = w.Write([]byte(f.sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.ts.Close)
	return f
}

func sumLine(archive []byte) string {
	sum := sha256.Sum256(archive)
	return hex.EncodeToString(sum[:]) + "  memors-mcp_9.9.9_darwin_arm64.tar.gz\n"
}

func (f *fakeGitHub) client() *Client {
	c := New("memors-tray-test")
	c.HTTP.Transport = f.ts.Client().Transport
	c.API = f.ts.URL + "/api"
	return c
}

func install(t *testing.T, f *fakeGitHub, dir string) (string, []Phase, error) {
	t.Helper()
	ctx := context.Background()
	c := f.client()
	rel, err := c.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	var phases []Phase
	path, err := c.Install(ctx, rel, "arm64", dir, func(p Progress) {
		if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
			phases = append(phases, p.Phase)
		}
	})
	return path, phases, err
}

func TestInstall(t *testing.T) {
	archive := tgz(t, entry{"LICENSE", "MIT", tar.TypeReg}, entry{"../escape", "x", tar.TypeReg},
		entry{"memors-mcp", goodBinary, tar.TypeReg}, entry{"README.md", "readme", tar.TypeReg})
	f := newFake(t, archive)
	dir := filepath.Join(t.TempDir(), "bin")
	path, phases, err := install(t, f, dir)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "memors-mcp") || !slices.Equal(phases, []Phase{Downloading, Verifying, Installing}) {
		t.Errorf("path %s, phases %v", path, phases)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("installed binary: %v %v", fi, err)
	}
	if des, _ := os.ReadDir(dir); len(des) != 1 {
		t.Errorf("dir holds more than memors-mcp: %v", des)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); err == nil {
		t.Error("an entry escaped the install directory")
	}
	if _, _, err := install(t, f, dir); err != nil {
		t.Errorf("reinstall over the old binary: %v", err)
	}
}

func TestInstallRefusesBadDownloads(t *testing.T) {
	good := tgz(t, entry{"memors-mcp", goodBinary, tar.TypeReg})
	for _, c := range []struct {
		name  string
		setup func(f *fakeGitHub)
		want  string
	}{
		{"checksum mismatch", func(f *fakeGitHub) { f.archive = tgz(t, entry{"memors-mcp", goodBinary + "#tampered\n", tar.TypeReg}) }, "checksum"},
		{"no checksums.txt", func(f *fakeGitHub) { f.noSums = true }, "no checksums.txt"},
		{"no entry for the archive", func(f *fakeGitHub) { f.sums = strings.Repeat("0", 64) + "  other.tar.gz\n" }, "no entry"},
		{"no binary inside", func(f *fakeGitHub) {
			f.archive = tgz(t, entry{"README.md", "x", tar.TypeReg})
			f.sums = sumLine(f.archive)
		}, "no memors-mcp"},
		{"binary is a symlink", func(f *fakeGitHub) {
			f.archive = tgz(t, entry{"memors-mcp", "", tar.TypeSymlink})
			f.sums = sumLine(f.archive)
		}, "no memors-mcp"},
		{"binary does not run", func(f *fakeGitHub) {
			f.archive = tgz(t, entry{"memors-mcp", "#!/bin/sh\nexit 3\n", tar.TypeReg})
			f.sums = sumLine(f.archive)
		}, "does not run"},
		{"redirect to plain http", func(f *fakeGitHub) {
			f.extra["/dl/archive"] = func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "http://example.com/memors-mcp.tar.gz", http.StatusFound)
			}
		}, "refusing a redirect"},
		{"more bytes than announced", func(f *fakeGitHub) {
			f.extra["/dl/archive"] = func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(append(slices.Clone(f.archive), 0)) }
		}, "expected"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t, good)
			c.setup(f)
			dir := t.TempDir()
			old := filepath.Join(dir, "memors-mcp")
			if err := os.WriteFile(old, []byte("old binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			_, _, err := install(t, f, dir)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if b, _ := os.ReadFile(old); string(b) != "old binary" {
				t.Error("the installed binary was replaced although the install failed")
			}
			if des, _ := os.ReadDir(dir); len(des) != 1 {
				t.Errorf("leftovers in the install dir: %v", des)
			}
		})
	}
}

func TestLatestErrors(t *testing.T) {
	f := newFake(t, nil)
	f.extra["/api"] = func(w http.ResponseWriter, r *http.Request) { http.Error(w, "rate limited", http.StatusForbidden) }
	if _, err := f.client().Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("rate limit: %v", err)
	}
	c := New("x")
	c.API = "http://example.com/api"
	if _, err := c.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Errorf("plain http API accepted: %v", err)
	}
	rel := Release{Tag: "v1.5.0"}
	if _, err := New("x").Install(context.Background(), rel, "arm64", t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "memors-mcp_1.5.0_darwin_arm64.tar.gz") {
		t.Errorf("missing archive: %v", err)
	}
	if !errors.Is(fmt.Errorf("x: %w", ErrChecksum), ErrChecksum) {
		t.Error("ErrChecksum does not unwrap")
	}
}

// With MEMORS_TRAY_LIVE_GITHUB=1, install the real latest release into a
// temporary directory: proves the asset names, checksums.txt format and
// GitHub's redirects still match what Install expects.
func TestLiveGitHub(t *testing.T) {
	if os.Getenv("MEMORS_TRAY_LIVE_GITHUB") != "1" {
		t.Skip("set MEMORS_TRAY_LIVE_GITHUB=1 to download the real latest release")
	}
	ctx := context.Background()
	c := New("memors-tray-test")
	rel, err := c.Latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path, err := c.Install(ctx, rel, "arm64", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("installed %s from %s", path, rel.Tag)
}
