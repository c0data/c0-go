package c0

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Runs the shared conformance vectors from the c0-spec submodule
// (https://github.com/c0data/c0-spec), the source of truth. Schema v2: see
// c0-spec/vectors/README.md.

func vectors(t *testing.T, name string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("c0-spec", "vectors", name))
	if err != nil {
		t.Fatalf("read vectors: %v (run `git submodule update --init`)", err)
	}
	var doc struct {
		Version int              `json:"version"`
		Cases   []map[string]any `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	if doc.Version != 2 {
		t.Fatalf("%s: vector schema v%d, want v2", name, doc.Version)
	}
	return doc.Cases
}

func hexBytes(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// A scalar expected value: JSON string or {"hex": ...}.
func isScalar(f any) bool {
	switch v := f.(type) {
	case string:
		return true
	case map[string]any:
		_, ok := v["hex"]
		return ok
	}
	return false
}

func scalarBytes(f any) []byte {
	switch v := f.(type) {
	case string:
		return []byte(v)
	case map[string]any:
		return hexBytes(v["hex"].(string))
	}
	return nil
}

func strs(a []any) []string {
	out := make([]string, len(a))
	for i, x := range a {
		out[i] = x.(string)
	}
	return out
}

// flatGroup is a group with the depth it should have in the reader.
type flatGroup struct {
	depth int
	g     map[string]any
}

// flatGroups flattens a group list with "sections" into (depth, group) pairs.
func flatGroups(groups []any, depth int) []flatGroup {
	var out []flatGroup
	for _, gAny := range groups {
		g := gAny.(map[string]any)
		out = append(out, flatGroup{depth, g})
		if secs, ok := g["sections"].([]any); ok {
			out = append(out, flatGroups(secs, depth+1)...)
		}
	}
	return out
}

func checkValue(t *testing.T, rec *Record, i int, f any) {
	t.Helper()
	switch {
	case isScalar(f):
		if got := rec.Value(i); !bytes.Equal(got, scalarBytes(f)) {
			t.Errorf("field %d = %q, want %q", i, got, scalarBytes(f))
		}
	case f.(map[string]any)["level"] != nil:
		body, err := rec.Level(i)
		if err != nil {
			t.Fatalf("field %d: %v", i, err)
		}
		checkBody(t, body, f.(map[string]any)["level"].(map[string]any))
	case f.(map[string]any)["ref"] != nil:
		r, err := rec.Ref(i)
		if err != nil {
			t.Fatalf("field %d: %v", i, err)
		}
		var want []string
		if s, ok := f.(map[string]any)["ref"].(string); ok {
			want = []string{s}
		} else {
			want = strs(f.(map[string]any)["ref"].([]any))
		}
		if len(r.Path) != len(want) {
			t.Fatalf("field %d: ref path %q, want %q", i, r.Path, want)
		}
		for j := range want {
			if string(r.Path[j]) != want[j] {
				t.Errorf("field %d: ref segment %d = %q, want %q", i, j, r.Path[j], want[j])
			}
		}
	default:
		t.Fatalf("unknown value form %v", f)
	}
}

func checkRecord(t *testing.T, rec *Record, r any) {
	t.Helper()
	if arr, ok := r.([]any); ok {
		if rec.IsKeyed() {
			t.Fatalf("record is keyed, want positional")
		}
		if rec.FieldCount() != len(arr) {
			t.Fatalf("field count = %d, want %d (%q)", rec.FieldCount(), len(arr), rec.Raw())
		}
		for j, f := range arr {
			checkValue(t, rec, j, f)
		}
		return
	}
	if !rec.IsKeyed() {
		t.Fatalf("record is positional, want keyed")
	}
	pairs := r.(map[string]any)["keys"].([]any)
	got := rec.Pairs()
	if len(got) != len(pairs) {
		t.Fatalf("pairs = %d, want %d", len(got), len(pairs))
	}
	for j, pAny := range pairs {
		p := pAny.([]any)
		if string(got[j].Key) != p[0].(string) {
			t.Errorf("key %d = %q, want %q", j, got[j].Key, p[0])
		}
		if !bytes.Equal(got[j].Value, scalarBytes(p[1])) {
			t.Errorf("value for %q = %q, want %q", p[0], got[j].Value, scalarBytes(p[1]))
		}
	}
}

func checkGroup(t *testing.T, g *Group, e map[string]any) {
	t.Helper()
	if string(g.Name()) != e["name"].(string) {
		t.Errorf("group name = %q, want %q", g.Name(), e["name"])
	}
	if headers, ok := e["headers"].([]any); ok {
		want := strs(headers)
		got := g.Headers()
		if len(got) != len(want) {
			t.Fatalf("headers = %q, want %q", got, want)
		}
		for i := range want {
			if string(got[i]) != want[i] {
				t.Errorf("header %d = %q, want %q", i, got[i], want[i])
			}
		}
	} else if g.HasHeader() {
		t.Errorf("unexpected header row %q", g.Headers())
	}
	records := e["records"].([]any)
	if g.RecordCount() != len(records) {
		t.Fatalf("record count = %d, want %d", g.RecordCount(), len(records))
	}
	for i, r := range records {
		checkRecord(t, g.Record(i), r)
	}
}

func checkGroups(t *testing.T, groups []*Group, expected []any) {
	t.Helper()
	flat := flatGroups(expected, 1)
	if len(groups) != len(flat) {
		t.Fatalf("group count = %d, want %d", len(groups), len(flat))
	}
	for i, fg := range flat {
		checkGroup(t, groups[i], fg.g)
		// the unnamed leading group has depth 0 in the reader
		if !(fg.g["name"].(string) == "" && i == 0) && groups[i].Depth() != fg.depth {
			t.Errorf("group %d depth = %d, want %d", i, groups[i].Depth(), fg.depth)
		}
	}
}

func checkBody(t *testing.T, body *Body, e map[string]any) {
	t.Helper()
	if files, ok := e["files"].([]any); ok {
		checkFiles(t, body, files)
		return
	}
	if records, ok := e["records"].([]any); ok {
		// a level described as a single table
		if len(body.Files()) != 1 {
			t.Fatalf("files = %d, want 1", len(body.Files()))
		}
		gs := body.Files()[0].Groups()
		_, hasHeaders := e["headers"].([]any)
		if len(records) == 0 && !hasHeaders {
			if len(gs) != 0 {
				t.Fatalf("groups = %d, want 0", len(gs))
			}
			return
		}
		if len(gs) != 1 {
			t.Fatalf("groups = %d, want 1", len(gs))
		}
		checkGroup(t, gs[0], map[string]any{"name": "", "headers": e["headers"], "records": records})
		return
	}
	checkGroups(t, body.Groups(), e["groups"].([]any))
}

func checkFiles(t *testing.T, body *Body, files []any) {
	t.Helper()
	if len(body.Files()) != len(files) {
		t.Fatalf("files = %d, want %d", len(body.Files()), len(files))
	}
	for i, fAny := range files {
		f := fAny.(map[string]any)
		if string(body.Files()[i].Name()) != f["name"].(string) {
			t.Errorf("file %d name = %q, want %q", i, body.Files()[i].Name(), f["name"])
		}
		checkGroups(t, body.Files()[i].Groups(), f["groups"].([]any))
	}
}

func checkHeading(t *testing.T, h *Heading, e []any) {
	t.Helper()
	pairs := h.Pairs()
	if len(pairs) != len(e) {
		t.Fatalf("heading pairs = %d, want %d", len(pairs), len(e))
	}
	for i, pAny := range e {
		p := pAny.([]any)
		if string(pairs[i].Key) != p[0].(string) {
			t.Errorf("heading key %d = %q, want %q", i, pairs[i].Key, p[0])
		}
		if isScalar(p[1]) {
			if !bytes.Equal(pairs[i].Value, scalarBytes(p[1])) {
				t.Errorf("heading value %q = %q, want %q", p[0], pairs[i].Value, scalarBytes(p[1]))
			}
		} else if lvl, ok := p[1].(map[string]any)["level"].(map[string]any); ok {
			raw := pairs[i].Value
			body, err := levelOf(raw)
			if err != nil {
				t.Fatalf("heading value %q: %v", p[0], err)
			}
			checkBody(t, body, lvl)
		}
	}
}

// ---------------------------------------------------------------- building

func buildValue(b *Builder, f any) {
	switch {
	case isScalar(f):
		b.Field(string(scalarBytes(f)))
	case f.(map[string]any)["level"] != nil:
		b.Level(func(b *Builder) { buildBody(b, f.(map[string]any)["level"].(map[string]any)) })
	case f.(map[string]any)["ref"] != nil:
		if s, ok := f.(map[string]any)["ref"].(string); ok {
			b.Ref(s)
		} else {
			b.RefPath(strs(f.(map[string]any)["ref"].([]any))...)
		}
	case f.(map[string]any)["map"] != nil:
		b.Level(func(b *Builder) { buildRecord(b, map[string]any{"keys": f.(map[string]any)["map"]}) })
	default:
		panic("unknown value form")
	}
}

func buildRecord(b *Builder, r any) {
	b.Record()
	if arr, ok := r.([]any); ok {
		for _, f := range arr {
			buildValue(b, f)
		}
		return
	}
	for _, pAny := range r.(map[string]any)["keys"].([]any) {
		p := pAny.([]any)
		k := p[0].(string)
		v := p[1]
		switch {
		case isScalar(v):
			b.Key(k, string(scalarBytes(v)))
		case v.(map[string]any)["map"] != nil:
			b.KeyLevel(k, func(b *Builder) { buildRecord(b, map[string]any{"keys": v.(map[string]any)["map"]}) })
		case v.(map[string]any)["level"] != nil:
			b.KeyLevel(k, func(b *Builder) { buildBody(b, v.(map[string]any)["level"].(map[string]any)) })
		}
	}
}

func buildGroups(b *Builder, groups []any) {
	for i, gAny := range groups {
		g := gAny.(map[string]any)
		var headers []string
		if hs, ok := g["headers"].([]any); ok {
			headers = strs(hs)
		}
		if g["name"].(string) == "" && i == 0 {
			if headers != nil {
				b.Header(headers...) // the unnamed leading group: no GS
			}
		} else {
			b.Group(g["name"].(string), headers)
		}
		for _, r := range g["records"].([]any) {
			buildRecord(b, r)
		}
	}
}

func buildBody(b *Builder, e map[string]any) {
	if records, ok := e["records"].([]any); ok {
		if hs, ok := e["headers"].([]any); ok {
			b.Header(strs(hs)...)
		}
		for _, r := range records {
			buildRecord(b, r)
		}
	} else if groups, ok := e["groups"].([]any); ok {
		buildGroups(b, groups)
	}
}

// ================================================================== tests

func TestConformanceDecode(t *testing.T) {
	for _, c := range vectors(t, "decode.json") {
		c := c
		t.Run(c["name"].(string), func(t *testing.T) {
			buf := hexBytes(c["bytes"].(string))
			cont, err := ParseContainer(buf)
			if err != nil {
				t.Fatal(err)
			}
			if docs, ok := c["documents"].([]any); ok {
				if len(cont.Documents()) != len(docs) {
					t.Fatalf("documents = %d, want %d", len(cont.Documents()), len(docs))
				}
				for i, dAny := range docs {
					d := dAny.(map[string]any)
					doc := cont.Documents()[i]
					if h, ok := d["heading"].([]any); ok {
						checkHeading(t, doc.Heading(), h)
					}
					want, _ := d["file"].(string)
					if string(doc.Name()) != want {
						t.Errorf("doc %d name = %q, want %q", i, doc.Name(), want)
					}
					checkGroups(t, doc.Groups(), d["groups"].([]any))
				}
				return
			}
			doc := cont.First()
			if h, ok := c["heading"].([]any); ok {
				if !doc.HasSYN() {
					t.Error("expected SYN")
				}
				checkHeading(t, doc.Heading(), h)
			}
			if files, ok := c["files"].([]any); ok {
				checkFiles(t, doc.Body(), files)
			} else {
				want, _ := c["file"].(string)
				if string(doc.Name()) != want {
					t.Errorf("doc name = %q, want %q", doc.Name(), want)
				}
				checkGroups(t, doc.Groups(), c["groups"].([]any))
			}
			if tail, ok := c["tail"].(string); ok {
				if !cont.HasEM() {
					t.Error("expected EM")
				}
				if got := hex.EncodeToString(cont.Tail()); got != tail {
					t.Errorf("tail = %s, want %s", got, tail)
				}
			}
		})
	}
}

func TestConformanceEncode(t *testing.T) {
	for _, c := range vectors(t, "encode.json") {
		c := c
		t.Run(c["name"].(string), func(t *testing.T) {
			build := c["build"].(map[string]any)
			emit := func(b *Builder) {
				if f, ok := build["file"].(string); ok {
					b.File(f)
				}
				buildGroups(b, build["groups"].([]any))
			}
			canonical, err := Build(emit)
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(canonical); got != c["canonical"].(string) {
				t.Errorf("canonical = %s, want %s", got, c["canonical"])
			}
			if !IsCanonical(canonical) {
				t.Error("builder output is not canonical")
			}
			if h, ok := build["heading"].([]any); ok {
				whole, err := Build(func(b *Builder) {
					b.SYN()
					for _, pAny := range h {
						p := pAny.([]any)
						b.Heading(p[0].(string), p[1].(string))
					}
					emit(b)
					b.EOT()
				})
				if err != nil {
					t.Fatal(err)
				}
				if got := hex.EncodeToString(whole); got != c["bytes"].(string) {
					t.Errorf("bytes = %s, want %s", got, c["bytes"])
				}
			}
		})
	}
}

func TestConformanceCanonical(t *testing.T) {
	for _, c := range vectors(t, "canonical.json") {
		c := c
		t.Run(c["name"].(string), func(t *testing.T) {
			buf := hexBytes(c["bytes"].(string))
			err := Validate(buf)
			if (err == nil) != c["wellformed"].(bool) {
				t.Errorf("wellformed = %v (%v), want %v", err == nil, err, c["wellformed"])
			}
			if IsCanonical(buf) != c["canonical"].(bool) {
				t.Errorf("canonical = %v, want %v", IsCanonical(buf), c["canonical"])
			}
		})
	}
}

func TestConformanceInvalid(t *testing.T) {
	for _, c := range vectors(t, "invalid.json") {
		c := c
		t.Run(c["name"].(string), func(t *testing.T) {
			buf := hexBytes(c["bytes"].(string))
			if err := Validate(buf); err == nil {
				t.Error("expected an error")
			}
			if _, err := ParseContainer(buf); err == nil {
				t.Error("ParseContainer: expected an error")
			}
		})
	}
}

func TestConformanceNested(t *testing.T) {
	isScalarList := func(items []any) bool {
		for _, x := range items {
			if !isScalar(x) {
				return false
			}
		}
		return true
	}
	checkList := func(t *testing.T, got [][]byte, items []any) {
		t.Helper()
		if len(got) != len(items) {
			t.Fatalf("list = %q, want %d items", got, len(items))
		}
		for j, x := range items {
			if !bytes.Equal(got[j], scalarBytes(x)) {
				t.Errorf("item %d = %q, want %q", j, got[j], scalarBytes(x))
			}
		}
	}
	for _, c := range vectors(t, "nested.json") {
		c := c
		t.Run(c["name"].(string), func(t *testing.T) {
			buf := hexBytes(c["bytes"].(string))
			expected := c["record"].([]any)
			body, err := ParseBody(buf)
			if err != nil {
				t.Fatal(err)
			}
			rec := body.Record(0)
			if rec.FieldCount() != len(expected) {
				t.Fatalf("field count = %d, want %d", rec.FieldCount(), len(expected))
			}
			for i, f := range expected {
				switch {
				case isScalar(f):
					checkValue(t, rec, i, f)
				case f.(map[string]any)["list"] != nil:
					items := f.(map[string]any)["list"].([]any)
					if isScalarList(items) {
						got, err := rec.List(i)
						if err != nil {
							t.Fatalf("field %d: %v", i, err)
						}
						checkList(t, got, items)
					} else {
						lvl, err := rec.Level(i)
						if err != nil {
							t.Fatalf("field %d: %v", i, err)
						}
						inner := lvl.Record(0)
						if inner.FieldCount() != len(items) {
							t.Fatalf("inner field count = %d, want %d", inner.FieldCount(), len(items))
						}
						for j, x := range items {
							if isScalar(x) {
								checkValue(t, inner, j, x)
							} else {
								got, err := inner.List(j)
								if err != nil {
									t.Fatalf("inner field %d: %v", j, err)
								}
								checkList(t, got, x.(map[string]any)["list"].([]any))
							}
						}
					}
				case f.(map[string]any)["level"] != nil:
					if _, err := rec.List(i); err == nil {
						t.Errorf("field %d: List accepted a level that is not a list", i)
					}
					checkValue(t, rec, i, f)
				}
			}
			if !c["canonical"].(bool) {
				return
			}
			listItems := func(items []any) []string {
				out := make([]string, len(items))
				for j, x := range items {
					out[j] = string(scalarBytes(x))
				}
				return out
			}
			built, err := Build(func(b *Builder) {
				b.Record()
				for _, f := range expected {
					items, isList := func() ([]any, bool) {
						if isScalar(f) {
							return nil, false
						}
						l, ok := f.(map[string]any)["list"].([]any)
						return l, ok
					}()
					switch {
					case isList && isScalarList(items):
						b.List(listItems(items)...)
					case isList:
						b.Level(func(b *Builder) {
							b.Record()
							for _, x := range items {
								if isScalar(x) {
									b.Field(string(scalarBytes(x)))
								} else {
									b.List(listItems(x.(map[string]any)["list"].([]any))...)
								}
							}
						})
					default:
						buildValue(b, f)
					}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(built); got != c["bytes"].(string) {
				t.Errorf("built = %s, want %s", got, c["bytes"])
			}
			if !IsCanonical(built) {
				t.Error("builder output is not canonical")
			}
		})
	}
}

func TestConformanceStream(t *testing.T) {
	for _, c := range vectors(t, "stream.json") {
		c := c
		t.Run(c["name"].(string), func(t *testing.T) {
			buf := hexBytes(c["bytes"].(string))
			r := NewStreamReader(buf)
			if r.CommittedEnd() != int(c["committed_end"].(float64)) {
				t.Errorf("committed_end = %d, want %v", r.CommittedEnd(), c["committed_end"])
			}
			if r.Torn() != c["torn"].(bool) {
				t.Errorf("torn = %v, want %v", r.Torn(), c["torn"])
			}
			blocks := c["blocks"].([]any)
			if r.BlockCount() != len(blocks) {
				t.Fatalf("block count = %d, want %d", r.BlockCount(), len(blocks))
			}
			for i, h := range blocks {
				if got := hex.EncodeToString(r.Block(i)); got != h.(string) {
					t.Errorf("block %d = %s, want %s", i, got, h)
				}
			}
			var damaged []int
			if ds, ok := c["damaged"].([]any); ok {
				for _, d := range ds {
					damaged = append(damaged, int(d.(float64)))
				}
			}
			if len(r.Damaged()) != len(damaged) {
				t.Errorf("damaged = %v, want %v", r.Damaged(), damaged)
			} else {
				for i := range damaged {
					if r.Damaged()[i] != damaged[i] {
						t.Errorf("damaged = %v, want %v", r.Damaged(), damaged)
					}
				}
			}
			if recs, ok := c["records"].([]any); ok {
				got, err := r.Records()
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(recs) {
					t.Fatalf("records = %d, want %d", len(got), len(recs))
				}
				for i, rAny := range recs {
					want := strs(rAny.([]any))
					vals := got[i].Values()
					if len(vals) != len(want) {
						t.Fatalf("record %d = %q, want %q", i, vals, want)
					}
					for j := range want {
						if string(vals[j]) != want[j] {
							t.Errorf("record %d field %d = %q, want %q", i, j, vals[j], want[j])
						}
					}
				}
			}
		})
	}
}
