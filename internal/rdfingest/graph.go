// Package rdfingest parses descriptive RDF into a vocabulary-neutral graph
// and derives a small IIIF Presentation manifest from graph resources that
// identify digital images.
package rdfingest

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const rdfNamespace = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"

// Format identifies an RDF serialization accepted by Parse and Convert.
type Format string

const (
	FormatRDFXML   Format = "application/rdf+xml"
	FormatTurtle   Format = "text/turtle"
	FormatNTriples Format = "application/n-triples"
	FormatJSONLD   Format = "application/ld+json"
)

// TermKind distinguishes IRIs, blank nodes, and literal RDF terms.
type TermKind uint8

const (
	IRI TermKind = iota + 1
	Blank
	Literal
)

// Term is one RDF graph term. Language and Datatype apply only to literals.
type Term struct {
	Kind     TermKind
	Value    string
	Language string
	Datatype string
}

// Triple is one RDF subject-predicate-object statement.
type Triple struct {
	Subject   Term
	Predicate string
	Object    Term
}

// Graph is an in-memory RDF graph. RDF acquisition is already bounded by the
// collector's response-size limit, so materializing the graph keeps later
// normalization deterministic and independent of serialization syntax.
type Graph struct {
	triples []Triple
}

func (g *Graph) add(subject Term, predicate string, object Term) {
	if subject.Value == "" || predicate == "" {
		return
	}
	g.triples = append(g.triples, Triple{Subject: subject, Predicate: predicate, Object: object})
}

// Len returns the number of statements in the graph.
func (g Graph) Len() int { return len(g.triples) }

// Triples returns a copy of the graph statements.
func (g Graph) Triples() []Triple { return append([]Triple(nil), g.triples...) }

// Objects returns all objects for an exact subject and predicate IRI.
func (g Graph) Objects(subject, predicate string) []Term {
	var out []Term
	for _, triple := range g.triples {
		if triple.Subject.Value == subject && triple.Predicate == predicate {
			out = append(out, triple.Object)
		}
	}
	return out
}

// ParseOptions configure serialization parsing. BaseURL resolves relative RDF
// IRIs; it does not resolve ordinary string literals containing image paths.
type ParseOptions struct {
	Format  Format
	BaseURL string
}

// Parse decodes one supported RDF serialization into a graph.
func Parse(data []byte, opts ParseOptions) (Graph, error) {
	switch opts.Format {
	case FormatRDFXML:
		return parseRDFXML(data, opts.BaseURL)
	case FormatNTriples:
		return parseNTriples(data)
	case FormatTurtle:
		return parseTurtle(data, opts.BaseURL)
	case FormatJSONLD:
		return parseJSONLD(data, opts.BaseURL)
	default:
		return Graph{}, fmt.Errorf("rdf ingest: unsupported RDF format %q", opts.Format)
	}
}

type rdfXMLParser struct {
	decoder *xml.Decoder
	graph   Graph
	blank   int
}

func parseRDFXML(data []byte, baseURL string) (Graph, error) {
	p := &rdfXMLParser{decoder: xml.NewDecoder(strings.NewReader(string(data)))}
	for {
		token, err := p.decoder.Token()
		if errors.Is(err, io.EOF) {
			return Graph{}, errors.New("rdf ingest: RDF/XML has no rdf:RDF root")
		}
		if err != nil {
			return Graph{}, fmt.Errorf("rdf ingest: decoding RDF/XML: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Space != rdfNamespace || start.Name.Local != "RDF" {
			continue
		}
		base := inheritedAttr(start.Attr, "http://www.w3.org/XML/1998/namespace", "base", baseURL)
		lang := inheritedAttr(start.Attr, "http://www.w3.org/XML/1998/namespace", "lang", "")
		if err := p.parseRoot(start, base, lang); err != nil {
			return Graph{}, err
		}
		return p.graph, nil
	}
}

func (p *rdfXMLParser) parseRoot(root xml.StartElement, base, lang string) error {
	for {
		token, err := p.decoder.Token()
		if err != nil {
			return fmt.Errorf("rdf ingest: decoding RDF/XML root: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if _, err := p.parseNode(value, base, lang); err != nil {
				return err
			}
		case xml.EndElement:
			if value.Name == root.Name {
				return nil
			}
		}
	}
}

func (p *rdfXMLParser) parseNode(start xml.StartElement, inheritedBase, inheritedLang string) (Term, error) {
	base := inheritedAttr(start.Attr, "http://www.w3.org/XML/1998/namespace", "base", inheritedBase)
	lang := inheritedAttr(start.Attr, "http://www.w3.org/XML/1998/namespace", "lang", inheritedLang)
	subject, err := p.nodeSubject(start.Attr, base)
	if err != nil {
		return Term{}, err
	}
	if start.Name.Space != rdfNamespace || start.Name.Local != "Description" {
		p.graph.add(subject, rdfNamespace+"type", Term{Kind: IRI, Value: start.Name.Space + start.Name.Local})
	}
	for {
		token, err := p.decoder.Token()
		if err != nil {
			return Term{}, fmt.Errorf("rdf ingest: decoding RDF/XML node: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if err := p.parseProperty(subject, value, base, lang); err != nil {
				return Term{}, err
			}
		case xml.EndElement:
			if value.Name == start.Name {
				return subject, nil
			}
		}
	}
}

func (p *rdfXMLParser) parseProperty(subject Term, start xml.StartElement, inheritedBase, inheritedLang string) error {
	predicate := start.Name.Space + start.Name.Local
	base := inheritedAttr(start.Attr, "http://www.w3.org/XML/1998/namespace", "base", inheritedBase)
	lang := inheritedAttr(start.Attr, "http://www.w3.org/XML/1998/namespace", "lang", inheritedLang)
	if resource := attr(start.Attr, rdfNamespace, "resource"); resource != "" {
		p.graph.add(subject, predicate, Term{Kind: IRI, Value: resolveIRI(base, resource)})
		return p.consumeElement(start)
	}
	if nodeID := attr(start.Attr, rdfNamespace, "nodeID"); nodeID != "" {
		p.graph.add(subject, predicate, Term{Kind: Blank, Value: "_:" + nodeID})
		return p.consumeElement(start)
	}
	datatype := attr(start.Attr, rdfNamespace, "datatype")
	var text strings.Builder
	hadNode := false
	for {
		token, err := p.decoder.Token()
		if err != nil {
			return fmt.Errorf("rdf ingest: decoding RDF/XML property %s: %w", predicate, err)
		}
		switch value := token.(type) {
		case xml.CharData:
			text.Write(value)
		case xml.StartElement:
			object, parseErr := p.parseNode(value, base, lang)
			if parseErr != nil {
				return parseErr
			}
			p.graph.add(subject, predicate, object)
			hadNode = true
		case xml.EndElement:
			if value.Name == start.Name {
				if !hadNode {
					p.graph.add(subject, predicate, Term{
						Kind: Literal, Value: strings.TrimSpace(text.String()),
						Language: lang, Datatype: resolveIRI(base, datatype),
					})
				}
				return nil
			}
		}
	}
}

func (p *rdfXMLParser) nodeSubject(attrs []xml.Attr, base string) (Term, error) {
	if about := attr(attrs, rdfNamespace, "about"); about != "" {
		return Term{Kind: IRI, Value: resolveIRI(base, about)}, nil
	}
	if id := attr(attrs, rdfNamespace, "ID"); id != "" {
		return Term{Kind: IRI, Value: resolveIRI(base, "#"+id)}, nil
	}
	if nodeID := attr(attrs, rdfNamespace, "nodeID"); nodeID != "" {
		return Term{Kind: Blank, Value: "_:" + nodeID}, nil
	}
	p.blank++
	return Term{Kind: Blank, Value: fmt.Sprintf("_:generated-%d", p.blank)}, nil
}

func (p *rdfXMLParser) consumeElement(start xml.StartElement) error {
	depth := 1
	for depth > 0 {
		token, err := p.decoder.Token()
		if err != nil {
			return fmt.Errorf("rdf ingest: decoding RDF/XML element: %w", err)
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

func attr(attrs []xml.Attr, namespace, local string) string {
	for _, candidate := range attrs {
		if candidate.Name.Space == namespace && candidate.Name.Local == local {
			return candidate.Value
		}
	}
	return ""
}

func inheritedAttr(attrs []xml.Attr, namespace, local, inherited string) string {
	if value := attr(attrs, namespace, local); value != "" {
		return value
	}
	return inherited
}

func resolveIRI(base, ref string) string {
	if ref == "" {
		return ""
	}
	reference, err := url.Parse(ref)
	if err != nil || reference.IsAbs() || base == "" {
		return ref
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return ref
	}
	return baseURL.ResolveReference(reference).String()
}
