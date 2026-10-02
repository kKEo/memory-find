package embedding

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// StaticEmbedder runs a model2vec-style static model: a lookup table with
// one vector per vocabulary token, mean-pooled over a text's WordPiece
// tokens and normalised. No neural network, so it is instant and pure Go;
// it has no notion of word order or context, so it is weaker on paraphrase.
// It is the instant tier and the fallback when no ONNX model can run.
type StaticEmbedder struct {
	info   ModelInfo
	vocab  map[string]int32
	table  []float32 // rows × dim
	dim    int
	unkID  int32
	prefix string // continuing-subword prefix, "##"
	mu     sync.Mutex
}

// NewStaticEmbedder downloads (once) and loads a static model.
func NewStaticEmbedder(ctx context.Context, info ModelInfo, modelDir string) (*StaticEmbedder, error) {
	dir, err := ensureStaticFiles(ctx, info, modelDir)
	if err != nil {
		return nil, err
	}
	return LoadStaticModel(info, filepath.Join(dir, "model.safetensors"), filepath.Join(dir, "tokenizer.json"))
}

// LoadStaticModel reads the two files a static model consists of.
func LoadStaticModel(info ModelInfo, safetensorsPath, tokenizerPath string) (*StaticEmbedder, error) {
	table, rows, dim, err := readSafetensors2D(safetensorsPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", safetensorsPath, err)
	}
	vocab, unk, prefix, err := readWordPieceVocab(tokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tokenizerPath, err)
	}
	if len(vocab) > rows {
		return nil, fmt.Errorf("tokenizer has %d tokens but the table has %d rows", len(vocab), rows)
	}
	e := &StaticEmbedder{info: info, vocab: vocab, table: table, dim: dim, unkID: unk, prefix: prefix}
	e.info.Dim = dim
	return e, nil
}

func (e *StaticEmbedder) Info() ModelInfo { return e.info }

func (e *StaticEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	out, err := e.EmbedBatch(ctx, []string{text}, RoleDocument)
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

func (e *StaticEmbedder) EmbedBatch(_ context.Context, texts []string, role Role) ([][]float32, error) {
	if e == nil || e.table == nil {
		return nil, errEmbedderUnavailable
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.embedOne(applyPrefix(e.info, role, t))
	}
	return out, nil
}

func (e *StaticEmbedder) embedOne(text string) []float32 {
	ids := e.tokenize(text)
	vec := make([]float32, e.dim)
	if len(ids) == 0 {
		return vec
	}
	for _, id := range ids {
		row := e.table[int(id)*e.dim : int(id+1)*e.dim]
		for j, x := range row {
			vec[j] += x
		}
	}
	var norm float64
	for _, x := range vec {
		norm += float64(x) * float64(x)
	}
	if norm > 0 {
		inv := float32(1 / math.Sqrt(norm))
		for j := range vec {
			vec[j] *= inv
		}
	}
	return vec
}

// tokenize is BERT-style: lowercase, split on whitespace and punctuation,
// then greedy longest-match WordPiece with the continuing-subword prefix.
func (e *StaticEmbedder) tokenize(text string) []int32 {
	var ids []int32
	for _, word := range bertPreTokenize(strings.ToLower(text)) {
		runes := []rune(word)
		start := 0
		var pieces []int32
		for start < len(runes) {
			end := len(runes)
			found := int32(-1)
			for end > start {
				piece := string(runes[start:end])
				if start > 0 {
					piece = e.prefix + piece
				}
				if id, ok := e.vocab[piece]; ok {
					found = id
					break
				}
				end--
			}
			if found < 0 {
				pieces = []int32{e.unkID}
				break
			}
			pieces = append(pieces, found)
			start = end
		}
		ids = append(ids, pieces...)
	}
	return ids
}

func bertPreTokenize(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			flush()
		case unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.Is(unicode.Han, r):
			flush()
			out = append(out, string(r))
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// readWordPieceVocab reads a Hugging Face tokenizer.json with a WordPiece model.
func readWordPieceVocab(path string) (map[string]int32, int32, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, "", err
	}
	var tok struct {
		Model struct {
			Type   string           `json:"type"`
			Vocab  map[string]int32 `json:"vocab"`
			Unk    string           `json:"unk_token"`
			Prefix string           `json:"continuing_subword_prefix"`
		} `json:"model"`
	}
	if err := json.Unmarshal(b, &tok); err != nil {
		return nil, 0, "", err
	}
	if tok.Model.Type != "WordPiece" {
		return nil, 0, "", fmt.Errorf("tokenizer model %q is not WordPiece", tok.Model.Type)
	}
	prefix := tok.Model.Prefix
	if prefix == "" {
		prefix = "##"
	}
	unk, ok := tok.Model.Vocab[tok.Model.Unk]
	if !ok {
		unk = 0
	}
	return tok.Model.Vocab, unk, prefix, nil
}

// readSafetensors2D reads the first 2-D F32 or F16 tensor from a safetensors
// file: an 8-byte little-endian header length, a JSON header, then raw data.
func readSafetensors2D(path string) ([]float32, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()
	var hdrLen uint64
	if err := binary.Read(f, binary.LittleEndian, &hdrLen); err != nil {
		return nil, 0, 0, err
	}
	if hdrLen > 1<<24 {
		return nil, 0, 0, fmt.Errorf("implausible header length %d", hdrLen)
	}
	hdr := make([]byte, hdrLen)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return nil, 0, 0, err
	}
	var header map[string]struct {
		Dtype   string  `json:"dtype"`
		Shape   []int   `json:"shape"`
		Offsets []int64 `json:"data_offsets"`
	}
	if err := json.Unmarshal(hdr, &header); err != nil {
		return nil, 0, 0, fmt.Errorf("parse header: %w", err)
	}
	for name, t := range header {
		if name == "__metadata__" || len(t.Shape) != 2 {
			continue
		}
		rows, dim := t.Shape[0], t.Shape[1]
		if _, err := f.Seek(int64(8+hdrLen)+t.Offsets[0], io.SeekStart); err != nil {
			return nil, 0, 0, err
		}
		n := rows * dim
		out := make([]float32, n)
		switch t.Dtype {
		case "F32":
			buf := make([]byte, 4*n)
			if _, err := io.ReadFull(f, buf); err != nil {
				return nil, 0, 0, err
			}
			for i := range out {
				out[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4*i:]))
			}
		case "F16":
			buf := make([]byte, 2*n)
			if _, err := io.ReadFull(f, buf); err != nil {
				return nil, 0, 0, err
			}
			for i := range out {
				out[i] = float16to32(binary.LittleEndian.Uint16(buf[2*i:]))
			}
		default:
			return nil, 0, 0, fmt.Errorf("tensor %s has unsupported dtype %s", name, t.Dtype)
		}
		return out, rows, dim, nil
	}
	return nil, 0, 0, fmt.Errorf("no 2-D tensor found")
}

func float16to32(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := int32((h >> 10) & 0x1f)
	mant := uint32(h & 0x3ff)
	switch exp {
	case 0:
		if mant == 0 {
			return math.Float32frombits(sign)
		}
		// subnormal
		f := float32(mant) / 1024 * float32(math.Pow(2, -14))
		if sign != 0 {
			return -f
		}
		return f
	case 0x1f:
		if mant == 0 {
			return math.Float32frombits(sign | 0x7f800000)
		}
		return math.Float32frombits(sign | 0x7fc00000)
	}
	return math.Float32frombits(sign | uint32(exp+112)<<23 | mant<<13)
}

// ensureStaticFiles downloads the static model's files once into
// modelDir/<repo>, with the same sentinel convention as the ONNX models.
func ensureStaticFiles(ctx context.Context, info ModelInfo, modelDir string) (string, error) {
	dir := filepath.Join(modelDir, sanitizedModelDirName(info.HFRepo))
	sentinel := dir + ".ok"
	if isModelReady(dir, sentinel) {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "Downloading embedding model %s (first run only)...\n", info.HFRepo)
	client := &http.Client{Timeout: 30 * time.Minute}
	for _, name := range info.Files {
		url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", info.HFRepo, name)
		if err := downloadFile(ctx, client, url, filepath.Join(dir, name)); err != nil {
			return "", fmt.Errorf("download %s: %w", name, err)
		}
	}
	if err := os.WriteFile(sentinel, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

func downloadFile(ctx context.Context, client *http.Client, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

var _ Embedder = (*StaticEmbedder)(nil)
