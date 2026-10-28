package cmd

import (
	"bytes"
	"github.com/spf13/cobra"
	"os"
	"testing"
)

func TestPageCost(t *testing.T) {
	t.Chdir(t.TempDir())
	oldPrice, oldCount := costPagePrice, costDocCount
	defer func() { costPagePrice, costDocCount = oldPrice, oldCount }()
	costPagePrice, costDocCount = .001, 1000
	s := EvalSummary{Config: EvalConfig{Provider: "mistral-ocr"}, Results: []EvalResult{{Pages: 2}, {Pages: 4}}}
	if e := saveEvalResults(s, "pages.yaml"); e != nil {
		t.Fatal(e)
	}
	c := &cobra.Command{}
	c.Flags().Float64("page-price", 0, "")
	if e := c.Flags().Set("page-price", "0.001"); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	c.SetOut(&out)
	if e := runCost(c, []string{"./pages.yaml"}); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(out.Bytes(), []byte("Cost per document: $0.003000")) || !bytes.Contains(out.Bytes(), []byte("$3.00")) {
		t.Fatal(out.String())
	}
}
func TestNormalizedConfigAndMetrics(t *testing.T) {
	t.Chdir(t.TempDir())
	if e := os.WriteFile("gt.txt", []byte("Title"), 0600); e != nil {
		t.Fatal(e)
	}
	s := EvalSummary{Config: EvalConfig{Normalize: "markdown", SingleLine: true}, Results: []EvalResult{{ProviderResponse: "# Title", TranscriptPath: "gt.txt", Pages: 1}}}
	if e := saveEvalResults(s, "results.yaml"); e != nil {
		t.Fatal(e)
	}
	c, e := loadEvalConfig("results.yaml")
	if e != nil || c.Normalize != "markdown" || !c.SingleLine {
		t.Fatal(c, e)
	}
	m := CalculateAccuracyMetrics("Title", "# Title", nil, c.SingleLine, c.Normalize)
	if m.CharacterAccuracy != 1 || m.WordErrorRate != 0 {
		t.Fatal(m)
	}
	if validateNormalization("invented") == nil {
		t.Fatal("unknown normalization accepted")
	}
}

func TestSavedConfigCannotSelectCredentialEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("malicious.yaml", []byte("config:\n  provider: openai-compatible\n  endpoint: https://example.org/chat/completions\n  api_key_env: AWS_SECRET_ACCESS_KEY\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadEvalConfig("malicious.yaml")
	if err != nil || config.APIKeyEnv != "" {
		t.Fatalf("%+v %v", config, err)
	}
	config.APIKeyEnv = "AWS_SECRET_ACCESS_KEY"
	if err = saveEvalResults(EvalSummary{Config: config}, "resaved.yaml"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("resaved.yaml")
	if err != nil || bytes.Contains(b, []byte("AWS_SECRET_ACCESS_KEY")) || bytes.Contains(b, []byte("api_key_env")) {
		t.Fatalf("credential selector persisted: %s %v", b, err)
	}
}
