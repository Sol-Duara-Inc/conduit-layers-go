package layers

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Mutation-verified: the closure check (A4), the lineage-equality check (A3),
// and the additivity check (L12) were each temporarily broken, the corpus
// failed, and the implementation was restored.

//go:embed testdata/corpus.json
var corpusJSON []byte

type corpusDoc struct {
	Counts struct {
		Catalog      int `json:"catalog"`
		LodgeCases   int `json:"lodgeCases"`
		ArrivalCases int `json:"arrivalCases"`
	} `json:"counts"`
	Catalog      []json.RawMessage `json:"catalog"`
	LodgeCases   []lodgeCase       `json:"lodgeCases"`
	ArrivalCases []arrivalCase     `json:"arrivalCases"`
}

type lodgeCase struct {
	Name   string          `json:"name"`
	Doc    json.RawMessage `json:"doc"`
	Expect struct {
		Outcome string `json:"outcome"`
		Kind    string `json:"kind"`
	} `json:"expect"`
}

type arrivalCase struct {
	Name   string          `json:"name"`
	Event  json.RawMessage `json:"event"`
	Expect struct {
		Outcome                 string         `json:"outcome"`
		Refusal                 *refusalExpect `json:"refusal"`
		Minted                  []mintedExpect `json:"minted"`
		CustomDataOnEveryMinted bool           `json:"customDataOnEveryMinted"`
		ServeAt                 []serveExpect  `json:"serveAt"`
	} `json:"expect"`
}

type refusalExpect struct {
	Kind  string `json:"kind"`
	Field string `json:"field"`
	Layer string `json:"layer"`
}

type mintedExpect struct {
	SchemaURI string         `json:"schemaUri"`
	Type      string         `json:"type"`
	Content   map[string]any `json:"content"`
}

type serveExpect struct {
	URI   string `json:"uri"`
	Found bool   `json:"found"`
}

func TestCorpusEmbedNonEmptyAndParses(t *testing.T) {
	if len(corpusJSON) == 0 {
		t.Fatal("embedded corpus is empty; the go:embed directive is missing or testdata/corpus.json is absent")
	}
	var c corpusDoc
	if err := json.Unmarshal(corpusJSON, &c); err != nil {
		t.Fatalf("embedded corpus does not parse: %v", err)
	}
}

func mustParseCorpus(t *testing.T) *corpusDoc {
	t.Helper()
	var c corpusDoc
	if err := json.Unmarshal(corpusJSON, &c); err != nil {
		t.Fatalf("corpus does not parse: %v", err)
	}
	return &c
}

// canonical re-marshals raw through Go's json package: maps marshal with
// sorted keys, which is the runner's canonical form.
func canonical(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("corpus fragment does not parse: %v", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("corpus fragment does not re-marshal: %v", err)
	}
	return out
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

func buildBase(t *testing.T, c *corpusDoc) *MemoryCatalog {
	t.Helper()
	cat := NewMemoryCatalog()
	for i, entry := range c.Catalog {
		layer, ref := cat.Lodge(canonical(t, entry))
		if ref != nil {
			t.Fatalf("catalog entry %d refused: %s: %s", i, ref.Kind, ref.Evidence)
		}
		if layer == nil {
			t.Fatalf("catalog entry %d lodged but returned no layer", i)
		}
	}
	return cat
}

// snapshot serializes the whole catalog state so before/after comparisons
// catch any mutation.
func snapshot(t *testing.T, c *MemoryCatalog) string {
	t.Helper()
	return mustMarshal(t, struct {
		BySchemaURI map[string]*Layer
		ByType      map[string]*Layer
		Envelopes   map[string][]byte
	}{c.bySchemaURI, c.byType, c.envelopes})
}

func TestCorpus(t *testing.T) {
	c := mustParseCorpus(t)

	if len(c.Catalog) != c.Counts.Catalog || len(c.LodgeCases) != c.Counts.LodgeCases || len(c.ArrivalCases) != c.Counts.ArrivalCases {
		t.Fatalf("corpus counts mismatch: declared catalog=%d lodgeCases=%d arrivalCases=%d, actual %d/%d/%d",
			c.Counts.Catalog, c.Counts.LodgeCases, c.Counts.ArrivalCases,
			len(c.Catalog), len(c.LodgeCases), len(c.ArrivalCases))
	}
	t.Logf("corpus case totals: catalog=%d lodgeCases=%d arrivalCases=%d", len(c.Catalog), len(c.LodgeCases), len(c.ArrivalCases))

	t.Run("Lodge", func(t *testing.T) {
		for _, lc := range c.LodgeCases {
			lc := lc
			t.Run(lc.Name, func(t *testing.T) {
				runLodgeCase(t, c, lc)
			})
		}
	})

	t.Run("Arrival", func(t *testing.T) {
		base := buildBase(t, c)
		before := snapshot(t, base)
		outcomes := map[string][2]*Outcome{}
		// Determinism sweep: every arrival case runs twice against the same
		// base catalog; both passes must agree and the catalog must not move.
		for pass := 0; pass < 2; pass++ {
			for _, ac := range c.ArrivalCases {
				ac := ac
				pass := pass
				t.Run(fmt.Sprintf("%s/pass%d", ac.Name, pass+1), func(t *testing.T) {
					out := runArrivalCase(t, base, ac)
					pair := outcomes[ac.Name]
					pair[pass] = out
					outcomes[ac.Name] = pair
				})
			}
		}
		for _, ac := range c.ArrivalCases {
			pair := outcomes[ac.Name]
			if pair[0] == nil || pair[1] == nil {
				continue // that case already failed and reported
			}
			if !reflect.DeepEqual(pair[0], pair[1]) {
				t.Errorf("case %s: the two determinism passes disagree", ac.Name)
			}
		}
		if after := snapshot(t, base); after != before {
			t.Error("arrivals changed the catalog")
		}
	})
}

func runLodgeCase(t *testing.T, c *corpusDoc, lc lodgeCase) {
	t.Helper()
	base := buildBase(t, c)
	docBytes := canonical(t, lc.Doc)
	if lc.Name == "idempotent-relodge" {
		// Both the case doc and the catalog's own envelope go through the
		// same canonicalization; assert they really are byte-identical, then
		// lodge the catalog's bytes.
		var doc struct {
			SchemaURI string `json:"schemaUri"`
		}
		if err := json.Unmarshal(lc.Doc, &doc); err != nil {
			t.Fatalf("case doc: %v", err)
		}
		envBytes := canonical(t, findCatalogEntry(t, c, doc.SchemaURI))
		if !bytes.Equal(envBytes, docBytes) {
			t.Fatal("idempotent-relodge doc is not byte-identical to the catalog envelope after canonicalization")
		}
		docBytes = envBytes
	}
	before := snapshot(t, base)
	layer, ref := base.Lodge(docBytes)
	switch lc.Expect.Outcome {
	case "lodged":
		if ref != nil {
			t.Fatalf("expected lodged, got refusal %s: %s", ref.Kind, ref.Evidence)
		}
		if layer == nil {
			t.Fatal("lodged but no layer returned")
		}
		if after := snapshot(t, base); after != before {
			t.Fatal("idempotent relodge changed the catalog")
		}
	case "refused":
		if ref == nil {
			t.Fatal("expected a refusal, got success")
		}
		if ref.Kind != lc.Expect.Kind {
			t.Fatalf("refusal kind = %q, want %q (evidence: %s)", ref.Kind, lc.Expect.Kind, ref.Evidence)
		}
		if ref.Evidence == "" {
			t.Fatal("refusal evidence is empty")
		}
		if layer != nil {
			t.Fatal("refused lodge also returned a layer")
		}
		if after := snapshot(t, base); after != before {
			t.Fatal("refused lodge changed the catalog")
		}
	default:
		t.Fatalf("corpus declares unknown lodge outcome %q", lc.Expect.Outcome)
	}
}

func findCatalogEntry(t *testing.T, c *corpusDoc, schemaURI string) json.RawMessage {
	t.Helper()
	for _, entry := range c.Catalog {
		var e struct {
			SchemaURI string `json:"schemaUri"`
		}
		if err := json.Unmarshal(entry, &e); err != nil {
			t.Fatalf("catalog entry: %v", err)
		}
		if e.SchemaURI == schemaURI {
			return entry
		}
	}
	t.Fatalf("no catalog entry for %q", schemaURI)
	return nil
}

func runArrivalCase(t *testing.T, base *MemoryCatalog, ac arrivalCase) *Outcome {
	t.Helper()
	evBytes := canonical(t, ac.Event)
	a, err := ParseArrival(evBytes)
	if err != nil {
		t.Fatalf("ParseArrival: %v", err)
	}
	out := Decompress(a, base)

	if out.Kind != ac.Expect.Outcome {
		t.Fatalf("outcome = %q, want %q", out.Kind, ac.Expect.Outcome)
	}
	assertOneBranch(t, out)

	switch out.Kind {
	case "refused":
		want := ac.Expect.Refusal
		if want == nil {
			t.Fatal("corpus case expects refused but declares no refusal")
		}
		if out.Refusal.Kind != want.Kind || out.Refusal.Field != want.Field || out.Refusal.Layer != want.Layer {
			t.Fatalf("refusal = {kind:%q field:%q layer:%q}, want {kind:%q field:%q layer:%q}",
				out.Refusal.Kind, out.Refusal.Field, out.Refusal.Layer, want.Kind, want.Field, want.Layer)
		}
		if out.Refusal.Evidence == "" {
			t.Fatal("refusal evidence is empty")
		}
	case "resolved":
		res := out.Resolution
		if len(res.Minted) != len(ac.Expect.Minted) {
			t.Fatalf("minted %d objects, want %d", len(res.Minted), len(ac.Expect.Minted))
		}
		for i, want := range ac.Expect.Minted {
			got := res.Minted[i]
			if got.SchemaURI != want.SchemaURI {
				t.Errorf("minted[%d].SchemaURI = %q, want %q", i, got.SchemaURI, want.SchemaURI)
			}
			if got.TypeName != want.Type {
				t.Errorf("minted[%d].TypeName = %q, want %q", i, got.TypeName, want.Type)
			}
			if g, w := mustMarshal(t, got.Content), mustMarshal(t, want.Content); g != w {
				t.Errorf("minted[%d].Content = %s, want %s", i, g, w)
			}
		}
		if ac.Expect.CustomDataOnEveryMinted {
			var ev struct {
				CustomData            any    `json:"customData"`
				CustomDataContentType string `json:"customDataContentType"`
			}
			if err := json.Unmarshal(evBytes, &ev); err != nil {
				t.Fatalf("event: %v", err)
			}
			for i, got := range res.Minted {
				if !reflect.DeepEqual(got.CustomData, ev.CustomData) {
					t.Errorf("minted[%d].CustomData does not deep-equal the event's", i)
				}
				if got.CustomDataContentType != ev.CustomDataContentType {
					t.Errorf("minted[%d].CustomDataContentType = %q, want %q", i, got.CustomDataContentType, ev.CustomDataContentType)
				}
			}
		}
		for _, sa := range ac.Expect.ServeAt {
			m, found := res.ServeAt(sa.URI)
			if found != sa.Found {
				t.Errorf("ServeAt(%q) found = %v, want %v", sa.URI, found, sa.Found)
				continue
			}
			if !found {
				continue
			}
			idx := -1
			for i := range res.Minted {
				if res.Minted[i].SchemaURI == sa.URI {
					idx = i
				}
			}
			if idx < 0 || m != &res.Minted[idx] {
				t.Errorf("ServeAt(%q) did not return the minted object itself", sa.URI)
			}
		}
	}
	return out
}

func assertOneBranch(t *testing.T, out *Outcome) {
	t.Helper()
	branches := 0
	if out.Resolution != nil {
		branches++
	}
	if out.Refusal != nil {
		branches++
	}
	if out.Fault != "" {
		branches++
	}
	switch out.Kind {
	case "resolved":
		if out.Resolution == nil || branches != 1 {
			t.Fatal("resolved outcome does not set exactly the Resolution branch")
		}
	case "refused":
		if out.Refusal == nil || branches != 1 {
			t.Fatal("refused outcome does not set exactly the Refusal branch")
		}
	case "fault":
		if out.Fault == "" || branches != 1 {
			t.Fatal("fault outcome does not set exactly the Fault branch")
		}
	case "coinage":
		if branches != 0 {
			t.Fatal("coinage outcome sets a branch")
		}
	default:
		t.Fatalf("unknown outcome kind %q", out.Kind)
	}
}

// --- unit tests beyond the corpus ---

func TestParseArrival(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		check   func(t *testing.T, a *Arrival)
	}{
		{name: "invalid-json", raw: `{`, wantErr: true},
		{name: "type-absent", raw: `{"context":{}}`, wantErr: true},
		{name: "type-empty", raw: `{"context":{"type":""}}`, wantErr: true},
		{name: "type-not-string", raw: `{"context":{"type":7}}`, wantErr: true},
		{name: "content-not-object", raw: `{"context":{"type":"a.b.c.0.1.0"},"subject":{"content":"x"}}`, wantErr: true},
		{name: "content-type-not-string", raw: `{"context":{"type":"a.b.c.0.1.0"},"customDataContentType":5}`, wantErr: true},
		{name: "minimal", raw: `{"context":{"type":"a.b.c.0.1.0"}}`, check: func(t *testing.T, a *Arrival) {
			if a.InheritsPresent || a.InheritsWellFormed || a.Inherits != nil {
				t.Error("absent inherits must be recorded absent")
			}
			if a.Content == nil || len(a.Content) != 0 {
				t.Error("absent content must be an empty map")
			}
			if a.CustomData != nil || a.CustomDataContentType != "" {
				t.Error("absent customData must be nil with an empty content type")
			}
		}},
		{name: "inherits-empty-array", raw: `{"context":{"type":"a.b.c.0.1.0","inherits":[]}}`, check: func(t *testing.T, a *Arrival) {
			if !a.InheritsPresent || !a.InheritsWellFormed || a.Inherits == nil || len(a.Inherits) != 0 {
				t.Errorf("empty-array inherits: present=%v wellFormed=%v inherits=%v", a.InheritsPresent, a.InheritsWellFormed, a.Inherits)
			}
		}},
		{name: "inherits-not-array", raw: `{"context":{"type":"a.b.c.0.1.0","inherits":"x"}}`, check: func(t *testing.T, a *Arrival) {
			if !a.InheritsPresent || a.InheritsWellFormed || a.Inherits != nil {
				t.Error("non-array inherits must be present and malformed")
			}
		}},
		{name: "inherits-non-string-element", raw: `{"context":{"type":"a.b.c.0.1.0","inherits":["x",3]}}`, check: func(t *testing.T, a *Arrival) {
			if !a.InheritsPresent || a.InheritsWellFormed {
				t.Error("array with a non-string element must be present and malformed")
			}
		}},
		{name: "inherits-null", raw: `{"context":{"type":"a.b.c.0.1.0","inherits":null}}`, check: func(t *testing.T, a *Arrival) {
			if !a.InheritsPresent || a.InheritsWellFormed {
				t.Error("null inherits must be present and malformed")
			}
		}},
		{name: "inherits-null-element", raw: `{"context":{"type":"a.b.c.0.1.0","inherits":["x",null]}}`, check: func(t *testing.T, a *Arrival) {
			if !a.InheritsPresent || a.InheritsWellFormed || a.Inherits != nil {
				t.Error("array with a null element must be present and malformed")
			}
		}},
		{name: "full", raw: `{"context":{"type":"a.b.c.0.1.0","inherits":["u1","u2"]},"subject":{"content":{"k":null}},"customData":{"d":[1]},"customDataContentType":"application/json"}`, check: func(t *testing.T, a *Arrival) {
			if !a.InheritsWellFormed || !equalStrings(a.Inherits, []string{"u1", "u2"}) {
				t.Errorf("inherits = %v", a.Inherits)
			}
			if v, ok := a.Content["k"]; !ok || v != nil {
				t.Error("a null field value must be present with a nil value")
			}
			if a.CustomDataContentType != "application/json" || a.CustomData == nil {
				t.Error("customData surface not carried")
			}
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			a, err := ParseArrival([]byte(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected a parse error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseArrival: %v", err)
			}
			if tc.check != nil {
				tc.check(t, a)
			}
		})
	}
}

const rootEnvelope = `{
  "schemaUri": "https://example.test/root",
  "lineage": [],
  "schema": {"properties": {
    "context": {"properties": {"type": {"const": "dev.cdevents.build.started.0.1.0"}}},
    "subject": {"properties": {"content": {"properties": {"a": {}}, "required": ["a"], "additionalProperties": false}}}
  }}
}`

func TestLodgeRefusals(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		kind string
	}{
		{"envelope-not-json", `{`, "bad-schema"},
		{"schemaUri-absent", `{"lineage":[],"schema":{}}`, "bad-schema"},
		{"schemaUri-not-string", `{"schemaUri":5,"lineage":[],"schema":{}}`, "bad-schema"},
		{"schema-absent", `{"schemaUri":"https://example.test/x","lineage":[]}`, "bad-schema"},
		{"schema-not-object", `{"schemaUri":"https://example.test/x","lineage":[],"schema":"s"}`, "bad-schema"},
		{"lineage-not-array", `{"schemaUri":"https://example.test/x","lineage":"nope","schema":{}}`, "bad-schema"},
		{"type-decl-absent", `{"schemaUri":"https://example.test/x","lineage":[],"schema":{"properties":{}}}`, "bad-schema"},
		{"type-enum-two", `{"schemaUri":"https://example.test/x","lineage":[],"schema":{"properties":{"context":{"properties":{"type":{"enum":["a.b.c.0.1.0","d.b.c.0.1.0"]}}}}}}`, "bad-schema"},
		{"type-enum-non-string", `{"schemaUri":"https://example.test/x","lineage":[],"schema":{"properties":{"context":{"properties":{"type":{"enum":[3]}}}}}}`, "bad-schema"},
		{"type-const-null", `{"schemaUri":"https://example.test/x","lineage":[],"schema":{"properties":{"context":{"properties":{"type":{"const":null}}}}}}`, "bad-schema"},
		{"type-enum-and-const", `{"schemaUri":"https://example.test/x","lineage":[],"schema":{"properties":{"context":{"properties":{"type":{"enum":["a.b.c.0.1.0"],"const":"a.b.c.0.1.0"}}}}}}`, "bad-schema"},
		{"required-not-strings", `{"schemaUri":"https://example.test/x","lineage":[],"schema":{"properties":{"context":{"properties":{"type":{"const":"dev.cdevents.b.c.0.1.0"}}},"subject":{"properties":{"content":{"properties":{},"required":[1],"additionalProperties":false}}}}}}`, "bad-schema"},
		{"name-too-short", `{"schemaUri":"https://example.test/x","lineage":[],"schema":{"properties":{"context":{"properties":{"type":{"const":"b.c.0.1.0"}}},"subject":{"properties":{"content":{"properties":{},"additionalProperties":false}}}}}}`, "bad-name"},
		{"name-version-not-digits", `{"schemaUri":"https://example.test/x","lineage":[],"schema":{"properties":{"context":{"properties":{"type":{"const":"dev.cdevents.b.c.0.1.x"}}},"subject":{"properties":{"content":{"properties":{},"additionalProperties":false}}}}}}`, "bad-name"},
		{"name-subject-not-lower", `{"schemaUri":"https://example.test/x","lineage":[],"schema":{"properties":{"context":{"properties":{"type":{"const":"dev.cdevents.b1.c.0.1.0"}}},"subject":{"properties":{"content":{"properties":{},"additionalProperties":false}}}}}}`, "bad-name"},
		{"closure-absent", `{"schemaUri":"https://example.test/x","lineage":[],"schema":{"properties":{"context":{"properties":{"type":{"const":"dev.cdevents.b.c.0.1.0"}}},"subject":{"properties":{"content":{"properties":{}}}}}}}`, "not-closed"},
		{"parent-missing", `{"schemaUri":"https://example.test/x","lineage":["https://example.test/never"],"schema":{"properties":{"context":{"properties":{"type":{"const":"com.x.b.c.0.1.0"}}},"subject":{"properties":{"content":{"properties":{},"additionalProperties":false}}}}}}`, "lineage-incoherent"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cat := NewMemoryCatalog()
			layer, ref := cat.Lodge([]byte(tc.doc))
			if ref == nil {
				t.Fatalf("expected refusal %q, got success (%v)", tc.kind, layer)
			}
			if ref.Kind != tc.kind {
				t.Fatalf("refusal kind = %q, want %q (evidence: %s)", ref.Kind, tc.kind, ref.Evidence)
			}
			if ref.Evidence == "" {
				t.Fatal("refusal evidence is empty")
			}
		})
	}
}

func TestLodgeRecordsTheLayer(t *testing.T) {
	cat := NewMemoryCatalog()
	layer, ref := cat.Lodge([]byte(rootEnvelope))
	if ref != nil {
		t.Fatalf("refused: %s: %s", ref.Kind, ref.Evidence)
	}
	if layer.SchemaURI != "https://example.test/root" ||
		layer.TypeName != "dev.cdevents.build.started.0.1.0" ||
		layer.Namespace != "dev.cdevents" ||
		layer.Subject != "build" ||
		layer.Predicate != "started" ||
		layer.Version != "0.1.0" {
		t.Fatalf("layer identity wrong: %+v", layer)
	}
	if !sort.StringsAreSorted(layer.Required) {
		t.Fatal("Required is not sorted")
	}
	if len(layer.Lineage) != 0 {
		t.Fatal("sanctioned layer carries a lineage")
	}
	if got, ok := cat.BySchemaURI(layer.SchemaURI); !ok || got != layer {
		t.Fatal("BySchemaURI does not return the lodged layer")
	}
	if got, ok := cat.ByType(layer.TypeName); !ok || got != layer {
		t.Fatal("ByType does not return the lodged layer")
	}
	if _, ok := cat.BySchemaURI("https://example.test/absent"); ok {
		t.Fatal("BySchemaURI found an absent URI")
	}
	if _, ok := cat.ByType("no.such.type.0.0.0"); ok {
		t.Fatal("ByType found an absent type")
	}
}

func TestLodgeDeepNamespaceName(t *testing.T) {
	doc := strings.Replace(rootEnvelope, "dev.cdevents.build.started.0.1.0", "org.example.platform.build.started.12.34.56", 1)
	doc = strings.Replace(doc, `"lineage": []`, `"lineage": ["https://example.test/root"]`, 1)
	doc = strings.Replace(doc, `https://example.test/root"`, `https://example.test/child"`, 1)
	cat := NewMemoryCatalog()
	if _, ref := cat.Lodge([]byte(rootEnvelope)); ref != nil {
		t.Fatalf("root refused: %s", ref.Kind)
	}
	layer, ref := cat.Lodge([]byte(doc))
	if ref != nil {
		t.Fatalf("refused: %s: %s", ref.Kind, ref.Evidence)
	}
	if layer.Namespace != "org.example.platform" || layer.Version != "12.34.56" {
		t.Fatalf("parsed name wrong: %+v", layer)
	}
}

// faultCatalog simulates a corrupted catalog for the A10 guard: the type
// resolves but a lineage entry does not.
type faultCatalog struct{ layer *Layer }

func (f *faultCatalog) BySchemaURI(string) (*Layer, bool) { return nil, false }
func (f *faultCatalog) ByType(name string) (*Layer, bool) {
	if name == f.layer.TypeName {
		return f.layer, true
	}
	return nil, false
}

func TestDecompressFault(t *testing.T) {
	layer := &Layer{
		SchemaURI: "https://example.test/leaf",
		TypeName:  "com.x.build.started.0.1.0",
		Fields:    map[string]json.RawMessage{},
		Lineage:   []string{"https://example.test/gone"},
	}
	a, err := ParseArrival([]byte(`{"context":{"type":"com.x.build.started.0.1.0","inherits":["https://example.test/gone"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	out := Decompress(a, &faultCatalog{layer})
	if out.Kind != "fault" || out.Fault == "" || out.Refusal != nil || out.Resolution != nil {
		t.Fatalf("expected a fault outcome, got %+v", out)
	}
}

func TestServeAtMiss(t *testing.T) {
	r := &Resolution{}
	if m, ok := r.ServeAt("https://example.test/anything"); ok || m != nil {
		t.Fatal("empty resolution served an object")
	}
}
