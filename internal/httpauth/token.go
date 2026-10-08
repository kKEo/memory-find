// Package httpauth protects `memo-mcp serve --http`: one shared secret,
// accepted as an `Authorization: Bearer` header (MCP clients, Prometheus)
// or, for browsers, as a session cookie set by /login. The token lives in a
// 0600 file under MEMO_HOME; rotating it invalidates every cookie.
package httpauth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// minTokenLen rejects hand-written tokens too short to resist guessing.
const minTokenLen = 32

// NewToken returns 32 random bytes, base64url-encoded (43 characters).
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// LoadOrCreate reads the token at path, creating it (0600, parent 0700)
// when missing. Like ssh with a private key, it refuses a file other users
// can read.
func LoadOrCreate(path string) (token string, created bool, err error) {
	tok, err := Load(path)
	if err == nil {
		return tok, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", false, fmt.Errorf("create token dir: %w", err)
	}
	tok = NewToken()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		// Another process created it first: use theirs.
		tok, err := Load(path)
		return tok, false, err
	}
	if err != nil {
		return "", false, fmt.Errorf("create token file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(tok + "\n"); err != nil {
		return "", false, fmt.Errorf("write token file: %w", err)
	}
	return tok, true, f.Close()
}

// Load reads an existing token file.
func Load(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s is readable by other users (mode %o); run: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if len(tok) < minTokenLen {
		return "", fmt.Errorf("token in %s is shorter than %d characters", path, minTokenLen)
	}
	return tok, nil
}

// Rotate replaces the token at path with a new one and returns it. The
// write is atomic, so a server starting concurrently reads old or new.
func Rotate(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tok := NewToken()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".http-token-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(tok + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return tok, os.Rename(tmp.Name(), path)
}
