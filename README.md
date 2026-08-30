# conduit-layers-go

A derived CDEvent is a compressed object: one `subject.content` carrying the
vocabularies of every type in its lineage at once, its ancestry stated in
`context.inherits`. This library keeps a catalog of lodged types and validates
by names and obligations only — the set of fields a schema declares and which
of them are required; every other schema keyword is advisory and never
enforced. Decompression turns one arriving event into one minted object per
lineage layer, root first (sanctioned or hidden-base), the event's own type
last: content restricted to the fields that layer declares, values untouched,
`customData` carried whole. An unknown type is a coinage and rides untouched. A bad
utterance earns a typed refusal, charged to the emitter; a broken catalog
record is a fault, never charged to the emitter.

## Lodging

A type name holds exactly one schema. The first lodging of a name takes it; a
second schema declaring that same name is refused as `type-already-lodged`, and
the refusal names the schema already held. An event carries its type name, not
its schema URI, and its own layer is resolved by name, so a name with two
schemas would make admission depend on which was lodged last — the same event
resolved under one and refused under the other. The version is part of the
name: `com.acme.build.finished.1.0.0` and `com.acme.build.finished.1.1.0` are
different names and both lodge, so ordinary evolution is untouched; only a
second schema for the same version collides. Replacing a lodged schema is an
administrator's act — remove it, flush the cache, lodge the replacement — never
something a second lodging performs. Re-lodging the same schema URI is governed
by immutability as before: identical content is idempotent, different content
is refused.

## Install

```
go get github.com/sol-duara-inc/conduit-layers-go
```

## API

```go
package layers

type Layer struct {
    SchemaURI string
    TypeName  string
    Namespace string
    Subject   string
    Predicate string
    Version   string
    Fields    map[string]json.RawMessage
    Required  []string
    Lineage   []string
}

type Catalog interface {
    BySchemaURI(uri string) (*Layer, bool)
    ByType(name string) (*Layer, bool)
}

type LodgeRefusal struct {
    Kind     string
    Evidence string
}

type MemoryCatalog struct{ /* unexported */ }

func NewMemoryCatalog() *MemoryCatalog
func (c *MemoryCatalog) Lodge(envelope []byte) (*Layer, *LodgeRefusal)
func (c *MemoryCatalog) BySchemaURI(uri string) (*Layer, bool)
func (c *MemoryCatalog) ByType(name string) (*Layer, bool)

type Arrival struct {
    Type                  string
    InheritsPresent       bool
    InheritsWellFormed    bool
    Inherits              []string
    Content               map[string]any
    CustomData            any
    CustomDataContentType string
}

func ParseArrival(raw []byte) (*Arrival, error)

type Refusal struct {
    Kind     string
    Field    string
    Layer    string
    Evidence string
}

type Minted struct {
    SchemaURI             string
    TypeName              string
    Content               map[string]any
    CustomData            any
    CustomDataContentType string
}

type Resolution struct {
    Minted []Minted
}

func (r *Resolution) ServeAt(schemaURI string) (*Minted, bool)

type Outcome struct {
    Kind       string // "resolved" | "coinage" | "refused" | "fault"
    Resolution *Resolution
    Refusal    *Refusal
    Fault      string
}

func Decompress(a *Arrival, c Catalog) *Outcome
```

## Acceptance

`layers/testdata/corpus.json` is the acceptance contract: the library is
correct exactly when the corpus runner passes every case.

## License

AGPL-3.0. See [LICENSE](LICENSE).
