package preserve

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/source"
)

// LinkedResource is a non-image resource referenced by a Presentation
// document and worth preserving for offline research use.
type LinkedResource struct {
	URL    string
	Kind   string
	Format string
}

const (
	linkedAnnotation = "annotation"
	linkedSeeAlso    = "seeAlso"
	linkedContent    = "content"
)

// DiscoverLinkedResources finds external v2 AnnotationLists (otherContent),
// v3 AnnotationPages (annotations), and machine-readable seeAlso resources.
// Embedded annotations remain byte-faithful inside their containing JSON and
// need no separate request.
func DiscoverLinkedResources(document []byte) ([]LinkedResource, error) {
	return discoverLinkedResources(document, false)
}

func discoverLinkedResources(document []byte, includeBodies bool) ([]LinkedResource, error) {
	var root any
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, fmt.Errorf("preserve: decoding linked-resource document: %w", err)
	}
	seen := make(map[string]bool)
	var out []LinkedResource
	add := func(r LinkedResource) {
		if !absoluteResourceURL(r.URL) || seen[r.URL] {
			return
		}
		seen[r.URL] = true
		out = append(out, r)
	}
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			nodeType := strings.ToLower(stringValue(firstNonNil(v["type"], v["@type"])))
			motivation := strings.ToLower(stringValue(v["motivation"]))
			if includeBodies && strings.Contains(nodeType, "annotationcollection") {
				collectLinkedReferences(v["items"], linkedAnnotation, true, add)
				collectLinkedReferences(v["first"], linkedAnnotation, true, add)
				collectLinkedReferences(v["last"], linkedAnnotation, true, add)
			}
			for key, child := range v {
				switch key {
				case "otherContent":
					collectLinkedReferences(child, linkedAnnotation, true, add)
				case "annotations":
					collectLinkedReferences(child, linkedAnnotation, true, add)
				case "seeAlso":
					collectLinkedReferences(child, linkedSeeAlso, false, add)
				case "supplementary":
					collectLinkedReferences(child, linkedAnnotation, true, add)
				case "next":
					if includeBodies && (strings.Contains(nodeType, "annotationpage") || strings.Contains(nodeType, "annotationcollection")) {
						collectLinkedReferences(child, linkedAnnotation, true, add)
					}
				case "body", "resource":
					isNonPaintingAnnotation := strings.Contains(nodeType, "annotation") && !strings.Contains(motivation, "painting")
					if includeBodies || isNonPaintingAnnotation {
						collectContentReferences(child, add)
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(root)
	return out, nil
}

func collectLinkedReferences(value any, kind string, skipEmbedded bool, add func(LinkedResource)) {
	switch v := value.(type) {
	case string:
		add(LinkedResource{URL: v, Kind: kind})
	case []any:
		for _, item := range v {
			collectLinkedReferences(item, kind, skipEmbedded, add)
		}
	case map[string]any:
		if skipEmbedded && (v["items"] != nil || v["resources"] != nil) {
			return
		}
		add(LinkedResource{URL: linkedID(v), Kind: kind, Format: stringValue(v["format"])})
	}
}

func collectContentReferences(value any, add func(LinkedResource)) {
	switch v := value.(type) {
	case string:
		add(LinkedResource{URL: v, Kind: linkedContent})
	case []any:
		for _, item := range v {
			collectContentReferences(item, add)
		}
	case map[string]any:
		// TextualBody/ContentAsText and embedded annotation resources carry
		// their content inline. Painting images belong to the image pipeline.
		if v["value"] != nil || v["chars"] != nil {
			return
		}
		typ := strings.ToLower(stringValue(firstNonNil(v["type"], v["@type"])))
		if strings.Contains(typ, "choice") {
			collectContentReferences(v["items"], add)
			return
		}
		if strings.Contains(typ, "specificresource") {
			collectContentReferences(v["source"], add)
			return
		}
		if v["items"] != nil || v["resources"] != nil {
			return
		}
		format := stringValue(v["format"])
		if strings.Contains(typ, "image") || strings.HasPrefix(strings.ToLower(format), "image/") {
			return
		}
		add(LinkedResource{URL: linkedID(v), Kind: linkedContent, Format: format})
	}
}

func linkedID(v map[string]any) string {
	if id := stringValue(v["id"]); id != "" {
		return id
	}
	return stringValue(v["@id"])
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func absoluteResourceURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func linkedExtension(r LinkedResource) string {
	format := strings.ToLower(strings.TrimSpace(strings.Split(r.Format, ";")[0]))
	switch format {
	case "application/json", "application/ld+json":
		return ".json"
	case "application/xml", "text/xml", "application/alto+xml", "application/tei+xml":
		return ".xml"
	case "text/plain":
		return ".txt"
	case "text/html":
		return ".html"
	}
	if strings.HasSuffix(format, "+xml") {
		return ".xml"
	}
	if strings.HasSuffix(format, "+json") {
		return ".json"
	}
	if u, err := url.Parse(r.URL); err == nil {
		ext := strings.ToLower(path.Ext(u.Path))
		if len(ext) >= 2 && len(ext) <= 10 {
			return ext
		}
	}
	if r.Kind == linkedAnnotation {
		return ".json"
	}
	return ".bin"
}

const maxLinkedResourcesPerBundle = 10_000
const maxLinkedResourceBytes = 64 << 20

func preserveLinkedResources(ctx context.Context, fetcher source.Fetcher, store BlobStore, dir string, manifest []byte, previous []provenanceLinked, summary *Summary, progress func(LinkedProgressEvent)) ([]provenanceLinked, []provenanceLinkedFailure, error) {
	initial, err := DiscoverLinkedResources(manifest)
	if err != nil {
		summary.LinkedFailures = append(summary.LinkedFailures, err.Error())
		return append([]provenanceLinked(nil), previous...), []provenanceLinkedFailure{{Error: err.Error()}}, nil
	}
	previousByURL := make(map[string]provenanceLinked, len(previous))
	for _, item := range previous {
		previousByURL[item.URL] = item
	}
	queue := append([]LinkedResource(nil), initial...)
	seen := make(map[string]bool)
	var preserved []provenanceLinked
	var failures []provenanceLinkedFailure
	for len(queue) > 0 && len(seen) < maxLinkedResourcesPerBundle {
		if err := ctx.Err(); err != nil {
			return preserved, failures, err
		}
		resource := queue[0]
		queue = queue[1:]
		if seen[resource.URL] {
			continue
		}
		seen[resource.URL] = true
		summary.Linked++

		entry, hadPrevious := previousByURL[resource.URL]
		if !hadPrevious || entry.File == "" {
			entry = linkedProvenanceEntry(resource)
		}
		var data []byte
		if entry.File != "" {
			if existing, readErr := store.Get(ctx, dir+"/"+entry.File); readErr == nil &&
				linkedDigestOK(existing, entry.SHA256) && validateLinkedResource(resource, existing) == nil {
				data = existing
				entry.Kind = resource.Kind
				if resource.Format != "" {
					entry.Format = resource.Format
				}
				entry.SHA256 = linkedDigest(existing)
				summary.LinkedSkipped++
				if progress != nil {
					progress(LinkedProgressEvent{Index: summary.Linked, URL: resource.URL, File: entry.File, Action: "skipped"})
				}
			}
		}
		if data == nil {
			fetched, fetchErr := fetcher.Fetch(ctx, resource.URL)
			if fetchErr != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return preserved, failures, ctxErr
				}
				message := fmt.Sprintf("%s: %v", resource.URL, fetchErr)
				summary.LinkedFailures = append(summary.LinkedFailures, message)
				failures = append(failures, provenanceLinkedFailure{URL: resource.URL, Kind: resource.Kind, Error: fetchErr.Error()})
				if progress != nil {
					progress(LinkedProgressEvent{Index: summary.Linked, URL: resource.URL, Action: "failed"})
				}
				continue
			}
			if len(fetched) > maxLinkedResourceBytes {
				fetchErr = fmt.Errorf("resource exceeds %d-byte linked-resource limit", maxLinkedResourceBytes)
				message := fmt.Sprintf("%s: %v", resource.URL, fetchErr)
				summary.LinkedFailures = append(summary.LinkedFailures, message)
				failures = append(failures, provenanceLinkedFailure{URL: resource.URL, Kind: resource.Kind, Error: fetchErr.Error()})
				if progress != nil {
					progress(LinkedProgressEvent{Index: summary.Linked, URL: resource.URL, Action: "failed"})
				}
				continue
			}
			if validationErr := validateLinkedResource(resource, fetched); validationErr != nil {
				message := fmt.Sprintf("%s: %v", resource.URL, validationErr)
				summary.LinkedFailures = append(summary.LinkedFailures, message)
				failures = append(failures, provenanceLinkedFailure{URL: resource.URL, Kind: resource.Kind, Error: validationErr.Error()})
				if progress != nil {
					progress(LinkedProgressEvent{Index: summary.Linked, URL: resource.URL, Action: "failed"})
				}
				continue
			}
			data = fetched
			entry = linkedProvenanceEntry(resource)
			entry.SHA256 = linkedDigest(data)
			if putErr := store.Put(ctx, dir+"/"+entry.File, data); putErr != nil {
				message := fmt.Sprintf("%s: %v", resource.URL, putErr)
				summary.LinkedFailures = append(summary.LinkedFailures, message)
				failures = append(failures, provenanceLinkedFailure{URL: resource.URL, Kind: resource.Kind, Error: putErr.Error()})
				if progress != nil {
					progress(LinkedProgressEvent{Index: summary.Linked, URL: resource.URL, Action: "failed"})
				}
				continue
			}
			summary.LinkedStored++
			if progress != nil {
				progress(LinkedProgressEvent{Index: summary.Linked, URL: resource.URL, File: entry.File, Action: "stored"})
			}
		}
		preserved = append(preserved, entry)

		if resource.Kind == linkedAnnotation {
			if nested, nestedErr := discoverAnnotationLinks(data); nestedErr == nil {
				queue = append(queue, nested...)
			}
		}
	}
	if len(queue) > 0 {
		message := fmt.Sprintf("linked resource limit %d exceeded", maxLinkedResourcesPerBundle)
		summary.LinkedFailures = append(summary.LinkedFailures, message)
		failures = append(failures, provenanceLinkedFailure{Error: message})
	}
	for _, old := range previous {
		if !seen[old.URL] {
			preserved = append(preserved, old)
		}
	}
	return preserved, failures, nil
}

// discoverAnnotationLinks expands only recognized IIIF annotation graph
// documents. Generic JSON datasets may contain common keys such as body,
// resource, annotations, or seeAlso; treating those as IIIF would turn a
// single preservation reference into an unrelated recursive crawl.
func discoverAnnotationLinks(document []byte) ([]LinkedResource, error) {
	var root map[string]any
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, err
	}
	typ := strings.ToLower(stringValue(firstNonNil(root["type"], root["@type"])))
	if !strings.Contains(typ, "annotationpage") &&
		!strings.Contains(typ, "annotationcollection") &&
		!strings.Contains(typ, "annotationlist") {
		return nil, errors.New("linked annotation resource has an unrecognized IIIF annotation type")
	}
	return discoverLinkedResources(document, true)
}

func linkedDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func linkedProvenanceEntry(resource LinkedResource) provenanceLinked {
	digest := sha256.Sum256([]byte(resource.URL))
	return provenanceLinked{
		URL: resource.URL, Kind: resource.Kind, Format: resource.Format,
		File: fmt.Sprintf("resources/%x%s", digest[:], linkedExtension(resource)),
	}
}

func validateLinkedResource(resource LinkedResource, data []byte) error {
	if len(data) > maxLinkedResourceBytes {
		return fmt.Errorf("resource exceeds %d-byte linked-resource limit", maxLinkedResourceBytes)
	}
	if resource.Kind == linkedAnnotation && !json.Valid(data) {
		return errors.New("annotation resource is not valid JSON")
	}
	return nil
}

func linkedDigestOK(data []byte, want string) bool {
	return want == "" || linkedDigest(data) == want
}
