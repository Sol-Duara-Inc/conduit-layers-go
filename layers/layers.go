// Package layers records derived CDEvent types in a catalog and decompresses
// arriving events into one minted object per lineage layer.
//
// A derived CDEvent is a compressed object: one subject.content carrying the
// vocabularies of every type in its lineage at once. Only two things are
// normative about a schema: the set of field names it declares and which
// names are required. Every other schema keyword is advisory and never
// enforced here.
package layers

import "encoding/json"

// Layer is one lodged type as the catalog records it.
type Layer struct {
	SchemaURI string
	TypeName  string
	Namespace string
	Subject   string
	Predicate string
	Version   string
	Fields    map[string]json.RawMessage // name -> opaque description; never interpreted
	Required  []string                   // sorted
	Lineage   []string                   // ordered ancestor schema URIs; empty = root
}

// Catalog is the read seam Decompress resolves against.
type Catalog interface {
	BySchemaURI(uri string) (*Layer, bool)
	ByType(name string) (*Layer, bool)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
