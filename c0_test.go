package c0

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mustBuild(t *testing.T, fn func(*Builder)) []byte {
	t.Helper()
	buf, err := Build(fn)
	if err != nil {
		t.Fatal(err)
	}
	return buf
}

func mustParse(t *testing.T, buf []byte) *Document {
	t.Helper()
	doc, err := ParseDocument(buf)
	if err != nil {
		t.Fatalf("parse %q: %v", buf, err)
	}
	return doc
}

func TestBuildReadRoundtrip(t *testing.T) {
	buf := mustBuild(t, func(b *Builder) {
		b.Group("users", []string{"name", "amount"})
		b.Record("Alice", "1502.30")
		b.Record("Bob", "340.00")
	})
	want := []byte("\x1dusers\x01\x1ename\x1famount\x1eAlice\x1f1502.30\x1eBob\x1f340.00")
	if !bytes.Equal(buf, want) {
		t.Fatalf("bytes = %q, want %q", buf, want)
	}
	doc := mustParse(t, buf)
	g := doc.GroupByName("users")
	if g == nil || g.RecordCount() != 2 {
		t.Fatalf("users group missing or wrong size")
	}
	if string(g.Header(1)) != "amount" || g.Column("amount") != 1 {
		t.Errorf("headers = %q", g.Headers())
	}
	if string(g.Record(0).Value(0)) != "Alice" || string(g.Record(1).Value(1)) != "340.00" {
		t.Errorf("values wrong")
	}
	if !IsCanonical(buf) {
		t.Error("not canonical")
	}
	if len(doc.Warnings()) != 2 { // no SYN, no EOT
		t.Errorf("warnings = %q", doc.Warnings())
	}
}

func TestDocumentWithHeading(t *testing.T) {
	buf := mustBuild(t, func(b *Builder) {
		b.SYN().Heading("shape", "tabular").Heading("version", "1")
		b.File("mydb")
		b.Group("users", []string{"name"})
		b.Record("Alice")
		b.Group("products", []string{"id"})
		b.Record("01")
		b.EOT()
	})
	doc := mustParse(t, buf)
	if !doc.HasSYN() || !doc.HasEOT() || len(doc.Warnings()) != 0 {
		t.Errorf("syn=%v eot=%v warnings=%q", doc.HasSYN(), doc.HasEOT(), doc.Warnings())
	}
	if doc.Shape() != "tabular" || doc.Heading().Version() != 1 {
		t.Errorf("heading = %v", doc.Heading().Pairs())
	}
	if string(doc.Name()) != "mydb" || doc.GroupCount() != 2 {
		t.Errorf("name = %q groups = %d", doc.Name(), doc.GroupCount())
	}
	if string(doc.GroupByName("products").Record(0).Value(0)) != "01" {
		t.Error("products lookup")
	}
	if doc.GroupByName("missing") != nil {
		t.Error("missing should be nil")
	}
}

func TestTextSpelling(t *testing.T) {
	cases := map[string]string{
		"plain":            "plain",
		"Alice Smith":      "Alice Smith",
		"":                 "",
		" edge":            "\x02 edge\x03",
		"a\nb":             "\x02a\nb\x03",
		"a\x1fb":           "\x02a\x1fb\x03",
		"\x02\x03\x10":     "\x02\x10\x02\x10\x03\x10\x10\x03",
		"\x17\x04\x16\x19": "\x02\x10\x17\x10\x04\x10\x16\x10\x19\x03",
	}
	for in, want := range cases {
		got := AppendValue(nil, []byte(in))
		if string(got) != want {
			t.Errorf("AppendValue(%q) = %q, want %q", in, got, want)
		}
		doc := mustParse(t, append([]byte{RS}, got...))
		if v := doc.Records()[0].Value(0); string(v) != in {
			t.Errorf("round trip of %q = %q", in, v)
		}
	}
}

func TestFieldArity(t *testing.T) {
	for in, want := range map[string]int{"\x1eAlice\x1f": 2, "\x1eAlice": 1, "\x1e": 1, "\x1e\x1f\x1f": 3} {
		doc := mustParse(t, []byte(in))
		if n := doc.Records()[0].FieldCount(); n != want {
			t.Errorf("%q: %d fields, want %d", in, n, want)
		}
	}
}

func TestLayoutIsNotData(t *testing.T) {
	doc := mustParse(t, []byte("\x1dusers\n  \x1e Alice Smith \x1f 100 \n"))
	rec := doc.Group(0).Record(0)
	if string(rec.Value(0)) != "Alice Smith" || string(rec.Value(1)) != "100" {
		t.Errorf("values = %q", rec.Values())
	}
	if IsCanonical([]byte("\x1ea \x1fb")) {
		t.Error("layout should not be canonical")
	}
}

func TestKeyedRecord(t *testing.T) {
	buf := mustBuild(t, func(b *Builder) {
		b.Group("database", nil)
		b.Record().Key("host", "localhost").Key("port", "5432").Key("host", "db.local")
	})
	rec := mustParse(t, buf).Group(0).Record(0)
	if !rec.IsKeyed() || rec.FieldCount() != 0 {
		t.Fatal("expected a keyed record")
	}
	if keys := rec.Keys(); len(keys) != 3 || string(keys[1]) != "port" {
		t.Errorf("keys = %q", keys)
	}
	if v, ok := rec.Get("host"); !ok || string(v) != "db.local" { // last wins
		t.Errorf("host = %q %v", v, ok)
	}
	if _, ok := rec.Get("missing"); ok {
		t.Error("missing key found")
	}
	if IsCanonical(buf) {
		t.Error("duplicate key should not be canonical")
	}
	if err := Validate([]byte("\x1ea\x01k\x1fv")); err == nil {
		t.Error("mixed record accepted")
	}
}

func TestNestedLevels(t *testing.T) {
	buf := mustBuild(t, func(b *Builder) {
		b.Record("Alice").List("Admin", "Editor").Level(func(b *Builder) {
			b.Header("street", "city")
			b.Record("1 Main", "Springfield")
		}).Field("100")
	})
	want := []byte("\x1eAlice\x1f\x10\x1eAdmin\x1fEditor\x18\x1f\x10\x01\x1estreet\x1fcity\x1e1 Main\x1fSpringfield\x18\x1f100")
	if !bytes.Equal(buf, want) {
		t.Fatalf("bytes = %q\nwant    %q", buf, want)
	}
	rec := mustParse(t, buf).Records()[0]
	if rec.FieldCount() != 4 || !rec.IsLevel(1) || rec.IsLevel(0) {
		t.Fatalf("shape wrong: %d fields", rec.FieldCount())
	}
	roles, err := rec.List(1)
	if err != nil || len(roles) != 2 || string(roles[1]) != "Editor" {
		t.Errorf("list = %q %v", roles, err)
	}
	addr, err := rec.Level(2)
	if err != nil {
		t.Fatal(err)
	}
	g := addr.Group(0)
	if string(g.Header(1)) != "city" || string(g.Record(0).Value(0)) != "1 Main" {
		t.Errorf("nested table = %q / %q", g.Headers(), g.Record(0).Values())
	}
	if _, err := rec.List(2); !errors.Is(err, ErrNotList) {
		t.Errorf("List on a table: %v", err)
	}
	if _, err := rec.Level(0); !errors.Is(err, ErrNotLevel) {
		t.Errorf("Level on a scalar: %v", err)
	}
	empty := mustParse(t, []byte("\x1e\x10\x18")).Records()[0]
	if items, err := empty.List(0); err != nil || len(items) != 0 {
		t.Errorf("empty level = %q %v", items, err)
	}
}

func TestReferences(t *testing.T) {
	buf := mustBuild(t, func(b *Builder) {
		b.Record("My Post").Ref("tags").RefPath("tags", "001").RefPath("tags", "001", "label")
	})
	want := []byte("\x1eMy Post\x1f\x05tags\x1f\x05\x02tags\x1f001\x03\x1f\x05\x02tags\x1f001\x1flabel\x03")
	if !bytes.Equal(buf, want) {
		t.Fatalf("bytes = %q\nwant    %q", buf, want)
	}
	rec := mustParse(t, buf).Records()[0]
	r1, err := rec.Ref(1)
	if err != nil || !r1.IsGroup() || string(r1.Group()) != "tags" || r1.Record() != nil {
		t.Errorf("ref 1 = %v %v", r1, err)
	}
	r3, err := rec.Ref(3)
	if err != nil || string(r3.Record()) != "001" || string(r3.Field()) != "label" {
		t.Errorf("ref 3 = %v %v", r3, err)
	}
	if _, err := rec.Ref(0); !errors.Is(err, ErrNotReference) {
		t.Errorf("Ref on a scalar: %v", err)
	}
	if _, err := Build(func(b *Builder) { b.RefPath("tags", "") }); !errors.Is(err, ErrNotName) {
		t.Errorf("empty segment: %v", err)
	}
}

func TestNamesMustBeNames(t *testing.T) {
	for _, fn := range []func(*Builder){
		func(b *Builder) { b.Group("bad\x1fname", nil) },
		func(b *Builder) { b.Header("a", " b") },
		func(b *Builder) { b.Record().Key("k\n", "v") },
		func(b *Builder) { b.Ref("bad\x1e") },
		func(b *Builder) { b.ETBPayload("bad\x1fpayload") },
		func(b *Builder) { b.SYN().Heading("sha pe\t", "x") },
	} {
		if _, err := Build(fn); !errors.Is(err, ErrNotName) {
			t.Errorf("expected ErrNotName, got %v", err)
		}
	}
	if _, err := Build(func(b *Builder) { b.Group("", nil).Record().Key("", "") }); err != nil {
		t.Errorf("empty names are legal: %v", err)
	}
}

func TestDocumentModeAndComments(t *testing.T) {
	buf := mustBuild(t, func(b *Builder) {
		b.Section("intro", 1).Block("hello\nworld").Item("one").Item("two")
		b.Section("deep", 2)
		b.Note("draft")
		b.Record("r")
	})
	want := []byte("\x1dintro\x1e\x02hello\nworld\x03\x1fone\x1ftwo\x1d\x1ddeep\x07draft\x1er")
	if !bytes.Equal(buf, want) {
		t.Fatalf("bytes = %q\nwant    %q", buf, want)
	}
	doc := mustParse(t, buf)
	gs := doc.Groups()
	if len(gs) != 2 || gs[1].Depth() != 2 || gs[1].RecordCount() != 1 {
		t.Errorf("groups = %d", len(gs))
	}
	// a dead record is parsed and discarded; a note inside a unit leaves it empty
	doc = mustParse(t, []byte("\x1dg\x1ea\x07\x1eb\x1ec\x1f\x07old\x1fd"))
	g := doc.Group(0)
	if g.RecordCount() != 2 || string(g.Record(1).Value(1)) != "" || string(g.Record(1).Value(2)) != "d" {
		t.Errorf("records = %d, rec1 = %q", g.RecordCount(), g.Record(1).Values())
	}
	if err := Validate([]byte("\x1ea\x07")); err == nil {
		t.Error("BEL at end accepted")
	}
}

func TestContainerBlob(t *testing.T) {
	buf := mustBuild(t, func(b *Builder) {
		b.SYN().HeadingLevel("blob", func(b *Builder) {
			b.Header("name", "offset", "length")
			b.Record("lamp", "0", "3")
			b.Record("desk", "3", "2")
		})
		b.EOT()
		b.SYN().File("catalogue").Group("items", []string{"name", "photo"}).Record("Lamp", "lamp").EOT()
		b.EM([]byte("abcde"))
	})
	cont, err := ParseContainer(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(cont.Documents()) != 2 || !cont.HasEM() || string(cont.Tail()) != "abcde" {
		t.Fatalf("documents = %d em = %v tail = %q", len(cont.Documents()), cont.HasEM(), cont.Tail())
	}
	if blob := cont.Blob(); blob == nil || blob.Group(0).Column("length") != 2 {
		t.Fatal("blob table not read")
	}
	if v, ok := cont.BlobBytes("desk"); !ok || string(v) != "de" {
		t.Errorf("desk = %q %v", v, ok)
	}
	if _, ok := cont.BlobBytes("none"); ok {
		t.Error("unknown blob found")
	}
	if string(cont.Documents()[1].Name()) != "catalogue" {
		t.Errorf("second document name = %q", cont.Documents()[1].Name())
	}
	if _, err := ParseContainer([]byte("\x16\x1ea\x04\x1eb")); err == nil {
		t.Error("second document without SYN accepted")
	}
}

func TestValidatorWarnings(t *testing.T) {
	v := NewValidator([]byte("\x1dg\n\x1e\x02only\x03\x07n"), true)
	if err := v.Run(); err != nil {
		t.Fatal(err)
	}
	if v.OK() || len(v.Warnings()) != 3 {
		t.Errorf("warnings = %q", v.Warnings())
	}
	var se *SyntaxError
	if err := Validate([]byte("\x1ea\x1ab")); !errors.As(err, &se) {
		t.Errorf("SUB outside a diff: %v", err)
	}
	var ue *UnassignedCodeError
	if err := Validate([]byte("\x1ea\x0eb")); !errors.As(err, &ue) || ue.Byte != 0x0e {
		t.Errorf("SO: %v", err)
	}
	// a diff document validates under the diff grammar
	if err := Validate([]byte("\x16\x01shape\x1fdiff\x1cfoo.txt\x1d\x02Hello \x03\x1fworld\x1auniverse\x1ecount\x1atotal\x04")); err != nil {
		t.Errorf("diff body: %v", err)
	}
}

func TestTokenizer(t *testing.T) {
	toks, err := Tokenize([]byte("\x1dg \x1e\x02a\x10\x03\x03\x1fb\x19raw"))
	if err != nil {
		t.Fatal(err)
	}
	want := []TokenType{TokenGS, TokenData, TokenLayout, TokenRS, TokenText, TokenUS, TokenData, TokenEM}
	if len(toks) != len(want) {
		t.Fatalf("tokens = %v", toks)
	}
	for i, w := range want {
		if toks[i].Type != w {
			t.Errorf("token %d = %v, want %v", i, toks[i].Type, w)
		}
	}
	if string(toks[4].Text([]byte("\x1dg \x1e\x02a\x10\x03\x03\x1fb\x19raw"))) != "a\x03" {
		t.Error("text token decode")
	}
	if _, err := Tokenize([]byte("\x1ea\x06")); err == nil {
		t.Error("unassigned code accepted")
	}
}

func TestStreamReader(t *testing.T) {
	r := NewStreamReader([]byte("\x1ecreate\x1fa1b2\x17\x1ename\x1fdra"))
	if !r.Torn() || r.BlockCount() != 1 || r.CommittedEnd() != 13 {
		t.Errorf("torn=%v blocks=%d end=%d", r.Torn(), r.BlockCount(), r.CommittedEnd())
	}
	recs, err := r.Records()
	if err != nil || len(recs) != 1 {
		t.Errorf("records = %d %v", len(recs), err)
	}
	// an ETB inside open text is a commit; the block is damaged
	r = NewStreamReader([]byte("\x1e\x02a\x17\x1eb\x17"))
	if r.Torn() || r.BlockCount() != 2 || !r.IsDamaged(0) || r.IsDamaged(1) {
		t.Errorf("damaged = %v", r.Damaged())
	}
	recs, _ = r.Records()
	if len(recs) != 1 || string(recs[0].Value(0)) != "b" {
		t.Errorf("records = %d", len(recs))
	}
	if LastCommitEnd([]byte("\x1ea\x17x\x1e\x02\x10\x17\x03")) != 4 {
		t.Error("LastCommitEnd")
	}
}

func TestStreamFileLogRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claims.c0")
	log, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Start(); err != nil {
		t.Fatal(err)
	}
	if err := log.Record("create", "a1b2"); err != nil {
		t.Fatal(err)
	}
	log.Close()

	// Simulate a crash mid-append, ending in an open text.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.Write([]byte{RS, 'x', US, STX, 'y'})
	f.Close()

	log, err = OpenLog(path) // repairs the torn tail
	if err != nil {
		t.Fatal(err)
	}
	log.Record("tag", "alpha")
	log.Close()

	r, err := ReadStream(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Torn() || r.BlockCount() != 3 {
		t.Errorf("torn=%v blocks=%d", r.Torn(), r.BlockCount())
	}
	doc, err := r.Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Shape() != "stream" || len(doc.Warnings()) != 0 {
		t.Errorf("shape = %q warnings = %q", doc.Shape(), doc.Warnings())
	}
	recs := doc.Records()
	if len(recs) != 2 || string(recs[1].Value(0)) != "tag" {
		t.Errorf("records = %d", len(recs))
	}
}

func TestPretty(t *testing.T) {
	// Expected strings are the Crystal reference formatter's output.
	buf := mustBuild(t, func(b *Builder) {
		b.SYN().Heading("shape", "tabular")
		b.File("mydb")
		b.Group("users", []string{"name", "amount", "roles"})
		b.Record("Alice Smith", "1502.30").List("admin", "editor")
		b.Record("Bob", "\x02 x\x03")
		b.EOT()
	})
	pretty, err := Format(buf)
	if err != nil {
		t.Fatal(err)
	}
	want := "␖␁shape␟tabular\n␜mydb\n␝users\n  ␁␞name␟amount␟roles\n  ␞Alice Smith␟1502.30␟␐\n    ␞admin␟editor\n  ␘\n  ␞Bob␟␂␐␂ x␐␃␃\n␄\n"
	if pretty != want {
		t.Errorf("pretty =\n%s\nwant\n%s", pretty, want)
	}
	if got := Parse(pretty); !bytes.Equal(got, buf) {
		t.Errorf("round trip = %q\nwant        %q", got, buf)
	}
	if _, err := Format([]byte("\x1e␞")); err == nil {
		t.Error("glyph in data accepted")
	}

	buf2 := mustBuild(t, func(b *Builder) {
		b.Group("users", []string{"name", "amount"})
		b.Record("Alice Smith", "1502.30")
		b.Record("Bob", "3")
		b.Group("cfg", nil)
		b.Record().Key("host", "localhost").Key("port", "5432")
	})
	spaced, err := FormatWith(buf2, "  ", FormatSpaced)
	if err != nil {
		t.Fatal(err)
	}
	wantSpaced := "␝ users\n  ␁␞ name        ␟ amount\n  ␞ Alice Smith ␟ 1502.30\n  ␞ Bob         ␟ 3\n␝ cfg\n  ␞ ␁host ␟ localhost ␁port ␟ 5432\n"
	if spaced != wantSpaced {
		t.Errorf("spaced =\n%s\nwant\n%s", spaced, wantSpaced)
	}
	aligned, err := FormatWith(buf2, "  ", FormatAligned)
	if err != nil {
		t.Fatal(err)
	}
	wantAligned := "␝users\n  ␁␞name       ␟amount\n  ␞Alice Smith␟1502.30\n  ␞Bob        ␟3\n␝cfg\n  ␞␁host␟localhost ␁port␟5432\n"
	if aligned != wantAligned {
		t.Errorf("aligned =\n%s\nwant\n%s", aligned, wantAligned)
	}
	for _, p := range []string{spaced, aligned} {
		if !bytes.Equal(Parse(p), buf2) {
			t.Errorf("round trip of\n%s", p)
		}
	}
}

func TestPrettyParseLayout(t *testing.T) {
	got := Parse("␝users\n  ␞ Alice Smith ␟ 100\n  ␞␂  padded\n␃\n")
	want := []byte("\x1dusers\x1eAlice Smith\x1f100\x1e\x02  padded\n\x03")
	if !bytes.Equal(got, want) {
		t.Errorf("parse = %q, want %q", got, want)
	}
}

func TestBuilderPrimitives(t *testing.T) {
	cases := []struct {
		fn   func(*Builder)
		want string
	}{
		{func(b *Builder) { b.FS("inv").GS("users").SOH().RS("name").US("amount") }, "␜inv␝users␁␞name␟amount"},
		{func(b *Builder) { b.RS("Alice").US("1502.30") }, "␞Alice␟1502.30"},
		{func(b *Builder) { b.RS().SOH("host").US("localhost").SOH("port").US("5432") }, "␞␁host␟localhost␁port␟5432"},
		{func(b *Builder) { b.RS("cfg").US().DLE().RS().SOH("b").US("2").CAN() }, "␞cfg␟␐␞␁b␟2␘"},
		{func(b *Builder) { b.BEL("checked").RS("x") }, "␇checked␞x"},
		{func(b *Builder) { b.BEL().GS("old").RS("1").GS("new").RS("2") }, "␇␝old␞1␝new␞2"},
		{func(b *Builder) { b.GS().GS("sub").RS().ENQ("users").US().ENQ("users", "1") }, "␝␝sub␞␅users␟␅␂users␟1␃"},
		{func(b *Builder) { b.SYN().SOH("version").US("1").RS("a").EOT() }, "␖␁version␟1␞a␄"},
		{func(b *Builder) { b.RS("a b").US(" c").ETB("sha").ETB() }, "␞a b␟␂ c␃␗sha␗"},
	}
	for _, c := range cases {
		got := mustBuild(t, c.fn)
		if want := Parse(c.want); !bytes.Equal(got, want) {
			t.Errorf("primitives = %q\nwant         %q", got, want)
		}
	}
	if got := mustBuild(t, func(b *Builder) { b.RS("a").EM([]byte("raw")) }); string(got) != "\x1ea\x19raw" {
		t.Errorf("EM = %q", got)
	}

	// Mixing with the composed methods: both layers share the record state.
	mixed := mustBuild(t, func(b *Builder) {
		b.RS("a")
		b.Field("b")
		b.Record()
		b.US("c")
		b.GS("g")
		b.Key("k", "v")
		b.Group("h", []string{"h"})
		b.Field("x")
	})
	if want := Parse("␞a␟b␞␟c␝g␞␁k␟v␝h␁␞h␞x"); !bytes.Equal(mixed, want) {
		t.Errorf("mixed = %q\nwant    %q", mixed, want)
	}

	// Errors are recorded, not written.
	if _, err := Build(func(b *Builder) { b.CAN() }); !errors.Is(err, ErrNoLevel) {
		t.Errorf("CAN with no level: %v", err)
	}
	for _, fn := range []func(*Builder){
		func(b *Builder) { b.Record("a").Note("") },
		func(b *Builder) { b.Record("a").BEL("") },
	} {
		if _, err := Build(fn); !errors.Is(err, ErrEmptyNote) {
			t.Errorf("empty note: %v", err)
		}
	}
	for _, fn := range []func(*Builder){
		func(b *Builder) { b.FS("a\x1cb") },
		func(b *Builder) { b.GS(" g") },
		func(b *Builder) { b.SOH("k\n") },
		func(b *Builder) { b.ENQ("bad\x1e") },
		func(b *Builder) { b.ENQ() },
		func(b *Builder) { b.ETB("x y\t") },
	} {
		if _, err := Build(fn); !errors.Is(err, ErrNotName) {
			t.Errorf("bad name accepted: %v", err)
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("RS with two arguments did not panic")
			}
		}()
		Build(func(b *Builder) { b.RS("a", "b") })
	}()

	// The same document through either layer is byte-identical.
	hi := mustBuild(t, func(b *Builder) {
		b.SYN().Heading("shape", "tabular")
		b.Group("users", []string{"name", "amount"})
		b.Record("Alice", "1502.30")
		b.Record("Bob", "3 40")
		b.Group("cfg", nil)
		b.Record().Key("host", "localhost").Key("port", "5432")
		b.EOT()
	})
	lo := mustBuild(t, func(b *Builder) {
		b.SYN().SOH("shape").US("tabular")
		b.GS("users").SOH().RS("name").US("amount").RS("Alice").US("1502.30").RS("Bob").US("3 40")
		b.GS("cfg").RS().SOH("host").US("localhost").SOH("port").US("5432").EOT()
	})
	if !bytes.Equal(lo, hi) {
		t.Errorf("layers differ:\n%q\n%q", lo, hi)
	}
	if !IsCanonical(mustParse(t, lo).Body().Raw()) {
		t.Error("primitive-built document is not canonical")
	}
}
