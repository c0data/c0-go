package c0

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildReadRoundtrip(t *testing.T) {
	buf, err := Build(func(b *Builder) {
		b.Group("users", []string{"name", "amount"})
		b.Record("Alice", "1502.30")
		b.Record("Bob", "340.00")
	})
	if err != nil {
		t.Fatal(err)
	}
	tbl := NewTable(buf)
	if string(tbl.Name()) != "users" {
		t.Errorf("name = %q", tbl.Name())
	}
	if tbl.RecordCount() != 2 {
		t.Fatalf("record count = %d", tbl.RecordCount())
	}
	if string(tbl.Record(0).Field(0)) != "Alice" {
		t.Errorf("rec0 f0 = %q", tbl.Record(0).Field(0))
	}
	if string(tbl.Record(1).Field(1)) != "340.00" {
		t.Errorf("rec1 f1 = %q", tbl.Record(1).Field(1))
	}
	if !Canonical(buf) {
		t.Error("not canonical")
	}
}

func TestDocument(t *testing.T) {
	buf, _ := Build(func(b *Builder) {
		b.File("mydb")
		b.Group("users", []string{"name"})
		b.Record("Alice")
		b.Group("products", []string{"id"})
		b.Record("01")
	})
	doc := NewDocument(buf)
	if string(doc.Name()) != "mydb" {
		t.Errorf("name = %q", doc.Name())
	}
	if doc.GroupCount() != 2 {
		t.Fatalf("group count = %d", doc.GroupCount())
	}
	if string(doc.GroupByName("products").Record(0).Field(0)) != "01" {
		t.Error("products lookup")
	}
	if doc.GroupByName("missing") != nil {
		t.Error("missing should be nil")
	}
}

func TestEscaping(t *testing.T) {
	buf, _ := Build(func(b *Builder) {
		b.Group("g", nil)
		b.Record("a\x1fb", "c")
	})
	rec := NewTable(buf).Record(0)
	if rec.FieldCount() != 2 {
		t.Fatalf("field count = %d", rec.FieldCount())
	}
	if !bytes.Equal(rec.Value(0), []byte("a\x1fb")) {
		t.Errorf("value0 = %q", rec.Value(0))
	}
}

func TestTrailingEmptyField(t *testing.T) {
	if n := NewTable([]byte("\x1eAlice\x1f")).Record(0).FieldCount(); n != 2 {
		t.Errorf("trailing US: %d fields, want 2", n)
	}
	if n := NewTable([]byte("\x1eAlice")).Record(0).FieldCount(); n != 1 {
		t.Errorf("no trailing: %d fields, want 1", n)
	}
}

func TestListFieldBytes(t *testing.T) {
	buf, err := Build(func(b *Builder) {
		b.Group("users", nil)
		b.Record("Alice").ListField("Admin", "Editor", "User").Field("1502.30")
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("\x1dusers\x1eAlice\x1f\x02Admin\x1fEditor\x1fUser\x03\x1f1502.30")
	if !bytes.Equal(buf, want) {
		t.Fatalf("bytes = %q, want %q", buf, want)
	}
	rec := NewTable(buf).Record(0)
	if rec.FieldCount() != 3 {
		t.Fatalf("field count = %d, want 3", rec.FieldCount())
	}
	got := rec.List(1)
	if len(got) != 3 || string(got[0]) != "Admin" || string(got[1]) != "Editor" || string(got[2]) != "User" {
		t.Errorf("list = %q", got)
	}
	if string(rec.Value(2)) != "1502.30" {
		t.Errorf("value2 = %q", rec.Value(2))
	}
}

func TestListFieldEscapedAndEmpty(t *testing.T) {
	buf, _ := Build(func(b *Builder) {
		b.Group("g", nil)
		b.Record("x").ListField("a\x1fb", "c\x02d", "")
		b.Record("y").ListField()
	})
	tbl := NewTable(buf)
	got := tbl.Record(0).List(1)
	if len(got) != 3 || !bytes.Equal(got[0], []byte("a\x1fb")) || !bytes.Equal(got[1], []byte("c\x02d")) || len(got[2]) != 0 {
		t.Errorf("escaped list = %q", got)
	}
	if tbl.Record(0).FieldCount() != 2 {
		t.Errorf("escaped field count = %d, want 2", tbl.Record(0).FieldCount())
	}
	empty := tbl.Record(1).List(1)
	if empty == nil || len(empty) != 0 {
		t.Errorf("empty list = %q, want []", empty)
	}
}

func TestListKeepsNestedScope(t *testing.T) {
	// An item that is itself a nested scope containing US stays one item.
	rec := NewTable([]byte("\x1ex\x1f\x02a\x1f\x02b\x1fc\x03\x1fd\x03")).Record(0)
	got := rec.List(1)
	if len(got) != 3 || string(got[0]) != "a" || string(got[1]) != "\x02b\x1fc\x03" || string(got[2]) != "d" {
		t.Errorf("list = %q", got)
	}
}

func TestListPlainField(t *testing.T) {
	rec := NewTable([]byte("\x1eAlice\x1fa\x10\x1fb")).Record(0)
	if got := rec.List(0); len(got) != 1 || string(got[0]) != "Alice" {
		t.Errorf("plain list = %q", got)
	}
	if got := rec.List(1); len(got) != 1 || string(got[0]) != "a\x1fb" {
		t.Errorf("plain escaped list = %q", got)
	}
}

func TestBuilderDocumentModeAndRefs(t *testing.T) {
	buf, err := Build(func(b *Builder) {
		b.Section("intro", 1).Block("hello\nworld").Item("one").Item("two")
		b.Section("deep", 2)
		b.Record("r").Field("v\x1f").Nested(func(n *Builder) {
			n.Record("inner")
		})
		b.Ref("users").RefPath("users", "01", "name")
		b.ETBPayload("abc123")
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("\x1dintro\x1ehello\x10\nworld\x1fone\x1ftwo" +
		"\x1d\x1ddeep" +
		"\x1er\x1fv\x10\x1f\x02\x1einner\x03" +
		"\x05users\x05\x02users\x1f01\x1fname\x03" +
		"\x17abc123")
	if !bytes.Equal(buf, want) {
		t.Errorf("bytes = %q\nwant    %q", buf, want)
	}
}

func TestETBPayloadRejectsControlBytes(t *testing.T) {
	b := &Builder{}
	b.ETBPayload("bad\x1fpayload")
	if b.Err() == nil {
		t.Error("expected an error for a control byte in an ETB payload")
	}
	if !bytes.Equal(b.Bytes(), []byte{ETB}) {
		t.Errorf("bytes = %q, want bare ETB", b.Bytes())
	}
}

func TestNamesRejectControlBytes(t *testing.T) {
	b := &Builder{}
	b.Group("bad\x1fname", nil)
	if b.Err() == nil {
		t.Error("expected an error for a control byte in a name")
	}
}

func TestStreamTornTail(t *testing.T) {
	r := NewStreamReader([]byte("\x1ecreate\x1fa1b2\x17\x1ename\x1fdra"))
	if !r.Torn() {
		t.Error("expected torn")
	}
	if r.BlockCount() != 1 {
		t.Errorf("block count = %d", r.BlockCount())
	}
	if r.Table().RecordCount() != 1 {
		t.Errorf("committed records = %d", r.Table().RecordCount())
	}
}

func TestPrettyRoundtrip(t *testing.T) {
	buf, _ := Build(func(b *Builder) {
		b.Group("g", []string{"a", "b"})
		b.Record("x", "y")
	})
	pretty := Format(buf)
	if !bytes.Contains([]byte(pretty), []byte("␞")) { // ␞ record glyph
		t.Error("missing RS glyph")
	}
	if !bytes.Equal(Parse(pretty), buf) {
		t.Error("pretty round-trip mismatch")
	}
}

func TestStreamFileLogRepair(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claims.c0")

	log, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Record("create", "a1b2"); err != nil {
		t.Fatal(err)
	}
	log.Close()

	// Simulate a crash mid-append, ending in a bare DLE.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.Write([]byte{RS, 'x', DLE})
	f.Close()

	log, err = OpenLog(path) // repairs the torn tail
	if err != nil {
		t.Fatal(err)
	}
	log.Record("tag", "alpha")
	log.Close()

	data, _ := ReadLog(path)
	r := NewStreamReader(data)
	if r.Torn() {
		t.Error("should not be torn after repair")
	}
	if r.Table().RecordCount() != 2 {
		t.Errorf("records = %d, want 2", r.Table().RecordCount())
	}
	if string(r.Table().Record(1).Field(0)) != "tag" {
		t.Errorf("rec1 f0 = %q", r.Table().Record(1).Field(0))
	}
}
