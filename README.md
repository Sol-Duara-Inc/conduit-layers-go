# conduit-layers-go

A derived CDEvent is a compressed object: one `subject.content` carrying the
vocabularies of every type in its lineage at once, its ancestry stated in
`context.inherits`. This library keeps a catalog of lodged types and validates
by names and obligations only — the set of fields a schema declares and which
of them are required; every other schema keyword is advisory and never
enforced. Decompression turns one arriving event into one minted object per
lineage layer, sanctioned type first, the event's own type last: content
restricted to the fields that layer declares, values untouched, `customData`
carried whole. An unknown type is a coinage and rides untouched. A bad
utterance earns a typed refusal, charged to the emitter; a broken catalog
record is a fault, never charged to the emitter.

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
