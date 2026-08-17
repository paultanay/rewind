package sources

import (
	"encoding/json"
	"fmt"

	"github.com/paultanay/rewind/internal/model"
)

// CurrentFixtureSchemaVersion identifies the source-result envelope stored in
// bundle sources/*.json entries. It is separate from the outer bundle schema
// so collectors can evolve their recorded payload without changing incident
// archive structure.
const CurrentFixtureSchemaVersion = 1

// Fixture is the replayable, source-normalized output of one collector run.
// Raw contains the collector's native response metadata/payload when the
// adapter recorded it; the model fields are sufficient for offline analysis.
type Fixture struct {
	SchemaVersion int             `json:"schemaVersion"`
	Source        string          `json:"source"`
	Entities      []model.Entity  `json:"entities,omitempty"`
	Events        []model.Event   `json:"events,omitempty"`
	Signals       []model.Signal  `json:"signals,omitempty"`
	Raw           json.RawMessage `json:"raw,omitempty"`
}

// EncodeFixture serializes one collector result into a deterministic JSON
// envelope suitable for a .rewind bundle.
func EncodeFixture(source string, result CollectResult) ([]byte, error) {
	if source == "" {
		return nil, fmt.Errorf("fixture source name is empty")
	}
	fixture := Fixture{
		SchemaVersion: CurrentFixtureSchemaVersion,
		Source:        source,
		Entities:      result.Entities,
		Events:        result.Events,
		Signals:       result.Signals,
	}
	if len(result.RawFixture) > 0 {
		fixture.Raw = append(json.RawMessage(nil), result.RawFixture...)
	}
	return json.Marshal(fixture)
}

// DecodeFixture decodes a current fixture. recognized is false for legacy raw
// source entries, allowing callers to fall back to the incident snapshot.
func DecodeFixture(data []byte) (fixture Fixture, recognized bool, err error) {
	var header struct {
		SchemaVersion *int   `json:"schemaVersion"`
		Source        string `json:"source"`
	}
	if err := json.Unmarshal(data, &header); err != nil || header.SchemaVersion == nil || header.Source == "" {
		return Fixture{}, false, nil
	}
	if *header.SchemaVersion > CurrentFixtureSchemaVersion {
		return Fixture{}, true, fmt.Errorf("fixture schema version %d is newer than supported version %d", *header.SchemaVersion, CurrentFixtureSchemaVersion)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		return Fixture{}, true, fmt.Errorf("decode %s fixture: %w", header.Source, err)
	}
	return fixture, true, nil
}
