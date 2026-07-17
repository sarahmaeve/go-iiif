package rdfingest

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Options configure RDF normalization and IIIF synthesis.
type Options struct {
	Format           Format
	SourceURL        string
	ImageBaseURL     string
	LocalSource      bool
	LocalImage       bool
	LocalImageWidth  int
	LocalImageHeight int
}

// Conversion is a derived Presentation 3 manifest plus the source identities
// needed by the preservation layer.
type Conversion struct {
	RecordURL    string
	ImageURL     string
	ImageWidth   int
	ImageHeight  int
	SourceURL    string
	SourceSHA256 string
	Manifest     []byte
}

type digitalObject struct {
	recordURL string
	labels    map[string][]string
	summaries map[string][]string
	metadata  []manifestMetadata
	image     digitalImage
}

type manifestMetadata struct {
	Label map[string][]string `json:"label"`
	Value map[string][]string `json:"value"`
}

type digitalImage struct {
	reference string
	width     int
	height    int
}

type candidate struct {
	reference string
	width     int
	height    int
}

// Convert parses descriptive RDF, identifies its primary digital image, and
// constructs a one-canvas IIIF Presentation 3 manifest. Recognition is based
// on graph relationships and predicate roles, not source hostnames.
func Convert(data []byte, opts Options) (Conversion, error) {
	if !opts.LocalSource {
		if err := validateHTTPURL(opts.SourceURL, "RDF source"); err != nil {
			return Conversion{}, err
		}
	}
	if opts.LocalImage && (opts.LocalImageWidth <= 0 || opts.LocalImageHeight <= 0) {
		return Conversion{}, errors.New("rdf ingest: local image requires positive pixel width and height")
	}
	graph, err := Parse(data, ParseOptions{Format: opts.Format, BaseURL: opts.SourceURL})
	if err != nil {
		return Conversion{}, err
	}
	object, err := normalize(graph, opts.LocalImage)
	if err != nil {
		return Conversion{}, err
	}
	sourceURL := opts.SourceURL
	if opts.LocalSource {
		sourceURL = object.recordURL + "#iiifpreserve-rdf-source"
	}
	imageURL := ""
	if opts.LocalImage {
		imageURL = strings.TrimRight(object.recordURL, "/") + "/iiifpreserve-primary-image.jpg"
		object.image.width, object.image.height = opts.LocalImageWidth, opts.LocalImageHeight
	} else {
		imageURL, err = resolveImageReference(object.image.reference, opts.ImageBaseURL)
		if err != nil {
			return Conversion{}, err
		}
	}
	manifest, err := presentation3(object, sourceURL, imageURL, opts.Format)
	if err != nil {
		return Conversion{}, err
	}
	digest := sha256.Sum256(data)
	return Conversion{
		RecordURL: object.recordURL, ImageURL: imageURL,
		ImageWidth: object.image.width, ImageHeight: object.image.height,
		SourceURL: sourceURL, SourceSHA256: fmt.Sprintf("%x", digest[:]),
		Manifest: manifest,
	}, nil
}

func normalize(graph Graph, localImageOverride bool) (digitalObject, error) {
	root, recordURL := graphRoot(graph)
	if root == "" {
		return digitalObject{}, errors.New("rdf ingest: cannot identify the described record")
	}
	labels := languageValues(graph, root, isLabelPredicate)
	if len(labels) == 0 {
		labels = map[string][]string{"none": {"RDF resource"}}
	}
	summaries := languageValues(graph, root, isSummaryPredicate)
	image, err := primaryImage(graph, root, localImageOverride)
	if err != nil {
		return digitalObject{}, err
	}
	return digitalObject{
		recordURL: recordURL, labels: labels, summaries: summaries,
		metadata: graphMetadata(graph, root), image: image,
	}, nil
}

func graphRoot(graph Graph) (root, recordURL string) {
	for _, triple := range graph.triples {
		if triple.Subject.Kind == IRI && triple.Object.Kind == IRI && localName(triple.Predicate) == "hasdocsem" && isHTTPURL(triple.Subject.Value) {
			return triple.Object.Value, triple.Subject.Value
		}
	}
	scores := make(map[string]int)
	for _, triple := range graph.triples {
		if triple.Subject.Kind != IRI || !isHTTPURL(triple.Subject.Value) {
			continue
		}
		if isLabelPredicate(triple.Predicate) {
			scores[triple.Subject.Value] += 2
		}
		if isImagePredicate(triple.Predicate) || isVisualLinkPredicate(triple.Predicate) {
			scores[triple.Subject.Value] += 3
		}
		if triple.Predicate == rdfNamespace+"type" {
			scores[triple.Subject.Value]++
		}
	}
	bestScore := 0
	for subject, score := range scores {
		if score > bestScore || (score == bestScore && (root == "" || subject < root)) {
			root, bestScore = subject, score
		}
	}
	return root, root
}

func languageValues(graph Graph, subject string, accepts func(string) bool) map[string][]string {
	out := make(map[string][]string)
	for _, triple := range graph.triples {
		if triple.Subject.Value != subject || triple.Object.Kind != Literal || !accepts(triple.Predicate) {
			continue
		}
		value := strings.TrimSpace(triple.Object.Value)
		if value == "" || looksLikeImage(value) {
			continue
		}
		lang := triple.Object.Language
		if lang == "" {
			lang = "none"
		}
		out[lang] = append(out[lang], value)
	}
	return out
}

func primaryImage(graph Graph, root string, allowIncomplete bool) (digitalImage, error) {
	var preferred string
	var linked []Term
	for _, triple := range graph.triples {
		if triple.Subject.Value != root {
			continue
		}
		if isImagePredicate(triple.Predicate) {
			if preferred == "" {
				preferred = triple.Object.Value
			}
			linked = append(linked, triple.Object)
			continue
		}
		if isVisualLinkPredicate(triple.Predicate) && triple.Object.Kind != Literal {
			linked = append(linked, triple.Object)
		}
	}
	var candidates []candidate
	seen := make(map[string]int)
	for _, term := range linked {
		candidate := imageCandidate(graph, term)
		if candidate.reference == "" {
			continue
		}
		if index, ok := seen[candidate.reference]; ok {
			if candidate.width > 0 && candidate.height > 0 {
				candidates[index].width, candidates[index].height = candidate.width, candidate.height
			}
			continue
		}
		seen[candidate.reference] = len(candidates)
		candidates = append(candidates, candidate)
	}
	if _, ok := seen[preferred]; preferred != "" && !ok {
		candidate := candidate{reference: preferred}
		if subject := referencedImageSubject(graph, preferred); subject != "" {
			candidate.width, candidate.height = imageDimensions(graph, subject)
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		if allowIncomplete {
			return digitalImage{}, nil
		}
		return digitalImage{}, errors.New("rdf ingest: graph has no identifiable image resource")
	}
	chosen := candidates[0]
	if preferred != "" {
		for _, candidate := range candidates {
			if candidate.reference == preferred {
				chosen = candidate
				break
			}
		}
	} else {
		for _, candidate := range candidates[1:] {
			if int64(candidate.width)*int64(candidate.height) > int64(chosen.width)*int64(chosen.height) {
				chosen = candidate
			}
		}
	}
	if chosen.width <= 0 || chosen.height <= 0 {
		if allowIncomplete {
			return digitalImage{reference: chosen.reference}, nil
		}
		return digitalImage{}, errors.New("rdf ingest: primary image has no pixel width and height")
	}
	return digitalImage{reference: chosen.reference, width: chosen.width, height: chosen.height}, nil
}

func imageCandidate(graph Graph, term Term) candidate {
	if looksLikeImage(term.Value) {
		width, height := imageDimensions(graph, term.Value)
		return candidate{reference: term.Value, width: width, height: height}
	}
	var result candidate
	for _, triple := range graph.triples {
		if triple.Subject.Value != term.Value {
			continue
		}
		if result.reference == "" && looksLikeImage(triple.Object.Value) {
			result.reference = triple.Object.Value
		}
	}
	result.width, result.height = imageDimensions(graph, term.Value)
	return result
}

func referencedImageSubject(graph Graph, reference string) string {
	for _, triple := range graph.triples {
		if triple.Subject.Value == reference {
			return reference
		}
	}
	return ""
}

func imageDimensions(graph Graph, subject string) (width, height int) {
	for _, triple := range graph.triples {
		if triple.Subject.Value != subject || triple.Object.Kind != Literal {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(triple.Object.Value))
		if err != nil || value <= 0 {
			continue
		}
		name := localName(triple.Predicate)
		switch {
		case strings.Contains(name, "width"):
			width = value
		case strings.Contains(name, "height"):
			height = value
		}
	}
	return width, height
}

func resolveImageReference(reference, imageBase string) (string, error) {
	ref, err := url.Parse(strings.TrimSpace(reference))
	if err != nil || reference == "" {
		return "", errors.New("rdf ingest: invalid image reference")
	}
	if ref.IsAbs() {
		if ref.Scheme != "https" && ref.Scheme != "http" {
			return "", errors.New("rdf ingest: image reference is not HTTP(S)")
		}
		return ref.String(), nil
	}
	if imageBase == "" {
		return "", errors.New("rdf ingest: relative image reference requires an image base URL or local image override")
	}
	base, err := url.Parse(imageBase)
	if err != nil || !base.IsAbs() || (base.Scheme != "https" && base.Scheme != "http") {
		return "", errors.New("rdf ingest: image base must be an absolute HTTP(S) URL")
	}
	return base.ResolveReference(ref).String(), nil
}

func presentation3(object digitalObject, sourceURL, imageURL string, format Format) ([]byte, error) {
	canvasID := object.recordURL + "#canvas-1"
	document := map[string]any{
		"@context": "http://iiif.io/api/presentation/3/context.json",
		"id":       object.recordURL, "type": "Manifest", "label": object.labels,
		"seeAlso": []any{map[string]any{"id": sourceURL, "type": "Dataset", "format": string(format)}},
		"items": []any{map[string]any{
			"id": canvasID, "type": "Canvas", "label": map[string][]string{"none": {"Image"}},
			"width": object.image.width, "height": object.image.height,
			"items": []any{map[string]any{
				"id": object.recordURL + "#painting-page-1", "type": "AnnotationPage",
				"items": []any{map[string]any{
					"id": object.recordURL + "#painting-1", "type": "Annotation", "motivation": "painting", "target": canvasID,
					"body": map[string]any{"id": imageURL, "type": "Image", "format": "image/jpeg", "width": object.image.width, "height": object.image.height},
				}},
			}},
		}},
	}
	if len(object.summaries) > 0 {
		document["summary"] = object.summaries
	}
	if len(object.metadata) > 0 {
		document["metadata"] = object.metadata
	}
	out, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rdf ingest: encoding Presentation 3 manifest: %w", err)
	}
	return append(out, '\n'), nil
}

type metadataRule struct {
	label      string
	predicates map[string]bool
}

var metadataRules = []metadataRule{
	{label: "Creator", predicates: names("creator", "author")},
	{label: "Date", predicates: names("date", "datecreated", "textdate", "p62e52p79hastimespanbeginning")},
	{label: "Identifier", predicates: names("identifier", "inventorynumber")},
	{label: "Department", predicates: names("department", "departmentname")},
	{label: "Current location", predicates: names("location", "p55hascurrentlocationexposed")},
	{label: "Provenance", predicates: names("provenance", "p27movedfrom")},
	{label: "Technique", predicates: names("technique", "artmedium")},
	{label: "Material", predicates: names("material", "artworksurface")},
	{label: "Tags", predicates: names("keywords", "taglabel")},
}

func graphMetadata(graph Graph, subject string) []manifestMetadata {
	var out []manifestMetadata
	for _, rule := range metadataRules {
		values := make(map[string][]string)
		seen := make(map[string]bool)
		for _, triple := range graph.triples {
			if triple.Subject.Value != subject || triple.Object.Kind != Literal || !rule.predicates[localName(triple.Predicate)] {
				continue
			}
			value := strings.TrimSpace(triple.Object.Value)
			value = normalizeMetadataValue(rule.label, value)
			if value == "" || seen[triple.Object.Language+"\x00"+value] {
				continue
			}
			seen[triple.Object.Language+"\x00"+value] = true
			lang := triple.Object.Language
			if lang == "" {
				lang = "none"
			}
			values[lang] = append(values[lang], value)
		}
		if len(values) > 0 {
			out = append(out, manifestMetadata{
				Label: map[string][]string{"en": {rule.label}}, Value: values,
			})
		}
	}
	return out
}

func normalizeMetadataValue(label, value string) string {
	if label == "Date" && len(value) > 4 && strings.Trim(value[4:], "0") == "" {
		return value[:4]
	}
	return value
}

func names(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func localName(iri string) string {
	if i := strings.LastIndexAny(iri, "#/"); i >= 0 {
		iri = iri[i+1:]
	}
	var normalized strings.Builder
	for _, r := range strings.ToLower(iri) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

func isLabelPredicate(iri string) bool {
	switch localName(iri) {
	case "title", "name", "preflabel", "p102e35p3hastitle":
		return true
	default:
		return false
	}
}

func isSummaryPredicate(iri string) bool {
	switch localName(iri) {
	case "description", "abstract", "content", "summary", "p3hasnote":
		return true
	default:
		return false
	}
}

func isImagePredicate(iri string) bool {
	switch localName(iri) {
	case "image", "mainimage", "primaryimage", "depiction", "isshownby", "thumbnailurl", "contenturl":
		return true
	default:
		return false
	}
}

func isVisualLinkPredicate(iri string) bool {
	name := localName(iri)
	return name == "p65showsvisualitem" || name == "visualitem" || name == "hasimage"
}

func looksLikeImage(value string) bool {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	switch strings.ToLower(path.Ext(u.Path)) {
	case ".jpg", ".jpeg", ".png", ".tif", ".tiff", ".webp", ".jp2":
		return true
	default:
		return false
	}
}

func validateHTTPURL(raw, label string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("rdf ingest: %s must be an absolute HTTP(S) URL", label)
	}
	return nil
}

func isHTTPURL(raw string) bool { return validateHTTPURL(raw, "URL") == nil }

// StableLanguageKeys is retained for callers that need deterministic language
// map presentation outside JSON's own sorted-key encoding.
func StableLanguageKeys(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
