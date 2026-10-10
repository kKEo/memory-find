package setup

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/kKEo/memory-find/tray/internal/config"
)

// Model is an embedding model memo-mcp can use (MEMO_MODEL).
type Model struct {
	ID      string
	Licence string
	Note    string
	Default bool // the registry default, used when MEMO_MODEL is unset
}

// Label is how a model reads in a list: its id and the start of its note.
func (m Model) Label() string {
	note := m.Note
	if i := strings.Index(note, ";"); i > 0 {
		note = note[:i]
	}
	if r := []rune(note); len(r) > 70 {
		note = string(r[:69]) + "…"
	}
	return m.ID + " — " + note
}

var columns = regexp.MustCompile(`\s{2,}`)

// ParseModels reads the table `memo-mcp model ls` prints: one row per
// model under an "id" header, up to the first blank line; the row marked
// "*" is the one in use.
func ParseModels(out string) []Model {
	var models []Model
	inTable := false
	for _, line := range strings.Split(out, "\n") {
		if !inTable {
			inTable = strings.HasPrefix(line, "id ")
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		f := columns.Split(strings.TrimSpace(line), 5)
		if len(f) < 5 {
			continue
		}
		note := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(f[4]), "(kb default)"))
		selected := strings.HasSuffix(note, "*")
		models = append(models, Model{ID: f[0], Licence: f[2], Note: strings.TrimSpace(strings.TrimSuffix(note, "*")), Default: selected})
	}
	return models
}

// ListModels asks memo-mcp which models it knows. MEMO_MODEL is left
// unset, so the marked row is the registry default.
func ListModels(homeDir string) func(ctx context.Context, bin string) ([]Model, error) {
	return func(ctx context.Context, bin string) ([]Model, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "model", "ls")
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			if k != "MEMO_MODEL" && !config.Reserved(k) {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, "MEMO_HOME="+homeDir)
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		return ParseModels(string(out)), nil
	}
}
