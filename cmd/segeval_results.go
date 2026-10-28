package cmd

import (
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/lehigh-university-libraries/htr/pkg/segeval"
	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v3"
)

var csvKind string
var backfillProfileFile string
var backfillKind string

func init() {
	csvCmd.Flags().StringVar(&csvKind, "kind", "transcription", "Result kind: transcription or segmentation")
	backfillCmd.Flags().StringVar(&backfillProfileFile, "profile-file", "", "Recompute segmentation with a custom profile")
	backfillCmd.Flags().StringVar(&backfillKind, "kind", "transcription", "Result kind: transcription or segmentation")
}
func loadSegSummary(path string) (segmentationSummary, error) {
	var s segmentationSummary
	b, e := readBoundedFile(path, 64<<20)
	if e != nil {
		return s, e
	}
	if e = yaml.Unmarshal(b, &s); e != nil {
		return s, e
	}
	if s.Config.Kind != "segmentation" {
		return s, fmt.Errorf("not a segmentation evaluation")
	}
	return s, nil
}
func segmentationFiles() ([]string, error) {
	return filepath.Glob(filepath.Join("evals", "segmentation", "*.yaml"))
}
func segmentationCSV(w io.Writer) error {
	files, e := segmentationFiles()
	if e != nil {
		return e
	}
	type row struct {
		name, profile          string
		n                      int
		score, prima, mote, f1 float64
	}
	var rows []row
	for _, file := range files {
		s, e := loadSegSummary(file)
		if e != nil {
			return e
		}
		groups := map[string]*row{}
		for _, r := range s.Results {
			if r.Error != "" {
				continue
			}
			g := groups[r.Report.Profile]
			if g == nil {
				g = &row{name: s.Config.Model, profile: r.Report.Profile}
				groups[r.Report.Profile] = g
			}
			g.n++
			g.score += r.Report.Score
			if r.Report.PRImA != nil {
				g.prima += r.Report.PRImA.Score
			}
			if r.Report.MOTe != nil {
				g.mote += r.Report.MOTe.Total
			}
			if r.Report.Matching != nil {
				g.f1 += r.Report.Matching.MeanF1
			}
		}
		for _, r := range groups {
			n := float64(r.n)
			r.score /= n
			r.prima /= n
			r.mote /= n
			r.f1 /= n
			rows = append(rows, *r)
		}
	}
	slices.SortFunc(rows, func(a, b row) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		if a.name < b.name {
			return -1
		}
		if a.name > b.name {
			return 1
		}
		if a.profile < b.profile {
			return -1
		}
		if a.profile > b.profile {
			return 1
		}
		return 0
	})
	out := csv.NewWriter(w)
	if e = out.Write([]string{"Model", "Profile", "Pages", "Score", "PRImA", "MOTe", "MeanF1"}); e != nil {
		return e
	}
	for _, r := range rows {
		f := func(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }
		if e = out.Write([]string{r.name, r.profile, strconv.Itoa(r.n), f(r.score), f(r.prima), f(r.mote), f(r.f1)}); e != nil {
			return e
		}
	}
	out.Flush()
	return out.Error()
}
func backfillSegmentation(c *cobra.Command) error {
	files, e := segmentationFiles()
	if e != nil {
		return e
	}
	for _, file := range files {
		s, e := loadSegSummary(file)
		if e != nil {
			fmt.Fprintf(c.ErrOrStderr(), "Warning: %s: %v\n", file, e)
			continue
		}
		for i := range s.Results {
			r := &s.Results[i]
			p := r.ProfileDefinition
			if backfillProfileFile != "" {
				p, e = resolveSegProfile("", backfillProfileFile)
				if e != nil {
					return e
				}
				if p.Name != r.ProfileDefinition.Name {
					continue
				}
			}
			gt, e := readOCRPage(r.GroundTruth)
			if e != nil {
				fmt.Fprintf(c.ErrOrStderr(), "Warning: %s row %d: %v\n", file, i, e)
				continue
			}
			hyp, e := readOCRPage(r.Segmentation)
			if e != nil {
				fmt.Fprintf(c.ErrOrStderr(), "Warning: %s row %d: %v\n", file, i, e)
				continue
			}
			report, e := segeval.Evaluate(gt, hyp, segeval.Options{Profile: p, Level: s.Config.Level, Ignore: s.Config.Ignore})
			if e != nil {
				fmt.Fprintf(c.ErrOrStderr(), "Warning: %s row %d: %v\n", file, i, e)
				continue
			}
			r.Report, r.ProfileDefinition, r.Error = report, p, ""
		}
		if e = saveSegSummary(file, s); e != nil {
			return e
		}
		fmt.Fprintf(c.OutOrStdout(), "Updated %s\n", file)
	}
	return nil
}
