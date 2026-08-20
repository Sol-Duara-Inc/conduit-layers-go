package layers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Arrival is one parsed event surface.
type Arrival struct {
	Type                  string
	InheritsPresent       bool
	InheritsWellFormed    bool     // false when present but not an array of strings
	Inherits              []string // nil unless present and well-formed
	Content               map[string]any
	CustomData            any
	CustomDataContentType string
}

// ParseArrival parses the raw event JSON. Only structural failures error:
// unparseable JSON, an absent/empty/non-string context.type, a non-object
// subject.content, or a non-string customDataContentType. A malformed
// context.inherits is NOT a parse error; it is recorded on the Arrival.
func ParseArrival(raw []byte) (*Arrival, error) {
	var ev struct {
		Context struct {
			Type     string          `json:"type"`
			Inherits json.RawMessage `json:"inherits"`
		} `json:"context"`
		Subject struct {
			Content map[string]any `json:"content"`
		} `json:"subject"`
		CustomData            any    `json:"customData"`
		CustomDataContentType string `json:"customDataContentType"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, fmt.Errorf("event does not parse: %w", err)
	}
	if ev.Context.Type == "" {
		return nil, errors.New("context.type is absent or empty")
	}
	a := &Arrival{
		Type:                  ev.Context.Type,
		Content:               ev.Subject.Content,
		CustomData:            ev.CustomData,
		CustomDataContentType: ev.CustomDataContentType,
	}
	if a.Content == nil {
		a.Content = map[string]any{}
	}
	if len(ev.Context.Inherits) > 0 {
		a.InheritsPresent = true
		// A JSON null inherits is present but not an array of strings. Each
		// element must itself be a JSON string: unmarshaling a null element
		// into a Go string is a silent no-op, so elements are checked raw.
		if string(bytes.TrimSpace(ev.Context.Inherits)) != "null" {
			var elems []json.RawMessage
			if json.Unmarshal(ev.Context.Inherits, &elems) == nil {
				uris := make([]string, 0, len(elems))
				wellFormed := true
				for _, e := range elems {
					s, ok := jsonString(e)
					if !ok {
						wellFormed = false
						break
					}
					uris = append(uris, s)
				}
				if wellFormed {
					a.InheritsWellFormed = true
					a.Inherits = uris
				}
			}
		}
	}
	return a, nil
}

// Refusal is a typed arrival refusal (utterance at fault).
type Refusal struct {
	Kind     string // "lineage-mismatch" | "undeclared-field" | "missing-required"
	Field    string // alphabetically first offender; empty for lineage-mismatch
	Layer    string // schema URI of the failing layer; empty for lineage-mismatch and undeclared-field
	Evidence string // non-empty; content not normative
}

// Minted is one layer's object of a resolution.
type Minted struct {
	SchemaURI             string
	TypeName              string
	Content               map[string]any
	CustomData            any
	CustomDataContentType string
}

// Resolution is a successful decompression: every layer, sanctioned first,
// own type last.
type Resolution struct {
	Minted []Minted
}

// ServeAt returns this resolution's minted object for schemaURI. A URI not in
// this event's resolution is not found; another layer's object is never
// substituted.
func (r *Resolution) ServeAt(schemaURI string) (*Minted, bool) {
	for i := range r.Minted {
		if r.Minted[i].SchemaURI == schemaURI {
			return &r.Minted[i], true
		}
	}
	return nil, false
}

// Outcome is the single result of Decompress. Exactly one branch is set.
type Outcome struct {
	Kind       string // "resolved" | "coinage" | "refused" | "fault"
	Resolution *Resolution
	Refusal    *Refusal
	Fault      string // non-empty iff Kind=="fault"
}

func refusedOutcome(kind, field, layer, format string, args ...any) *Outcome {
	return &Outcome{Kind: "refused", Refusal: &Refusal{
		Kind:     kind,
		Field:    field,
		Layer:    layer,
		Evidence: fmt.Sprintf(format, args...),
	}}
}

// Decompress applies rules A1-A10 in order and, on success, mints one object
// per lineage layer, sanctioned first, the event's own type last. Arrivals
// never mutate the catalog.
func Decompress(a *Arrival, c Catalog) *Outcome {
	// A1: an unlodged type is a coinage; nothing else is examined.
	own, ok := c.ByType(a.Type)
	if !ok {
		return &Outcome{Kind: "coinage"}
	}

	if len(own.Lineage) == 0 {
		// A2: a sanctioned event must not carry inherits, even empty.
		if a.InheritsPresent {
			return refusedOutcome("lineage-mismatch", "", "", "sanctioned type %q must not carry context.inherits", a.Type)
		}
	} else {
		// A3: a derived event's inherits must be present, well-formed, and
		// equal the recorded lineage entry for entry.
		switch {
		case !a.InheritsPresent:
			return refusedOutcome("lineage-mismatch", "", "", "derived type %q arrived without context.inherits", a.Type)
		case !a.InheritsWellFormed:
			return refusedOutcome("lineage-mismatch", "", "", "derived type %q carries a context.inherits that is not an array of strings", a.Type)
		case !equalStrings(a.Inherits, own.Lineage):
			return refusedOutcome("lineage-mismatch", "", "", "context.inherits of %q does not equal the catalog's recorded lineage", a.Type)
		}
	}

	// A4: every content field must be declared by the event's own schema.
	names := make([]string, 0, len(a.Content))
	for name := range a.Content {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := own.Fields[name]; !ok {
			return refusedOutcome("undeclared-field", name, "", "field %q is not declared by schema %q", name, own.SchemaURI)
		}
	}

	// A10 guard: a recorded lineage entry the catalog no longer resolves is a
	// catalog fault, never charged to the emitter.
	chain := make([]*Layer, 0, len(own.Lineage)+1)
	for _, uri := range own.Lineage {
		anc, ok := c.BySchemaURI(uri)
		if !ok {
			return &Outcome{Kind: "fault", Fault: fmt.Sprintf("catalog does not resolve lineage entry %q recorded for type %q", uri, a.Type)}
		}
		chain = append(chain, anc)
	}
	chain = append(chain, own)

	// A5: every layer's required names must be present, checked sanctioned
	// first. A sorted copy keeps the alphabetically-first report independent
	// of the Catalog implementation.
	for _, layer := range chain {
		required := append([]string(nil), layer.Required...)
		sort.Strings(required)
		for _, req := range required {
			if _, present := a.Content[req]; !present {
				return refusedOutcome("missing-required", req, layer.SchemaURI, "layer %q requires field %q which is absent", layer.SchemaURI, req)
			}
		}
	}

	// A6/A7: values are never judged; mint one object per layer with content
	// restricted to that layer's declared names and customData carried whole.
	minted := make([]Minted, 0, len(chain))
	for _, layer := range chain {
		content := make(map[string]any, len(layer.Fields))
		for name := range layer.Fields {
			if v, ok := a.Content[name]; ok {
				content[name] = v
			}
		}
		minted = append(minted, Minted{
			SchemaURI:             layer.SchemaURI,
			TypeName:              layer.TypeName,
			Content:               content,
			CustomData:            a.CustomData,
			CustomDataContentType: a.CustomDataContentType,
		})
	}
	return &Outcome{Kind: "resolved", Resolution: &Resolution{Minted: minted}}
}
