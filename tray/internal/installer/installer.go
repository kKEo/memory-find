// Package installer downloads memors-mcp from the project's GitHub releases
// and installs it for memors-tray: the archive for this Mac, checked against
// the release's checksums.txt, unpacked, tried out, and only then moved
// into place, atomically. Nothing runs before its checksum matched.
package installer

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LatestAPI is the GitHub API address of the newest memors-mcp release.
const LatestAPI = "https://api.github.com/repos/kKEo/memors/releases/latest"

// Limits on what is downloaded and unpacked.
const (
	maxJSON     = 1 << 20
	maxSums     = 64 << 10
	maxArchive  = 200 << 20
	maxBinary   = 300 << 20
	versionWait = 10 * time.Second
)

// ErrChecksum means the download does not match checksums.txt.
var ErrChecksum = errors.New("the download does not match the release's checksum; nothing was installed")

// Release is a GitHub release.
type Release struct {
	Tag       string    `json:"tag_name"`
	Name      string    `json:"name"`
	Published time.Time `json:"published_at"`
	PageURL   string    `json:"html_url"`
	Assets    []Asset   `json:"assets"`
}

// Asset is a file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Version is the release's version without the leading v.
func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// ArchiveName is the memors-mcp archive for macOS on goarch (arm64, amd64).
func (r Release) ArchiveName(goarch string) string {
	return fmt.Sprintf("memors-mcp_%s_darwin_%s.tar.gz", r.Version(), goarch)
}

// Asset finds an attached file by name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Phase is how far an installation got.
type Phase string

// The phases, in order.
const (
	Downloading Phase = "downloading"
	Verifying   Phase = "verifying"
	Installing  Phase = "installing"
)

// Progress is reported while installing.
type Progress struct {
	Phase       Phase
	Done, Total int64 // bytes, while downloading
}

// Client talks to GitHub.
type Client struct {
	HTTP      *http.Client
	API       string // LatestAPI unless testing
	UserAgent string
}

// New returns a client that follows redirects only to https addresses.
func New(userAgent string) *Client {
	return &Client{
		API:       LatestAPI,
		UserAgent: userAgent,
		HTTP: &http.Client{
			Timeout: 10 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if req.URL.Scheme != "https" {
					return fmt.Errorf("refusing a redirect to %s", req.URL.Redacted())
				}
				if len(via) >= 10 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
	}
}

// Latest returns the newest release.
func (c *Client) Latest(ctx context.Context) (Release, error) {
	var r Release
	body, err := c.get(ctx, c.API, "application/vnd.github+json")
	if err != nil {
		return r, err
	}
	defer body.Close()
	if err := json.NewDecoder(io.LimitReader(body, maxJSON)).Decode(&r); err != nil {
		return r, fmt.Errorf("reading the release list: %w", err)
	}
	if r.Tag == "" {
		return r, errors.New("GitHub returned no release")
	}
	return r, nil
}

// Install downloads rel's archive for goarch, checks it against the
// release's checksums.txt, unpacks memors-mcp, makes sure it runs, and moves
// it to dir/memors-mcp, replacing any older one. It returns the path.
func (c *Client) Install(ctx context.Context, rel Release, goarch, dir string, progress func(Progress)) (string, error) {
	if progress == nil {
		progress = func(Progress) {}
	}
	archive, ok := rel.Asset(rel.ArchiveName(goarch))
	if !ok {
		return "", fmt.Errorf("release %s has no macOS %s archive (%s)", rel.Tag, goarch, rel.ArchiveName(goarch))
	}
	sums, ok := rel.Asset("checksums.txt")
	if !ok {
		return "", fmt.Errorf("release %s has no checksums.txt; refusing to install unverified files", rel.Tag)
	}
	want, err := c.checksum(ctx, sums, archive.Name)
	if err != nil {
		return "", err
	}

	work, err := os.MkdirTemp("", "memors-tray-install-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	tgz := filepath.Join(work, archive.Name)
	got, err := c.download(ctx, archive, tgz, progress)
	if err != nil {
		return "", err
	}
	progress(Progress{Phase: Verifying})
	if got != want {
		return "", ErrChecksum
	}

	progress(Progress{Phase: Installing})
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := extract(tgz, dir)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp) // no-op after the rename
	if err := runs(ctx, tmp); err != nil {
		return "", fmt.Errorf("the downloaded memors-mcp does not run on this Mac: %w", err)
	}
	dst := filepath.Join(dir, "memors-mcp")
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// checksum reads the SHA-256 for name from checksums.txt.
func (c *Client) checksum(ctx context.Context, sums Asset, name string) (string, error) {
	body, err := c.get(ctx, sums.URL, "")
	if err != nil {
		return "", err
	}
	defer body.Close()
	sc := bufio.NewScanner(io.LimitReader(body, maxSums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if len(f[0]) != 64 {
				break
			}
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s; refusing to install unverified files", name)
}

// download saves an asset to path and returns its SHA-256.
func (c *Client) download(ctx context.Context, a Asset, path string, progress func(Progress)) (string, error) {
	body, err := c.get(ctx, a.URL, "application/octet-stream")
	if err != nil {
		return "", err
	}
	defer body.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	limit := int64(maxArchive)
	if a.Size > 0 && a.Size+1 < limit {
		limit = a.Size + 1 // a file larger than announced is wrong
	}
	w := &counter{w: io.MultiWriter(f, h), total: a.Size, report: progress}
	n, err := io.Copy(w, io.LimitReader(body, limit))
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", a.Name, err)
	}
	if a.Size > 0 && n != a.Size {
		return "", fmt.Errorf("downloading %s: got %d bytes, expected %d", a.Name, n, a.Size)
	}
	return hex.EncodeToString(h.Sum(nil)), f.Close()
}

// extract copies the memors-mcp binary at the top of the archive to a
// temporary executable file in dir and returns its path. Nothing else in
// the archive is written anywhere.
func extract(tgz, dir string) (string, error) {
	f, err := os.Open(tgz)
	if err != nil {
		return "", err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("not a gzip archive: %w", err)
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", errors.New("the archive has no memors-mcp binary")
		}
		if err != nil {
			return "", fmt.Errorf("reading the archive: %w", err)
		}
		if strings.TrimPrefix(hdr.Name, "./") != "memors-mcp" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Size <= 0 || hdr.Size > maxBinary {
			return "", fmt.Errorf("memors-mcp in the archive has an implausible size (%d bytes)", hdr.Size)
		}
		out, err := os.CreateTemp(dir, ".memors-mcp-*.tmp")
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, hdr.Size)); err != nil {
			out.Close()
			os.Remove(out.Name())
			return "", err
		}
		if err := out.Chmod(0o755); err != nil {
			out.Close()
			os.Remove(out.Name())
			return "", err
		}
		if err := out.Close(); err != nil {
			os.Remove(out.Name())
			return "", err
		}
		return out.Name(), nil
	}
}

// runs makes sure a binary starts: `memors-mcp version` must succeed.
func runs(ctx context.Context, bin string) error {
	ctx, cancel := context.WithTimeout(ctx, versionWait)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	if !strings.HasPrefix(string(out), "memors-mcp ") {
		return errors.New("it does not report a memors-mcp version")
	}
	return nil
}

func (c *Client) get(ctx context.Context, url, accept string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if req.URL.Scheme != "https" {
		return nil, fmt.Errorf("refusing a non-https download: %s", req.URL.Redacted())
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach GitHub: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return nil, errors.New("GitHub refused the request (rate limit?); try again in a while")
		}
		return nil, fmt.Errorf("GitHub answered %s for %s", resp.Status, req.URL.Redacted())
	}
	return resp.Body, nil
}

// counter reports download progress at most every 256 KB.
type counter struct {
	w      io.Writer
	done   int64
	total  int64
	last   int64
	report func(Progress)
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.done += int64(n)
	if c.done-c.last >= 256<<10 || (c.total > 0 && c.done == c.total) {
		c.last = c.done
		c.report(Progress{Phase: Downloading, Done: c.done, Total: c.total})
	}
	return n, err
}
