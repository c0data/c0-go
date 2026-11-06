# c0

A Go implementation of [C0DATA](https://github.com/c0data) — structured data
built on ASCII C0 control codes, at the 1.0 redesign of the spec.

Pure Go, **no dependencies**. The read path is **zero-copy**: accessors return
sub-slices of the input buffer (a Go slice is a view into the backing array).
Outside text the hot loop is a single comparison, `byte < 0x20`.

## Install

```sh
go get github.com/c0data/c0-go
```

```go
import c0 "github.com/c0data/c0-go"
```

## Usage

```go
// Write: SYN heading, a group with a header row (␁␞), records, EOT
buf, _ := c0.Build(func(b *c0.Builder) {
    b.SYN().Heading("shape", "tabular")
    b.Group("users", []string{"name", "amount"})
    b.Record("Alice Smith", "100")
    b.Record("Bob", "200")
    b.EOT()
})
// ␖␁shape␟tabular␝users␁␞name␟amount␞Alice Smith␟100␞Bob␟200␄

// Read (validates, then indexes; fields are sub-slices of buf)
doc, err := c0.ParseDocument(buf)
users := doc.GroupByName("users")
for _, rec := range users.Records() {
    name := rec.Value(0)    // []byte view into buf, layout trimmed, text decoded
    _ = rec.Value(users.Column("amount"))
    _ = name
}
doc.Warnings()              // loud warnings: no SYN, no EOT, unknown heading key

// Canonical form is hashable for content addressing (the body; framing is outside the hash)
c0.IsCanonical(doc.Body().Raw()) // true

// Containers, streams, pretty
c0.ParseContainer(data)  // several documents, EM tail, .BlobBytes(name)
c0.NewStreamReader(data) // .Torn(), .Blocks(), .Damaged(), .Records()
c0.Format(buf)           // Unicode Control Pictures
```

### Values: plain or text

A value is written plain when it can be and as text (`␂…␃`) when it must be:
any byte below 0x20 or edge whitespace. Inside text only STX, ETX, DLE, ETB,
EOT, SYN and EM take a `␐` escape. The builder picks the spelling; `Value`
gives the logical bytes back either way.

### Keyed records, nesting, lists, references

```go
buf, _ := c0.Build(func(b *c0.Builder) {
    b.Group("server", nil)
    b.Record().Key("host", "0.0.0.0").Key("port", "8080")   // ␞␁host␟0.0.0.0␁port␟8080
    b.Group("users", []string{"name", "roles", "address", "team"})
    b.Record("Alice").List("Admin", "Editor")                // ␐␞Admin␟Editor␘
    b.Level(func(b *c0.Builder) {                            // ␐␁␞street␟city␞1 Main␟Springfield␘
        b.Header("street", "city")
        b.Record("1 Main", "Springfield")
    })
    b.RefPath("teams", "t1")                                 // ␅␂teams␟t1␃
})
doc, _ := c0.ParseDocument(buf)
cfg := doc.GroupByName("server").Record(0)
host, _ := cfg.Get("host")                 // last wins; order is kept in cfg.Pairs()
alice := doc.GroupByName("users").Record(0)
roles, _ := alice.List(1)                  // [][]byte{"Admin", "Editor"}
addr, _ := alice.Level(2)                  // *c0.Body: a nested document body
ref, _ := alice.Ref(3)                     // ref.Group(), ref.Record(), ref.Field()
_, _, _, _ = host, roles, addr, ref
```

The builder also has `File`, `GroupAt`/`Section`, `Block`/`Item`, `KeyLevel`,
`HeadingLevel` (the `blob` table), `Note`, `ETB` and `EM`.

### Primitives

Underneath, the builder has one method per code: `FS`, `GS`, `RS`, `US`,
`SUB` (C0-DIFF only), `SOH`, `DLE`, `CAN`, `BEL`, `ENQ`, `SYN`, `EOT`, `ETB`,
`EM`. Each writes
its code and, when given, what follows it (optional arguments are variadic:
none or one), so a chain reads like the bytes, and anything the grammar
allows can be written:

```go
b.RS("Alice").US("1502.30")                                 // ␞Alice␟1502.30
b.RS().SOH("host").US("localhost").SOH("port").US("5432")   // ␞␁host␟localhost␁port␟5432
b.GS("users").SOH().RS("name").US("amount")                 // header row
b.RS("cfg").US().DLE().RS().SOH("b").US("2").CAN()          // ␞cfg␟␐␞␁b␟2␘
b.BEL().GS("old")                                           // a dead group
b.GS().GS("sub").RS().ENQ("users").US().ENQ("users", "1")   // ␝␝sub␞␅users␟␅␂users␟1␃
b.RS("old").SUB("new")                                      // ␞old␚new  (C0-DIFF replace-all)
```

The composed methods above are built on these and cannot write ill-formed
output; with the primitives, well-formedness is yours. Values still take
their one spelling and names are still checked, so what is written stays
canonical. The two layers share state and mix freely: `b.GS("g").Key("k", "v")`
writes `␝g␞␁k␟v`.

### Streams

`OpenLog` appends ETB-committed blocks to a file, repairing a torn tail first;
`StreamWriter` does the same over any `io.Writer`. `NewStreamReader` scans a
log: committed blocks, blocks cut by an ETB inside open text or a level
(`Damaged`), and whether data trails the last commit (`Torn`).

## Status

Core (scan primitives, validator, zero-copy body/document/container readers),
builder, canonical check, ETB stream mode, tokenizer, and pretty form (format
in compact/aligned/spaced modes, parse). Passes every case of the shared
conformance vectors from [c0-spec](https://github.com/c0data/c0-spec).

Converters (CSV / JSON / C0-DIFF) are not ported.

## Docs

Spec: [c0-spec](https://github.com/c0data/c0-spec) (`DESIGN.md`, `GRAMMAR.md`).
API docs: <https://pkg.go.dev/github.com/c0data/c0-go>

## Test

```sh
git submodule update --init   # pulls in c0-spec (the shared vectors)
go test ./...
```

## License

MIT
