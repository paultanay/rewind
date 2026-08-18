package sources

import (
	"bytes"
	"testing"

	"github.com/paultanay/rewind/internal/model"
)

func TestFixtureRoundTrip(t *testing.T) {
	t.Parallel()
	want := CollectResult{
		Entities:   []model.Entity{{ID: "service/shop/checkout", Kind: model.EntityKindService}},
		Events:     []model.Event{{ID: "event-1", EntityID: "service/shop/checkout"}},
		Signals:    []model.Signal{{ID: "signal-1", EntityID: "service/shop/checkout", Metric: model.MetricErrorRate}},
		RawFixture: []byte(`{"status":"success"}`),
	}
	data, err := EncodeFixture("prometheus", want)
	if err != nil {
		t.Fatalf("EncodeFixture: %v", err)
	}
	got, recognized, err := DecodeFixture(data)
	if err != nil || !recognized {
		t.Fatalf("DecodeFixture = recognized %v, err %v", recognized, err)
	}
	if got.Source != "prometheus" || !bytes.Equal(got.Raw, want.RawFixture) || len(got.Signals) != 1 {
		t.Fatalf("fixture round trip lost data: %#v", got)
	}
}

func TestDecodeFixtureRecognizesLegacyRawData(t *testing.T) {
	t.Parallel()
	if _, recognized, err := DecodeFixture([]byte(`{"url":"http://prometheus"}`)); err != nil || recognized {
		t.Fatalf("legacy raw data = recognized %v, err %v; want unrecognized and tolerated", recognized, err)
	}
}
