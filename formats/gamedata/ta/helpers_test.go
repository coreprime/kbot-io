package ta

import (
	"testing"

	"github.com/coreprime/kbot-io/formats/tdf"
)

// marshal writes v with tdf.Marshal, failing the test on error.
func marshal(t *testing.T, v any) string {
	t.Helper()
	out, err := tdf.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}
