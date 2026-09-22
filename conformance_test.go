package c0

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func vectors(t *testing.T, name string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("c0-spec", "vectors", name))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var doc struct {
		Cases []map[string]any `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	return doc.Cases
}

func hexBytes(s string) []byte {
	b, _ := hex.DecodeString(s)
	return b
}

// A field is a JSON string (UTF-8 bytes) or {"hex": "..."} (raw bytes).
func fieldBytes(f any) []byte {
	switch v := f.(type) {
	case string:
		return []byte(v)
	case map[string]any:
		return hexBytes(v["hex"].(string))
	}
	return nil
}

func checkTable(t *testing.T, name string, tbl *Table, g map[string]any) {
	t.Helper()
	if string(tbl.Name()) != g["name"].(string) {
		t.Errorf("%s: name = %q, want %q", name, tbl.Name(), g["name"])
	}
	if hs, ok := g["headers"].([]any); ok {
		if tbl.HeaderCount() != len(hs) {
			t.Errorf("%s: header count = %d, want %d", name, tbl.HeaderCount(), len(hs))
		}
		for i, h := range hs {
			if string(tbl.Header(i)) != h.(string) {
				t.Errorf("%s: header %d = %q, want %q", name, i, tbl.Header(i), h)
			}
		}
	} else if tbl.HeaderCount() != 0 {
		t.Errorf("%s: expected no headers, got %d", name, tbl.HeaderCount())
	}
	recs := g["records"].([]any)
	if tbl.RecordCount() != len(recs) {
		t.Errorf("%s: record count = %d, want %d", name, tbl.RecordCount(), len(recs))
	}
	for i, rAny := range recs {
		row := rAny.([]any)
		rec := tbl.Record(i)
		if rec.FieldCount() != len(row) {
			t.Errorf("%s: rec %d arity = %d, want %d", name, i, rec.FieldCount(), len(row))
		}
		for j, f := range row {
			if !bytes.Equal(rec.Value(j), fieldBytes(f)) {
				t.Errorf("%s: rec %d field %d = %q, want %q", name, i, j, rec.Value(j), fieldBytes(f))
			}
		}
	}
}

func TestConformanceDecode(t *testing.T) {
	for _, c := range vectors(t, "decode.json") {
		name := c["name"].(string)
		buf := hexBytes(c["bytes"].(string))
		groups := c["groups"].([]any)
		if c["file"] == nil && len(groups) == 1 && groups[0].(map[string]any)["name"] == "" {
			checkTable(t, name, NewTable(buf), groups[0].(map[string]any))
			continue
		}
		doc := NewDocument(buf)
		want := ""
		if c["file"] != nil {
			want = c["file"].(string)
		}
		if string(doc.Name()) != want {
			t.Errorf("%s: doc name = %q, want %q", name, doc.Name(), want)
		}
		if doc.GroupCount() != len(groups) {
			t.Errorf("%s: group count = %d, want %d", name, doc.GroupCount(), len(groups))
		}
		for i, g := range groups {
			checkTable(t, name, doc.Group(i).Table(), g.(map[string]any))
		}
	}
}

func TestConformanceEncode(t *testing.T) {
	for _, c := range vectors(t, "encode.json") {
		name := c["name"].(string)
		spec := c["build"].(map[string]any)
		b := &Builder{}
		if f, ok := spec["file"].(string); ok {
			b.File(f)
		}
		for _, gAny := range spec["groups"].([]any) {
			g := gAny.(map[string]any)
			var headers []string
			if hs, ok := g["headers"].([]any); ok {
				for _, h := range hs {
					headers = append(headers, h.(string))
				}
			}
			b.Group(g["name"].(string), headers)
			for _, rAny := range g["records"].([]any) {
				row := rAny.([]any)
				fields := make([]string, len(row))
				for i, f := range row {
					fields[i] = string(fieldBytes(f))
				}
				b.Record(fields...)
			}
		}
		if b.Err() != nil {
			t.Fatalf("%s: %v", name, b.Err())
		}
		if got := hex.EncodeToString(b.Bytes()); got != c["canonical"].(string) {
			t.Errorf("%s: got %s, want %s", name, got, c["canonical"])
		}
		if !Canonical(b.Bytes()) {
			t.Errorf("%s: builder output is not canonical", name)
		}
	}
}

func TestConformanceCanonical(t *testing.T) {
	for _, c := range vectors(t, "canonical.json") {
		name := c["name"].(string)
		buf := hexBytes(c["bytes"].(string))
		_, err := Tokenize(buf)
		if (err == nil) != c["wellformed"].(bool) {
			t.Errorf("%s: wellformed = %v, want %v", name, err == nil, c["wellformed"])
		}
		if Canonical(buf) != c["canonical"].(bool) {
			t.Errorf("%s: canonical = %v, want %v", name, Canonical(buf), c["canonical"])
		}
	}
}

func TestConformanceInvalid(t *testing.T) {
	for _, c := range vectors(t, "invalid.json") {
		name := c["name"].(string)
		if _, err := Tokenize(hexBytes(c["bytes"].(string))); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestConformanceStream(t *testing.T) {
	for _, c := range vectors(t, "stream.json") {
		name := c["name"].(string)
		buf := hexBytes(c["bytes"].(string))
		r := NewStreamReader(buf)
		if r.CommittedEnd() != int(c["committed_end"].(float64)) {
			t.Errorf("%s: committed_end = %d, want %v", name, r.CommittedEnd(), c["committed_end"])
		}
		if r.Torn() != c["torn"].(bool) {
			t.Errorf("%s: torn = %v, want %v", name, r.Torn(), c["torn"])
		}
		blocks := c["blocks"].([]any)
		if r.BlockCount() != len(blocks) {
			t.Errorf("%s: block count = %d, want %d", name, r.BlockCount(), len(blocks))
		}
		for i, h := range blocks {
			if got := hex.EncodeToString(r.Block(i)); got != h.(string) {
				t.Errorf("%s: block %d = %s, want %s", name, i, got, h)
			}
		}
		if recsAny, ok := c["records"].([]any); ok {
			tbl := r.Table()
			if tbl.RecordCount() != len(recsAny) {
				t.Errorf("%s: record count = %d, want %d", name, tbl.RecordCount(), len(recsAny))
			}
			for i, rAny := range recsAny {
				row := rAny.([]any)
				rec := tbl.Record(i)
				for j, f := range row {
					if string(rec.Value(j)) != f.(string) {
						t.Errorf("%s: rec %d field %d = %q, want %q", name, i, j, rec.Value(j), f)
					}
				}
			}
		}
	}
}

func TestConformanceList(t *testing.T) {
	for _, c := range vectors(t, "list.json") {
		name := c["name"].(string)
		buf := hexBytes(c["bytes"].(string))
		record := c["record"].([]any)
		rec := NewTable(buf).Record(0)
		if rec.FieldCount() != len(record) {
			t.Errorf("%s: arity = %d, want %d", name, rec.FieldCount(), len(record))
		}
		for i, f := range record {
			items, isList := f.([]any)
			if !isList {
				if !bytes.Equal(rec.Value(i), fieldBytes(f)) {
					t.Errorf("%s: field %d = %q, want %q", name, i, rec.Value(i), fieldBytes(f))
				}
				continue
			}
			got := rec.List(i)
			if len(got) != len(items) {
				t.Errorf("%s: field %d list len = %d, want %d", name, i, len(got), len(items))
				continue
			}
			for j, item := range items {
				if !bytes.Equal(got[j], fieldBytes(item)) {
					t.Errorf("%s: field %d item %d = %q, want %q", name, i, j, got[j], fieldBytes(item))
				}
			}
		}
		if !c["canonical"].(bool) {
			continue
		}
		b := &Builder{}
		b.Record(string(fieldBytes(record[0])))
		for _, f := range record[1:] {
			if items, isList := f.([]any); isList {
				strs := make([]string, len(items))
				for j, item := range items {
					strs[j] = string(fieldBytes(item))
				}
				b.ListField(strs...)
			} else {
				b.Field(string(fieldBytes(f)))
			}
		}
		if b.Err() != nil {
			t.Fatalf("%s: %v", name, b.Err())
		}
		if got := hex.EncodeToString(b.Bytes()); got != c["bytes"].(string) {
			t.Errorf("%s: got %s, want %s", name, got, c["bytes"])
		}
		if !Canonical(b.Bytes()) {
			t.Errorf("%s: builder output is not canonical", name)
		}
	}
}
