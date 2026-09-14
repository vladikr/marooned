package agentproto

import (
	"bytes"
	"testing"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	env := Envelope{ID: "1", Method: MethodPing, OK: true}
	if err := WriteEnvelope(&buf, env); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEnvelope(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != MethodPing || got.ID != "1" || !got.OK {
		t.Fatalf("%+v", got)
	}
}
