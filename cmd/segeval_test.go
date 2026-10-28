package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v3"
)

func TestSegEvalFormatsAndBackfill(t *testing.T) {
	root, e := filepath.Abs("..")
	if e != nil {
		t.Fatal(e)
	}
	t.Chdir(t.TempDir())
	gt := filepath.Join(root, "fixtures", "segeval", "page.xml")
	for _, format := range []string{"page.kraken.json", "page.htr.json", "page.iiif.json"} {
		t.Run(format, func(t *testing.T) {
			out := filepath.Join("evals", "segmentation", format+".yaml")
			command := newSegmentationCommand(true)
			var b bytes.Buffer
			command.SetOut(&b)
			command.SetArgs([]string{"--gt", gt, "--segmentation", filepath.Join(root, "fixtures", "segeval", format), "--profile", "letter", "-o", out})
			if e := command.Execute(); e != nil {
				t.Fatal(e)
			}
			s, e := loadSegSummary(out)
			if e != nil {
				t.Fatal(e)
			}
			if len(s.Results) != 1 || s.Results[0].Report.Score != 1 || s.Results[0].Report.Primary != "cbad" {
				t.Fatal(s)
			}
			if !strings.Contains(b.String(), "score=1.000000") {
				t.Fatal(b.String())
			}
			s.Results[0].Report.Score = 0
			if e = saveSegSummary(out, s); e != nil {
				t.Fatal(e)
			}
			if e = backfillSegmentation(&cobra.Command{}); e != nil {
				t.Fatal(e)
			}
			s, e = loadSegSummary(out)
			if e != nil || s.Results[0].Report.Score != 1 {
				t.Fatal(s, e)
			}
		})
	}
	var csv bytes.Buffer
	if e = segmentationCSV(&csv); e != nil {
		t.Fatal(e)
	}
	if strings.Count(csv.String(), "1.000000") < 3 {
		t.Fatal(csv.String())
	}
}
func TestSegCSVAndSelection(t *testing.T) {
	root, e := filepath.Abs("..")
	if e != nil {
		t.Fatal(e)
	}
	t.Chdir(t.TempDir())
	csv := "ground_truth,segmentation,profile\n" + filepath.Join(root, "fixtures/segeval/page.xml") + "," + filepath.Join(root, "fixtures/segeval/page.kraken.json") + ",letter\n"
	if e = os.WriteFile("pages.csv", []byte(csv), 0600); e != nil {
		t.Fatal(e)
	}
	c := newSegmentationCommand(true)
	c.SetArgs([]string{"-c", "pages.csv", "-n", "test", "--rows", "0"})
	if e = c.Execute(); e != nil {
		t.Fatal(e)
	}
	s, e := loadSegSummary("evals/segmentation/test.yaml")
	if e != nil || len(s.Results) != 1 || s.Results[0].Report.Profile != "letter" {
		t.Fatal(s, e)
	}
	rerun := newSegmentationCommand(true)
	rerun.SetArgs([]string{"--config", "evals/segmentation/test.yaml"})
	if e = rerun.Execute(); e != nil {
		t.Fatal(e)
	}
}
func TestConvertCommand(t *testing.T) {
	c := newConvertCommand()
	var b bytes.Buffer
	c.SetOut(&b)
	c.SetArgs([]string{"--in", "../fixtures/segeval/page.xml", "--to", "htrjson"})
	if e := c.Execute(); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(b.String(), `"htrPage": 1`) {
		t.Fatal(b.String())
	}
}

func TestSegmentationKeepsSuccessfulRowsAroundFailure(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	gt := filepath.Join(root, "fixtures/segeval/page.xml")
	hyp := filepath.Join(root, "fixtures/segeval/page.kraken.json")
	csv := "ground_truth,segmentation,profile\n" + gt + "," + hyp + ",letter\nmissing.xml," + hyp + ",letter\n" + gt + "," + hyp + ",printed\n"
	if err = os.WriteFile("pages.csv", []byte(csv), 0600); err != nil {
		t.Fatal(err)
	}
	c := newSegmentationCommand(true)
	var warnings bytes.Buffer
	c.SetErr(&warnings)
	c.SetArgs([]string{"-c", "pages.csv", "-o", "evals/segmentation/partial.yaml"})
	if err = c.Execute(); err != nil {
		t.Fatal(err)
	}
	s, err := loadSegSummary("evals/segmentation/partial.yaml")
	if err != nil || len(s.Results) != 3 {
		t.Fatalf("%+v %v", s, err)
	}
	if s.Results[0].Report.Score != 1 || s.Results[1].Error == "" || s.Results[2].Report.Score != 1 || s.Results[2].Row != 2 {
		t.Fatal(s.Results)
	}
	if !strings.Contains(warnings.String(), "row 1") {
		t.Fatal(warnings.String())
	}
	var exported bytes.Buffer
	if err = segmentationCSV(&exported); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exported.String(), ",,1,") {
		t.Fatal(exported.String())
	}
	old := backfillProfileFile
	t.Cleanup(func() { backfillProfileFile = old })
	profile := s.Results[0].ProfileDefinition
	profile.Calibrated = true
	b, err := yaml.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile("letter.yaml", b, 0600); err != nil {
		t.Fatal(err)
	}
	backfillProfileFile = "letter.yaml"
	if err = backfillSegmentation(c); err != nil {
		t.Fatal(err)
	}
	s, err = loadSegSummary("evals/segmentation/partial.yaml")
	if err != nil || !s.Results[0].Report.Calibrated || s.Results[2].Report.Profile != "printed" || s.Results[2].Report.Calibrated {
		t.Fatalf("%+v %v", s, err)
	}
	// Transcription backfill must never inspect broken segmentation results.
	if err = os.WriteFile("evals/segmentation/broken.yaml", []byte("broken: ["), 0600); err != nil {
		t.Fatal(err)
	}
	oldKind := backfillKind
	t.Cleanup(func() { backfillKind = oldKind })
	backfillKind = "transcription"
	warnings.Reset()
	if err = runBackfill(c, nil); err != nil {
		t.Fatal(err)
	}
	if warnings.Len() != 0 {
		t.Fatal(warnings.String())
	}
}
