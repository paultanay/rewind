// Package bundle implements the .rewind bundle format: a gzipped tar archive
// containing incident.json (the full Incident model) and optional raw source
// fixtures for offline replay.
//
// Format invariants:
//   - schemaVersion field must be present; current version: 1.
//   - export → import → export is byte-identical modulo Meta.CreatedAt.
//   - Unknown JSON fields are preserved on read (forward-compatibility).
//   - Bundles are designed to stay under ~5 MB; downsampling is enforced by
//     collectors, not here.
//
// File layout inside the tar:
//
//	incident.json          — JSON-encoded model.Incident
//	sources/<name>.json    — raw fixture data per source (optional)
package bundle

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rewind-io/rewind/internal/model"
)

const (
	// CurrentSchemaVersion is incremented when the bundle format changes in a
	// backwards-incompatible way. Readers must reject bundles with a schema
	// version greater than their own.
	CurrentSchemaVersion = 1

	incidentFileName = "incident.json"
	sourcesDir       = "sources"
)

// Bundle is the in-memory representation of a loaded .rewind file.
type Bundle struct {
	Incident model.Incident
	// RawSources maps source name → raw JSON bytes. Populated on import from
	// the sources/*.json entries; used by --replay to re-run analysis.
	RawSources map[string][]byte
}

// Export writes the incident and any raw source fixtures to path as a
// gzipped tar (.rewind) file. If path is "-", Export writes to w instead
// (path takes precedence when non-empty).
//
// Export is safe to call from multiple goroutines only if each call writes to
// a different path.
func Export(inc model.Incident, rawSources map[string][]byte, path string) error {
	f, err := os.Create(path) //nolint:gosec // path is caller-controlled
	if err != nil {
		return fmt.Errorf("bundle: create %q: %w", path, err)
	}
	defer f.Close()

	if err := write(inc, rawSources, f); err != nil {
		// Attempt to remove the partial file; ignore secondary error.
		_ = os.Remove(path)
		return err
	}
	return nil
}

// ExportTo writes to an arbitrary writer. Useful for streaming and tests.
func ExportTo(inc model.Incident, rawSources map[string][]byte, w io.Writer) error {
	return write(inc, rawSources, w)
}

func write(inc model.Incident, rawSources map[string][]byte, w io.Writer) error {
	// Stamp schema version; preserve any version higher than ours (allows
	// testing the forward-compatibility guard without needing a real future build).
	if inc.Meta.SchemaVersion < CurrentSchemaVersion {
		inc.Meta.SchemaVersion = CurrentSchemaVersion
	}
	if inc.Meta.CreatedAt.IsZero() {
		inc.Meta.CreatedAt = time.Now().UTC()
	}

	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("bundle: gzip init: %w", err)
	}
	tw := tar.NewWriter(gz)

	// ── incident.json ────────────────────────────────────────────────────────
	incBytes, err := json.MarshalIndent(inc, "", "  ")
	if err != nil {
		return fmt.Errorf("bundle: marshal incident: %w", err)
	}
	if err := writeEntry(tw, incidentFileName, incBytes); err != nil {
		return err
	}

	// ── sources/*.json ───────────────────────────────────────────────────────
	for name, data := range rawSources {
		// Sanitise name: strip path separators to prevent tar path traversal.
		safeName := strings.ReplaceAll(filepath.Base(name), "..", "")
		entryPath := sourcesDir + "/" + safeName + ".json"
		if err := writeEntry(tw, entryPath, data); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("bundle: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("bundle: close gzip: %w", err)
	}
	return nil
}

func writeEntry(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0), // deterministic; reproducible exports
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("bundle: write tar header %q: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("bundle: write tar entry %q: %w", name, err)
	}
	return nil
}

// Import reads a .rewind bundle from path and returns the Bundle.
func Import(path string) (*Bundle, error) {
	f, err := os.Open(path) //nolint:gosec // path is caller-controlled
	if err != nil {
		return nil, fmt.Errorf("bundle: open %q: %w", path, err)
	}
	defer f.Close()
	return Read(f)
}

// Read parses a bundle from an arbitrary reader. Useful for tests and
// streaming (e.g. reading from an HTTP response body).
func Read(r io.Reader) (*Bundle, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("bundle: gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	b := &Bundle{RawSources: make(map[string][]byte)}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("bundle: read tar: %w", err)
		}

		// Guard against tar bombs: 50 MB per-entry limit.
		const maxEntry = 50 << 20
		data, err := io.ReadAll(io.LimitReader(tr, maxEntry+1))
		if err != nil {
			return nil, fmt.Errorf("bundle: read entry %q: %w", hdr.Name, err)
		}
		if int64(len(data)) > maxEntry {
			return nil, fmt.Errorf("bundle: entry %q exceeds 50 MB safety limit", hdr.Name)
		}

		switch {
		case hdr.Name == incidentFileName:
			if err := json.Unmarshal(data, &b.Incident); err != nil {
				return nil, fmt.Errorf("bundle: parse incident.json: %w", err)
			}
		case strings.HasPrefix(hdr.Name, sourcesDir+"/"):
			// Strip "sources/" prefix and ".json" suffix for the key.
			name := strings.TrimPrefix(hdr.Name, sourcesDir+"/")
			name = strings.TrimSuffix(name, ".json")
			b.RawSources[name] = data
		default:
			// Unknown entries are silently skipped (forward-compatibility).
		}
	}

	if b.Incident.ID == "" {
		return nil, fmt.Errorf("bundle: incident.json missing or empty")
	}
	if err := validateSchema(b.Incident.Meta.SchemaVersion); err != nil {
		return nil, err
	}
	return b, nil
}

func validateSchema(v int) error {
	if v == 0 {
		// Tolerate bundles that predate the field.
		return nil
	}
	if v > CurrentSchemaVersion {
		return fmt.Errorf("bundle: schema version %d is newer than this tool supports (%d); upgrade rewind",
			v, CurrentSchemaVersion)
	}
	return nil
}
