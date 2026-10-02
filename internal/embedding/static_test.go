package embedding

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writeTinyStaticModel writes a 6-token WordPiece vocab and a matching
// safetensors table so the loader and tokenizer can be tested offline.
func writeTinyStaticModel(t *testing.T, dtype string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	vocab := map[string]int32{"[UNK]": 0, "inter": 1, "##cept": 2, "##ors": 3, "order": 4, "walk": 5}
	tok := map[string]any{"model": map[string]any{"type": "WordPiece", "vocab": vocab, "unk_token": "[UNK]", "continuing_subword_prefix": "##"}}
	tb, _ := json.Marshal(tok)
	tokPath := filepath.Join(dir, "tokenizer.json")
	if err := os.WriteFile(tokPath, tb, 0o644); err != nil {
		t.Fatal(err)
	}

	rows, dim := 6, 4
	vals := make([]float32, rows*dim)
	for r := 0; r < rows; r++ {
		vals[r*dim+r%dim] = 1 // one-hot-ish rows
	}
	var data []byte
	if dtype == "F32" {
		data = make([]byte, 4*len(vals))
		for i, v := range vals {
			binary.LittleEndian.PutUint32(data[4*i:], math.Float32bits(v))
		}
	} else {
		data = make([]byte, 2*len(vals))
		for i, v := range vals {
			h := uint16(0)
			if v == 1 {
				h = 0x3c00 // 1.0 in float16
			}
			binary.LittleEndian.PutUint16(data[2*i:], h)
		}
	}
	hdr, _ := json.Marshal(map[string]any{"embeddings": map[string]any{"dtype": dtype, "shape": []int{rows, dim}, "data_offsets": []int{0, len(data)}}})
	var file []byte
	lenb := make([]byte, 8)
	binary.LittleEndian.PutUint64(lenb, uint64(len(hdr)))
	file = append(file, lenb...)
	file = append(file, hdr...)
	file = append(file, data...)
	stPath := filepath.Join(dir, "model.safetensors")
	if err := os.WriteFile(stPath, file, 0o644); err != nil {
		t.Fatal(err)
	}
	return stPath, tokPath
}

func TestStaticEmbedderTokenizesAndPools(t *testing.T) {
	for _, dtype := range []string{"F32", "F16"} {
		st, tok := writeTinyStaticModel(t, dtype)
		e, err := LoadStaticModel(ModelInfo{ID: "tiny", Static: true, Normalize: true}, st, tok)
		if err != nil {
			t.Fatalf("%s: %v", dtype, err)
		}
		ids := e.tokenize("Interceptors order!")
		// inter + ##cept + ##ors, order, "!" -> UNK
		if len(ids) != 5 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 || ids[3] != 4 || ids[4] != 0 {
			t.Fatalf("%s: tokens = %v", dtype, ids)
		}
		v, err := e.Embed(context.Background(), "walk")
		if err != nil || len(v) != 4 {
			t.Fatal(err)
		}
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if math.Abs(norm-1) > 1e-5 {
			t.Fatalf("%s: not unit length: %v", dtype, v)
		}
		if e.Info().Dim != 4 {
			t.Fatalf("dim = %d", e.Info().Dim)
		}
	}
}

func TestRegistryLookupAndTruncate(t *testing.T) {
	if _, err := LookupModel("minilm"); err != nil {
		t.Fatal(err)
	}
	if _, err := LookupModel("nope"); err == nil {
		t.Fatal("unknown model accepted")
	}
	for _, m := range Known {
		if m.ID == "" || m.Licence == "" || m.Dim == 0 || (!m.Static && m.OnnxPath == "") {
			t.Errorf("incomplete registry entry: %+v", m)
		}
	}
	v := TruncateNormalize([]float32{3, 4, 100, 100}, 2)
	if len(v) != 2 || math.Abs(float64(v[0])-0.6) > 1e-6 || math.Abs(float64(v[1])-0.8) > 1e-6 {
		t.Fatalf("truncate = %v", v)
	}
}
