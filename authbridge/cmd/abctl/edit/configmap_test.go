package edit

import (
	"strings"
	"testing"
)

const fixtureMidYAML = `mode: proxy-sidecar

listener:
  forward_proxy_addr: ":8081"

pipeline:
  inbound:
    - name: jwt-validation
      config:
        issuer: http://idp
  outbound:
    - name: token-exchange

session:
  enabled: true
`

const fixtureLastYAML = `mode: proxy-sidecar

pipeline:
  inbound:
    - name: jwt-validation
`

const fixtureFirstYAML = `pipeline:
  inbound:
    - name: jwt-validation

mode: proxy-sidecar
`

const fixtureMissingYAML = `mode: proxy-sidecar

listener:
  forward_proxy_addr: ":8081"
`

func TestFindPipelineRange_Middle(t *testing.T) {
	start, end, err := FindPipelineRange([]byte(fixtureMidYAML))
	if err != nil {
		t.Fatalf("FindPipelineRange: %v", err)
	}
	got := fixtureMidYAML[start:end]
	if !strings.Contains(got, "pipeline:") {
		t.Fatalf("range missing pipeline header: %q", got)
	}
	if !strings.Contains(got, "token-exchange") {
		t.Fatalf("range missing pipeline body: %q", got)
	}
	if strings.Contains(got, "session:") {
		t.Fatalf("range includes next key: %q", got)
	}
	if strings.Contains(got, "listener:") {
		t.Fatalf("range includes prior key: %q", got)
	}
}

func TestFindPipelineRange_LastKey(t *testing.T) {
	start, end, err := FindPipelineRange([]byte(fixtureLastYAML))
	if err != nil {
		t.Fatalf("FindPipelineRange: %v", err)
	}
	if end != len(fixtureLastYAML) {
		t.Fatalf("end = %d, want len(yaml) = %d", end, len(fixtureLastYAML))
	}
	got := fixtureLastYAML[start:end]
	if !strings.Contains(got, "jwt-validation") {
		t.Fatalf("range missing pipeline body: %q", got)
	}
}

func TestFindPipelineRange_FirstKey(t *testing.T) {
	start, _, err := FindPipelineRange([]byte(fixtureFirstYAML))
	if err != nil {
		t.Fatalf("FindPipelineRange: %v", err)
	}
	if start != 0 {
		t.Fatalf("start = %d, want 0", start)
	}
}

func TestFindPipelineRange_Missing(t *testing.T) {
	_, _, err := FindPipelineRange([]byte(fixtureMissingYAML))
	if err == nil {
		t.Fatal("want error when pipeline key is absent")
	}
	if !strings.Contains(err.Error(), "pipeline") {
		t.Fatalf("error should mention pipeline: %v", err)
	}
}
