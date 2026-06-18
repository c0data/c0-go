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
