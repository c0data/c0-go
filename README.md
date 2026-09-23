# c0

A Go implementation of [C0DATA](https://github.com/c0data) — structured data
built on ASCII C0 control codes.

Pure Go, **no dependencies**. The read path is **zero-copy**: accessors return
sub-slices of the input buffer (a Go slice is a view into the backing array).
The hot loop is a single comparison, `byte < 0x20`.

## Install

```sh
go get github.com/c0data/c0-go
```

```go
import c0 "github.com/c0data/c0-go"
```

## Usage

```go
// Write
buf, _ := c0.Build(func(b *c0.Builder) {
    b.Group("users", []string{"name", "amount"})
    b.Record("Alice", "100")
    b.Record("Bob", "200")
})

// Read (zero-copy: fields are sub-slices of buf)
t := c0.NewTable(buf)
for _, rec := range t.Records() {
    name := rec.Field(0)   // []byte view into buf
    _ = rec.Value(1)       // []byte, DLE-escapes decoded
    _ = name
}

// Compact form is canonical — hashable for content addressing
c0.Canonical(buf) // => true

// Documents, streams, pretty
c0.NewDocument(buf)
c0.NewStreamReader(data) // .Torn(), .Committed(), .Block(i)
c0.Format(buf)           // Unicode Control Pictures
```

### List fields

A field whose value is a flat list is written as US-separated items inside
STX/ETX (`␂Admin␟Editor␃`). `ListField` writes one; `Record.List` reads it
back as unescaped items.

```go
buf, _ := c0.Build(func(b *c0.Builder) {
    b.Group("users", nil)
    b.Record("Alice")
    b.ListField("Admin", "Editor")   // one field: ␂Admin␟Editor␃
})
rec := c0.NewTable(buf).Record(0)
roles := rec.List(1)                 // [][]byte{"Admin", "Editor"}
_ = roles
```

The builder also has `Field`, `Nested`, `Ref`, `RefPath`, `Section`, `Block`,
`Item`, and `ETBPayload`, matching the Crystal reference.

## Status

Core: tokenizer, table/record and document/group readers (zero-copy), builder,
canonical helpers, ETB stream mode, and pretty (compact format + parse). Passes
the shared conformance vectors from
[c0-spec](https://github.com/c0data/c0-spec).

Converters (CSV / JSON / C0DIFF) are not yet ported — Go's stdlib `encoding/csv`
and `encoding/json` make those straightforward follow-ups.

## Docs

API docs: <https://pkg.go.dev/github.com/c0data/c0-go>

## Test

```sh
git submodule update --init   # pulls in c0-spec (the shared vectors)
go test ./...
```

## License

MIT
