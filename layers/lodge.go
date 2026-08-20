package layers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// LodgeRefusal is a typed lodging refusal. Kind is one of the L-rule kinds.
type LodgeRefusal struct {
	Kind     string
	Evidence string // human-readable; non-empty; content not normative
}

// MemoryCatalog is the reference Catalog. Lodge is its only writer.
type MemoryCatalog struct {
	bySchemaURI map[string]*Layer
	byType      map[string]*Layer
	envelopes   map[string][]byte // exact lodged bytes per schemaUri, for L13
}

func NewMemoryCatalog() *MemoryCatalog {
	return &MemoryCatalog{
		bySchemaURI: map[string]*Layer{},
		byType:      map[string]*Layer{},
		envelopes:   map[string][]byte{},
	}
}

func (c *MemoryCatalog) BySchemaURI(uri string) (*Layer, bool) {
	l, ok := c.bySchemaURI[uri]
	return l, ok
}

func (c *MemoryCatalog) ByType(name string) (*Layer, bool) {
	l, ok := c.byType[name]
	return l, ok
}

// envelopeDoc is the lodging envelope. A malformed member (schemaUri not a
// string, lineage not an array of strings) fails to unmarshal and is refused
// as bad-schema; an absent lineage is the empty lineage.
type envelopeDoc struct {
	SchemaURI *string         `json:"schemaUri"`
	Lineage   []string        `json:"lineage"`
	Schema    json.RawMessage `json:"schema"`
}

type schemaDoc struct {
	Properties struct {
		Context struct {
			Properties struct {
				Type *typeDecl `json:"type"`
			} `json:"properties"`
		} `json:"context"`
		Subject struct {
			Properties struct {
				Content contentDecl `json:"content"`
			} `json:"properties"`
		} `json:"subject"`
	} `json:"properties"`
}

type typeDecl struct {
	Enum  []json.RawMessage `json:"enum"`
	Const json.RawMessage   `json:"const"`
}

type contentDecl struct {
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
}

// typeName accepts exactly two declaration shapes: an enum holding one
// string, or a const string. Both at once is ambiguous and rejected — the
// spec admits one shape or the other, not a combination.
func (td *typeDecl) typeName() (string, bool) {
	if td == nil {
		return "", false
	}
	enumPresent := td.Enum != nil
	constPresent := td.Const != nil
	if enumPresent == constPresent {
		return "", false
	}
	if enumPresent {
		if len(td.Enum) != 1 {
			return "", false
		}
		return jsonString(td.Enum[0])
	}
	return jsonString(td.Const)
}

// jsonString unmarshals raw only when it is a JSON string; a bare null would
// otherwise unmarshal into a string as a silent no-op.
func jsonString(raw json.RawMessage) (string, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(t, &s); err != nil {
		return "", false
	}
	return s, true
}

func isJSONFalse(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "false"
}

func lodgeRefusal(kind, format string, args ...any) *LodgeRefusal {
	return &LodgeRefusal{Kind: kind, Evidence: fmt.Sprintf(format, args...)}
}

// Lodge records one type in the catalog. Rules L1-L13 are checked in order;
// the first violation wins.
func (c *MemoryCatalog) Lodge(envelope []byte) (*Layer, *LodgeRefusal) {
	// L1: envelope and schema parse; type name extractable.
	var env envelopeDoc
	if err := json.Unmarshal(envelope, &env); err != nil {
		return nil, lodgeRefusal("bad-schema", "envelope does not parse: %v", err)
	}
	if env.SchemaURI == nil || *env.SchemaURI == "" {
		return nil, lodgeRefusal("bad-schema", "envelope has no schemaUri")
	}
	schemaURI := *env.SchemaURI
	if len(env.Schema) == 0 {
		return nil, lodgeRefusal("bad-schema", "envelope %q has no schema document", schemaURI)
	}
	var sd schemaDoc
	if err := json.Unmarshal(env.Schema, &sd); err != nil {
		return nil, lodgeRefusal("bad-schema", "schema %q does not parse: %v", schemaURI, err)
	}
	typeName, ok := sd.Properties.Context.Properties.Type.typeName()
	if !ok {
		return nil, lodgeRefusal("bad-schema", "schema %q declares no extractable type name (need an enum with exactly one string, or a const string)", schemaURI)
	}

	// L2: type name grammar.
	pn, ok := parseTypeName(typeName)
	if !ok {
		return nil, lodgeRefusal("bad-name", "type name %q does not match <namespace>.<subject>.<predicate>.<MAJOR>.<MINOR>.<PATCH>", typeName)
	}

	// L3: content closure.
	content := sd.Properties.Subject.Properties.Content
	if !isJSONFalse(content.AdditionalProperties) {
		return nil, lodgeRefusal("not-closed", "schema %q: subject.content additionalProperties must be exactly false", schemaURI)
	}

	fields := content.Properties
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	required := append([]string(nil), content.Required...)
	sort.Strings(required)

	// L4: every required name is a declared field.
	for _, r := range required {
		if _, ok := fields[r]; !ok {
			return nil, lodgeRefusal("required-undeclared", "schema %q requires %q which it does not declare", schemaURI, r)
		}
	}

	// L5: empty lineage iff sanctioned name.
	lineage := append([]string(nil), env.Lineage...)
	if pn.sanctioned() != (len(lineage) == 0) {
		return nil, lodgeRefusal("root-not-sanctioned", "type %q: a sanctioned name must have an empty lineage and a derived name a non-empty one (lineage has %d entries)", typeName, len(lineage))
	}

	// L6: no entry equals schemaUri.
	for _, u := range lineage {
		if u == schemaURI {
			return nil, lodgeRefusal("cycle", "lineage of %q contains itself", schemaURI)
		}
	}

	// L7: no entry appears twice.
	seen := map[string]bool{}
	for _, u := range lineage {
		if seen[u] {
			return nil, lodgeRefusal("duplicate-entry", "lineage of %q lists %q twice", schemaURI, u)
		}
		seen[u] = true
	}

	// L8: lineage length at most 7.
	if len(lineage) > 7 {
		return nil, lodgeRefusal("depth-exceeded", "lineage of %q has %d entries; the maximum is 7", schemaURI, len(lineage))
	}

	// L9-L12 apply only to derived lodgings (there is no parent otherwise).
	if len(lineage) > 0 {
		// L9: coherence against the parent's recorded lineage.
		parentURI := lineage[len(lineage)-1]
		parent, ok := c.bySchemaURI[parentURI]
		if !ok {
			return nil, lodgeRefusal("lineage-incoherent", "parent %q of %q is not in the catalog", parentURI, schemaURI)
		}
		if !equalStrings(lineage[:len(lineage)-1], parent.Lineage) {
			return nil, lodgeRefusal("lineage-incoherent", "lineage of %q minus its last entry does not equal the recorded lineage of parent %q", schemaURI, parentURI)
		}

		// L10: subject and predicate equal the parent's.
		if pn.Subject != parent.Subject || pn.Predicate != parent.Predicate {
			return nil, lodgeRefusal("cross-subject", "type %q has subject.predicate %s.%s but parent %q has %s.%s", typeName, pn.Subject, pn.Predicate, parent.TypeName, parent.Subject, parent.Predicate)
		}

		// L11: completeness — every parent field restated.
		parentFields := make([]string, 0, len(parent.Fields))
		for name := range parent.Fields {
			parentFields = append(parentFields, name)
		}
		sort.Strings(parentFields)
		for _, name := range parentFields {
			if _, ok := fields[name]; !ok {
				return nil, lodgeRefusal("incomplete", "schema %q omits field %q declared by parent %q", schemaURI, name, parentURI)
			}
		}

		// L12: additivity — no parent requirement relaxed.
		reqSet := map[string]bool{}
		for _, r := range required {
			reqSet[r] = true
		}
		for _, r := range parent.Required {
			if !reqSet[r] {
				return nil, lodgeRefusal("relaxed-obligation", "schema %q drops the requirement on %q imposed by parent %q", schemaURI, r, parentURI)
			}
		}
	}

	// L13: immutability.
	if prev, ok := c.envelopes[schemaURI]; ok {
		if bytes.Equal(prev, envelope) {
			return c.bySchemaURI[schemaURI], nil
		}
		return nil, lodgeRefusal("immutable", "%q is already lodged with different content", schemaURI)
	}

	layer := &Layer{
		SchemaURI: schemaURI,
		TypeName:  typeName,
		Namespace: pn.Namespace,
		Subject:   pn.Subject,
		Predicate: pn.Predicate,
		Version:   pn.Version,
		Fields:    fields,
		Required:  required,
		Lineage:   lineage,
	}
	c.bySchemaURI[schemaURI] = layer
	// The rules do not forbid two schemaUris declaring the same type name;
	// the by-type index simply records the most recent lodging.
	c.byType[typeName] = layer
	c.envelopes[schemaURI] = append([]byte(nil), envelope...)
	return layer, nil
}
