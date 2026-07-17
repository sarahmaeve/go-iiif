package rdfingest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// DetectFormat identifies the supported RDF serialization from a filename or
// URL extension, then falls back to conservative content sniffing.
func DetectFormat(data []byte, source string) (Format, error) {
	ext := strings.ToLower(filepath.Ext(strings.SplitN(source, "?", 2)[0]))
	switch ext {
	case ".rdf", ".xml":
		return FormatRDFXML, nil
	case ".ttl", ".turtle":
		return FormatTurtle, nil
	case ".nt", ".ntriples":
		return FormatNTriples, nil
	case ".jsonld", ".json":
		return FormatJSONLD, nil
	}
	trimmed := strings.TrimSpace(string(data))
	switch {
	case strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "["):
		return FormatJSONLD, nil
	case strings.HasPrefix(trimmed, "<?xml") || strings.Contains(trimmed[:min(len(trimmed), 512)], "<rdf:RDF"):
		return FormatRDFXML, nil
	case strings.HasPrefix(strings.ToLower(trimmed), "@prefix") || strings.HasPrefix(strings.ToLower(trimmed), "prefix"):
		return FormatTurtle, nil
	case strings.HasPrefix(trimmed, "<"):
		return FormatNTriples, nil
	default:
		return "", errors.New("rdf ingest: cannot detect RDF serialization; use a standard .rdf, .ttl, .nt, or .jsonld name")
	}
}

func parseNTriples(data []byte) (Graph, error) {
	var graph Graph
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 64<<10), len(data)+1)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		triple, err := parseNTripleLine(line)
		if err != nil {
			return Graph{}, fmt.Errorf("rdf ingest: N-Triples line %d: %w", lineNumber, err)
		}
		graph.add(triple.Subject, triple.Predicate, triple.Object)
	}
	if err := scanner.Err(); err != nil {
		return Graph{}, fmt.Errorf("rdf ingest: reading N-Triples: %w", err)
	}
	return graph, nil
}

func parseNTripleLine(line string) (Triple, error) {
	reader := &termReader{input: line}
	subject, err := reader.rdfTerm(false)
	if err != nil || subject.Kind == Literal {
		return Triple{}, errors.New("invalid subject")
	}
	predicate, err := reader.rdfTerm(false)
	if err != nil || predicate.Kind != IRI {
		return Triple{}, errors.New("invalid predicate")
	}
	object, err := reader.rdfTerm(true)
	if err != nil {
		return Triple{}, errors.New("invalid object")
	}
	reader.space()
	if !reader.take(".") {
		return Triple{}, errors.New("statement lacks terminating period")
	}
	reader.space()
	if reader.rest() != "" && !strings.HasPrefix(reader.rest(), "#") {
		return Triple{}, errors.New("unexpected content after statement")
	}
	return Triple{Subject: subject, Predicate: predicate.Value, Object: object}, nil
}

type termReader struct {
	input string
	pos   int
}

func (r *termReader) space() {
	for r.pos < len(r.input) && unicode.IsSpace(rune(r.input[r.pos])) {
		r.pos++
	}
}

func (r *termReader) take(prefix string) bool {
	if strings.HasPrefix(r.input[r.pos:], prefix) {
		r.pos += len(prefix)
		return true
	}
	return false
}

func (r *termReader) rest() string { return r.input[r.pos:] }

func (r *termReader) rdfTerm(allowLiteral bool) (Term, error) {
	r.space()
	if r.take("<") {
		end := strings.IndexByte(r.input[r.pos:], '>')
		if end < 0 {
			return Term{}, errors.New("unterminated IRI")
		}
		value := r.input[r.pos : r.pos+end]
		r.pos += end + 1
		return Term{Kind: IRI, Value: value}, nil
	}
	if strings.HasPrefix(r.rest(), "_:") {
		start := r.pos
		r.pos += 2
		for r.pos < len(r.input) && !unicode.IsSpace(rune(r.input[r.pos])) {
			r.pos++
		}
		return Term{Kind: Blank, Value: r.input[start:r.pos]}, nil
	}
	if allowLiteral && r.take("\"") {
		value, err := r.quoted()
		if err != nil {
			return Term{}, err
		}
		term := Term{Kind: Literal, Value: value}
		if r.take("@") {
			start := r.pos
			for r.pos < len(r.input) && (unicode.IsLetter(rune(r.input[r.pos])) || unicode.IsDigit(rune(r.input[r.pos])) || r.input[r.pos] == '-') {
				r.pos++
			}
			term.Language = r.input[start:r.pos]
		} else if r.take("^^") {
			datatype, datatypeErr := r.rdfTerm(false)
			if datatypeErr != nil || datatype.Kind != IRI {
				return Term{}, errors.New("invalid literal datatype")
			}
			term.Datatype = datatype.Value
		}
		return term, nil
	}
	return Term{}, errors.New("unrecognized RDF term")
}

func (r *termReader) quoted() (string, error) {
	var out strings.Builder
	for r.pos < len(r.input) {
		ch := r.input[r.pos]
		r.pos++
		if ch == '"' {
			return out.String(), nil
		}
		if ch != '\\' {
			out.WriteByte(ch)
			continue
		}
		if r.pos >= len(r.input) {
			return "", errors.New("unterminated literal escape")
		}
		escaped := r.input[r.pos]
		r.pos++
		switch escaped {
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case '"', '\\':
			out.WriteByte(escaped)
		default:
			return "", fmt.Errorf("unsupported literal escape \\%c", escaped)
		}
	}
	return "", errors.New("unterminated literal")
}

type turtleTokenKind uint8

const (
	turtleWord turtleTokenKind = iota + 1
	turtleIRI
	turtleString
	turtlePunct
)

type turtleToken struct {
	kind  turtleTokenKind
	value string
}

func parseTurtle(data []byte, baseURL string) (Graph, error) {
	tokens, err := lexTurtle(string(data))
	if err != nil {
		return Graph{}, err
	}
	p := turtleParser{tokens: tokens, prefixes: make(map[string]string), base: baseURL}
	return p.parse()
}

func lexTurtle(data string) ([]turtleToken, error) {
	var tokens []turtleToken
	for i := 0; i < len(data); {
		if unicode.IsSpace(rune(data[i])) {
			i++
			continue
		}
		if data[i] == '#' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		switch data[i] {
		case ';', ',', '.', '[', ']':
			tokens = append(tokens, turtleToken{kind: turtlePunct, value: data[i : i+1]})
			i++
		case '<':
			end := strings.IndexByte(data[i+1:], '>')
			if end < 0 {
				return nil, errors.New("rdf ingest: Turtle has an unterminated IRI")
			}
			tokens = append(tokens, turtleToken{kind: turtleIRI, value: data[i+1 : i+1+end]})
			i += end + 2
		case '"':
			reader := &termReader{input: data, pos: i + 1}
			value, quoteErr := reader.quoted()
			if quoteErr != nil {
				return nil, fmt.Errorf("rdf ingest: Turtle: %w", quoteErr)
			}
			tokens = append(tokens, turtleToken{kind: turtleString, value: value})
			i = reader.pos
		default:
			start := i
			for i < len(data) && !unicode.IsSpace(rune(data[i])) && !strings.ContainsRune(";,.[]<>", rune(data[i])) {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("rdf ingest: unexpected Turtle character %q", data[i])
			}
			tokens = append(tokens, turtleToken{kind: turtleWord, value: data[start:i]})
		}
	}
	return tokens, nil
}

type turtleParser struct {
	tokens   []turtleToken
	pos      int
	prefixes map[string]string
	base     string
	graph    Graph
}

func (p *turtleParser) parse() (Graph, error) {
	for p.pos < len(p.tokens) {
		if strings.EqualFold(p.peek().value, "@prefix") || strings.EqualFold(p.peek().value, "prefix") {
			if err := p.prefix(); err != nil {
				return Graph{}, err
			}
			continue
		}
		if err := p.statement(); err != nil {
			return Graph{}, err
		}
	}
	return p.graph, nil
}

func (p *turtleParser) prefix() error {
	p.pos++
	if p.pos+1 >= len(p.tokens) {
		return errors.New("rdf ingest: incomplete Turtle prefix declaration")
	}
	name, iri := p.tokens[p.pos], p.tokens[p.pos+1]
	p.pos += 2
	if name.kind != turtleWord || !strings.HasSuffix(name.value, ":") || iri.kind != turtleIRI {
		return errors.New("rdf ingest: invalid Turtle prefix declaration")
	}
	p.prefixes[strings.TrimSuffix(name.value, ":")] = resolveIRI(p.base, iri.value)
	if p.pos < len(p.tokens) && p.tokens[p.pos].value == "." {
		p.pos++
	}
	return nil
}

func (p *turtleParser) statement() error {
	subject, err := p.resource()
	if err != nil {
		return err
	}
	for {
		if p.pos >= len(p.tokens) {
			return errors.New("rdf ingest: incomplete Turtle statement")
		}
		predicateToken := p.tokens[p.pos]
		p.pos++
		predicate := rdfNamespace + "type"
		if predicateToken.value != "a" {
			predicate, err = p.expand(predicateToken)
			if err != nil {
				return err
			}
		}
		for {
			object, objectErr := p.object()
			if objectErr != nil {
				return objectErr
			}
			p.graph.add(subject, predicate, object)
			if p.pos < len(p.tokens) && p.tokens[p.pos].value == "," {
				p.pos++
				continue
			}
			break
		}
		if p.pos >= len(p.tokens) {
			return errors.New("rdf ingest: Turtle statement lacks terminating period")
		}
		switch p.tokens[p.pos].value {
		case ";":
			p.pos++
			if p.pos < len(p.tokens) && p.tokens[p.pos].value == "." {
				p.pos++
				return nil
			}
			continue
		case ".":
			p.pos++
			return nil
		default:
			return fmt.Errorf("rdf ingest: unexpected Turtle token %q", p.tokens[p.pos].value)
		}
	}
}

func (p *turtleParser) resource() (Term, error) {
	if p.pos >= len(p.tokens) {
		return Term{}, errors.New("rdf ingest: missing Turtle resource")
	}
	token := p.tokens[p.pos]
	p.pos++
	value, err := p.expand(token)
	if err != nil {
		return Term{}, err
	}
	kind := IRI
	if strings.HasPrefix(value, "_:") {
		kind = Blank
	}
	return Term{Kind: kind, Value: value}, nil
}

func (p *turtleParser) object() (Term, error) {
	if p.pos >= len(p.tokens) {
		return Term{}, errors.New("rdf ingest: missing Turtle object")
	}
	token := p.tokens[p.pos]
	p.pos++
	if token.kind == turtleString {
		term := Term{Kind: Literal, Value: token.value}
		if p.pos < len(p.tokens) && strings.HasPrefix(p.tokens[p.pos].value, "@") {
			term.Language = strings.TrimPrefix(p.tokens[p.pos].value, "@")
			p.pos++
		}
		return term, nil
	}
	if token.kind == turtleWord {
		if _, err := strconv.ParseFloat(token.value, 64); err == nil || token.value == "true" || token.value == "false" {
			return Term{Kind: Literal, Value: token.value}, nil
		}
	}
	p.pos--
	return p.resource()
}

func (p *turtleParser) expand(token turtleToken) (string, error) {
	if token.kind == turtleIRI {
		return resolveIRI(p.base, token.value), nil
	}
	if strings.HasPrefix(token.value, "_:") {
		return token.value, nil
	}
	prefix, local, ok := strings.Cut(token.value, ":")
	if !ok {
		return "", fmt.Errorf("rdf ingest: Turtle token %q is not an IRI or prefixed name", token.value)
	}
	base, found := p.prefixes[prefix]
	if !found {
		return "", fmt.Errorf("rdf ingest: Turtle prefix %q is not declared", prefix)
	}
	return base + local, nil
}

func (p *turtleParser) peek() turtleToken { return p.tokens[p.pos] }

type jsonLDContext struct {
	terms   map[string]string
	idTerms map[string]bool
	vocab   string
}

func parseJSONLD(data []byte, baseURL string) (Graph, error) {
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return Graph{}, fmt.Errorf("rdf ingest: decoding JSON-LD: %w", err)
	}
	context := jsonLDContext{terms: make(map[string]string), idTerms: make(map[string]bool)}
	var nodes []any
	switch root := document.(type) {
	case map[string]any:
		context.apply(root["@context"])
		if graph, ok := root["@graph"].([]any); ok {
			nodes = graph
		} else {
			nodes = []any{root}
		}
	case []any:
		nodes = root
	default:
		return Graph{}, errors.New("rdf ingest: JSON-LD root must be an object or array")
	}
	parser := jsonLDParser{context: context, base: baseURL}
	for _, node := range nodes {
		object, ok := node.(map[string]any)
		if !ok {
			return Graph{}, errors.New("rdf ingest: JSON-LD graph member is not an object")
		}
		if _, err := parser.node(object); err != nil {
			return Graph{}, err
		}
	}
	return parser.graph, nil
}

func (c *jsonLDContext) apply(raw any) {
	object, ok := raw.(map[string]any)
	if !ok {
		return
	}
	if vocab, ok := object["@vocab"].(string); ok {
		c.vocab = vocab
	}
	for term, definition := range object {
		if strings.HasPrefix(term, "@") {
			continue
		}
		switch value := definition.(type) {
		case string:
			c.terms[term] = c.expand(value)
		case map[string]any:
			if id, ok := value["@id"].(string); ok {
				c.terms[term] = c.expand(id)
			}
			c.idTerms[term] = value["@type"] == "@id"
		}
	}
}

func (c jsonLDContext) expand(value string) string {
	if strings.HasPrefix(value, "@") || isHTTPURL(value) {
		return value
	}
	if prefix, local, ok := strings.Cut(value, ":"); ok {
		if base := c.terms[prefix]; base != "" {
			return base + local
		}
		return value
	}
	if mapped := c.terms[value]; mapped != "" {
		return mapped
	}
	return c.vocab + value
}

type jsonLDParser struct {
	context jsonLDContext
	base    string
	blank   int
	graph   Graph
}

func (p *jsonLDParser) node(object map[string]any) (Term, error) {
	context := p.context
	context.apply(object["@context"])
	p.context = context
	subject := Term{Kind: IRI}
	if id, ok := object["@id"].(string); ok {
		subject.Value = resolveIRI(p.base, context.expand(id))
	} else {
		p.blank++
		subject = Term{Kind: Blank, Value: fmt.Sprintf("_:jsonld-%d", p.blank)}
	}
	for key, raw := range object {
		if key == "@context" || key == "@id" || key == "@graph" {
			continue
		}
		predicate := context.expand(key)
		if key == "@type" {
			predicate = rdfNamespace + "type"
		}
		values := []any{raw}
		if array, ok := raw.([]any); ok {
			values = array
		}
		for _, value := range values {
			term, err := p.value(value, context, key)
			if err != nil {
				return Term{}, err
			}
			p.graph.add(subject, predicate, term)
		}
	}
	return subject, nil
}

func (p *jsonLDParser) value(value any, context jsonLDContext, term string) (Term, error) {
	switch item := value.(type) {
	case string:
		if term == "@type" || context.idTerms[term] {
			return Term{Kind: IRI, Value: resolveIRI(p.base, context.expand(item))}, nil
		}
		return Term{Kind: Literal, Value: item}, nil
	case float64, bool:
		return Term{Kind: Literal, Value: fmt.Sprint(item)}, nil
	case map[string]any:
		if id, ok := item["@id"].(string); ok {
			return Term{Kind: IRI, Value: resolveIRI(p.base, context.expand(id))}, nil
		}
		if raw, ok := item["@value"]; ok {
			literal := Term{Kind: Literal, Value: fmt.Sprint(raw)}
			literal.Language, _ = item["@language"].(string)
			if datatype, ok := item["@type"].(string); ok {
				literal.Datatype = context.expand(datatype)
			}
			return literal, nil
		}
		return p.node(item)
	default:
		return Term{}, fmt.Errorf("rdf ingest: unsupported JSON-LD value %T", value)
	}
}

func absoluteURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.IsAbs()
}
