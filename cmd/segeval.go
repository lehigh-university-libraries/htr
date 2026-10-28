package cmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/azure"
	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	htrmetrics "github.com/lehigh-university-libraries/htr/pkg/metrics"
	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/pipeline"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segeval"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
	"github.com/lehigh-university-libraries/htr/pkg/segment/kraken"
	"github.com/lehigh-university-libraries/htr/pkg/segment/layoutkraken"
	"github.com/lehigh-university-libraries/htr/pkg/segment/ppdoclayout"
	"github.com/lehigh-university-libraries/htr/pkg/segment/remote"
	"github.com/lehigh-university-libraries/htr/pkg/segment/textract"
	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v3"
)

type segmentationConfig struct {
	Recognizer      string               `yaml:"recognizer,omitempty"`
	RecognizerModel string               `yaml:"recognizer_model,omitempty"`
	Kind            string               `yaml:"kind"`
	Provider        string               `yaml:"provider"`
	Model           string               `yaml:"model"`
	CSV             string               `yaml:"csv"`
	Directory       string               `yaml:"dir"`
	Rows            []int                `yaml:"rows,omitempty"`
	Profile         string               `yaml:"profile"`
	ProfileFile     string               `yaml:"profile_file,omitempty"`
	Level           ocrpage.Level        `yaml:"level"`
	Ignore          []ocrpage.RegionType `yaml:"ignore,omitempty"`
	Timestamp       string               `yaml:"timestamp"`
}
type segmentationResult struct {
	Error             string             `yaml:"error,omitempty"`
	Row               int                `yaml:"row"`
	Transcription     *htrmetrics.Result `yaml:"transcription,omitempty"`
	NativeResponse    string             `yaml:"native_response,omitempty"`
	NativeFormat      string             `yaml:"native_format,omitempty"`
	Identifier        string             `yaml:"identifier"`
	GroundTruth       string             `yaml:"ground_truth"`
	Segmentation      string             `yaml:"segmentation"`
	ProfileDefinition segeval.Profile    `yaml:"profile_definition"`
	Report            segeval.Report     `yaml:"report"`
}
type segmentationSummary struct {
	Config  segmentationConfig   `yaml:"config"`
	Results []segmentationResult `yaml:"results"`
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, ocrpage.ErrLimit
	}
	return b, nil
}
func readOCRPage(path string) (ocrpage.Page, error) {
	b, e := readBoundedFile(path, 64<<20)
	if e != nil {
		return ocrpage.Page{}, e
	}
	return ocrpage.Parse(b, ocrpage.ReadOptions{})
}
func writePage(p ocrpage.Page, to string) ([]byte, error) {
	switch ocrpage.Format(to) {
	case ocrpage.FormatHTRJSON:
		return ocrpage.WriteJSON(p)
	case ocrpage.FormatPageXML:
		return ocrpage.WritePageXML(p, ocrpage.PageXMLOptions{})
	case ocrpage.FormatHOCR:
		return ocrpage.WriteHOCR(p, ocrpage.HOCROptions{})
	case ocrpage.FormatALTO:
		return ocrpage.WriteALTO(p)
	case ocrpage.FormatText:
		return ocrpage.WriteText(p)
	case ocrpage.FormatIIIF:
		items, e := ocrpage.WriteIIIF(p, ocrpage.IIIFWriteOptions{Levels: []ocrpage.Level{ocrpage.LevelRegion, ocrpage.LevelLine, ocrpage.LevelWord}})
		if e != nil {
			return nil, e
		}
		return json.MarshalIndent(map[string]any{"type": "AnnotationPage", "width": p.Width, "height": p.Height, "items": items}, "", "  ")
	}
	return nil, fmt.Errorf("unsupported output format %q", to)
}
func init() {
	RootCmd.AddCommand(newConvertCommand(), newSegmentationCommand(true), newSegmentationCommand(false), newSegmentCommand())
}
func newConvertCommand() *cobra.Command {
	var in, to, out string
	var w, h int
	var dpi float64
	c := &cobra.Command{Use: "convert --in page.xml --to htrjson", Short: "Convert OCR page geometry and text between interchange formats", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if in == "" {
			return fmt.Errorf("--in is required")
		}
		b, e := readBoundedFile(in, 64<<20)
		if e != nil {
			return e
		}
		p, e := ocrpage.Parse(b, ocrpage.ReadOptions{CanvasWidth: w, CanvasHeight: h, DPI: dpi})
		if e != nil {
			return e
		}
		b, e = writePage(p, to)
		if e != nil {
			return e
		}
		if out != "" {
			return os.WriteFile(out, b, 0600)
		}
		_, e = c.OutOrStdout().Write(b)
		return e
	}}
	c.Flags().StringVar(&in, "in", "", "Input OCR file (format detected)")
	c.Flags().StringVar(&to, "to", "htrjson", "Output: htrjson, pagexml, alto, hocr, iiif, txt")
	c.Flags().StringVarP(&out, "output", "o", "", "Output file (default stdout)")
	c.Flags().IntVar(&w, "width", 0, "Canvas width if absent from input")
	c.Flags().IntVar(&h, "height", 0, "Canvas height if absent from input")
	c.Flags().Float64Var(&dpi, "dpi", 0, "DPI for ALTO physical units")
	return c
}
func segmentationProvider(name, model string) (segment.Segmenter, error) {
	switch name {
	case "kraken":
		return kraken.New(model), nil
	case "ppdoclayout":
		return ppdoclayout.New(ppdoclayout.Options{ModelDir: os.Getenv("PADDLE_LAYOUT_MODEL_DIR")}), nil
	case "layout+kraken", "newspapers":
		return &layoutkraken.Segmenter{Layout: ppdoclayout.New(ppdoclayout.Options{ModelDir: os.Getenv("PADDLE_LAYOUT_MODEL_DIR")}), Lines: kraken.New(model)}, nil
	case "azure-layout":
		return azure.NewSegmenter(azure.LayoutOptions{Endpoint: os.Getenv("AZURE_OCR_ENDPOINT"), APIKey: func(context.Context) (string, error) { return os.Getenv("AZURE_OCR_API_KEY"), nil }})
	case "textract":
		return textract.NewClient(context.Background(), textract.Options{})
	case "remote":
		opts := remote.Options{Endpoint: os.Getenv("HTR_REMOTE_SEGMENT_URL"), V1Fallback: true}
		if token := os.Getenv("HTR_REMOTE_TOKEN"); token != "" {
			opts.Authenticator = httpclient.StaticBearer(token)
		}
		client, err := remote.NewClient(opts)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		caps, err := client.FetchCapabilities(ctx, model)
		if err != nil {
			var pe *providers.Error
			if !errors.As(err, &pe) || (pe.StatusCode != 404 && pe.StatusCode != 405) {
				return nil, err
			}
		} else {
			opts.Capabilities = caps
		}
		return remote.NewClient(opts)
	}
	return nil, fmt.Errorf("unknown segmentation provider %q", name)
}
func newSegmentCommand() *cobra.Command {
	var provider, model, to string
	var timeout time.Duration
	c := &cobra.Command{Use: "segment image.jpg", Short: "Run a segmenter and export its geometry", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		s, e := segmentationProvider(provider, model)
		if e != nil {
			return e
		}
		ctx, cancel := contextWithTimeout(c, timeout)
		defer cancel()
		r, e := runSegmentFile(ctx, s, args[0], model, "")
		if e != nil {
			return e
		}
		b, e := writePage(r.Page, to)
		if e != nil {
			return e
		}
		_, e = c.OutOrStdout().Write(b)
		return e
	}}
	c.Flags().StringVar(&provider, "provider", "kraken", "Segmentation provider")
	c.Flags().StringVarP(&model, "model", "m", "", "Model name or local model path")
	c.Flags().StringVar(&to, "to", "htrjson", "Output format")
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Segmentation deadline")
	return c
}
func newSegmentationCommand(external bool) *cobra.Command {
	cfg := segmentationConfig{Kind: "segmentation", Profile: "generic", Directory: "./"}
	var gtPath, hypPath, configPath, output string
	var ignored []string
	var timeout time.Duration
	name := "segeval"
	short := "Evaluate a segmenter against ground-truth page layouts"
	if external {
		name = "segeval-external"
		short = "Evaluate exported OCR layouts against ground truth"
	}
	c := &cobra.Command{Use: name, Short: short, Long: short + `. Formats are detected on both sides.

CSV columns: ground_truth,segmentation,profile (external), or
image,ground_truth,profile (provider). The profile column is optional.
With --recognizer, an optional transcript column enables text scoring.
Rows are zero-based, matching htr eval.

Examples:
  htr segeval-external --gt truth.xml --segmentation predicted.json --profile letter
  htr segeval-external -c pages.csv -n kraken-blla --dir bundle
  htr segeval --provider kraken -m blla.mlmodel -c pages.csv
`, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if configPath != "" {
			b, e := readBoundedFile(configPath, 16<<20)
			if e != nil {
				return e
			}
			var prev segmentationSummary
			if e = yaml.Unmarshal(b, &prev); e != nil {
				return e
			}
			if prev.Config.Kind != "segmentation" {
				return fmt.Errorf("config is not a segmentation evaluation")
			}
			saved := prev.Config
			if c.Flags().Changed("rows") {
				saved.Rows = cfg.Rows
			}
			if c.Flags().Changed("profile") {
				saved.Profile = cfg.Profile
			}
			if c.Flags().Changed("profile-file") {
				saved.ProfileFile = cfg.ProfileFile
			}
			if c.Flags().Changed("level") {
				saved.Level = cfg.Level
			}
			if c.Flags().Changed("dir") {
				saved.Directory = cfg.Directory
			}
			cfg = saved
		}
		if c.Flags().Changed("ignore-type") || configPath == "" {
			cfg.Ignore = nil
		}
		for _, s := range ignored {
			typ := ocrpage.CanonicalType(s)
			if typ == ocrpage.RegionUnknown && s != "unknown" {
				return fmt.Errorf("unknown region type %q", s)
			}
			cfg.Ignore = append(cfg.Ignore, typ)
		}
		if cfg.Level != "" {
			if e := ocrpage.CheckLevel(cfg.Level); e != nil {
				return e
			}
		}
		profile, e := resolveSegProfile(cfg.Profile, cfg.ProfileFile)
		if e != nil {
			return e
		}
		var records []map[string]string
		if gtPath != "" || hypPath != "" {
			if !external || gtPath == "" || hypPath == "" || cfg.CSV != "" {
				return fmt.Errorf("use both --gt and --segmentation, or --csv")
			}
			records = []map[string]string{{"ground_truth": gtPath, "segmentation": hypPath}}
		} else {
			if cfg.CSV == "" {
				return fmt.Errorf("--csv or --gt and --segmentation required")
			}
			records, e = readSegCSV(cfg.CSV)
			if e != nil {
				return e
			}
		}
		if cfg.Model == "" {
			if external {
				cfg.Model = "external"
			} else {
				cfg.Model = defaultSegModel(cfg.Provider)
			}
		}
		cfg.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
		summary := segmentationSummary{Config: cfg}
		var seg segment.Segmenter
		if !external {
			seg, e = segmentationProvider(cfg.Provider, cfg.Model)
			if e != nil {
				return e
			}
			level := cfg.Level
			if level == "" {
				level = profile.Level
			}
			if e = segment.RequireLevel(seg, cfg.Model, level); e != nil {
				return e
			}
		}
		for i, row := range records {
			if len(cfg.Rows) > 0 && !slices.Contains(cfg.Rows, i) {
				continue
			}
			result, rowErr := func() (segmentationResult, error) {
				rp := profile
				if s := row["profile"]; s != "" && cfg.ProfileFile == "" {
					var ok bool
					rp, ok = segeval.Lookup(s)
					if !ok {
						return segmentationResult{}, fmt.Errorf("unknown profile %q", s)
					}
				}
				gtFile := joinInput(cfg.Directory, row["ground_truth"])
				gt, e := readOCRPage(gtFile)
				if e != nil {
					return segmentationResult{}, fmt.Errorf("ground truth: %w", e)
				}
				hypFile := joinInput(cfg.Directory, row["segmentation"])
				var hyp ocrpage.Page
				nativePath, nativeFormat := "", ""
				if external {
					hyp, e = readOCRPage(hypFile)
				} else {
					level := cfg.Level
					if level == "" {
						level = rp.Level
					}
					if e = segment.RequireLevel(seg, cfg.Model, level); e != nil {
						return segmentationResult{}, e
					}
					ctx, cancel := contextWithTimeout(c, timeout)
					var r segment.Result
					r, e = runSegmentFile(ctx, seg, joinInput(cfg.Directory, row["image"]), cfg.Model, rp.Name)
					if e == nil && cfg.Recognizer != "" {
						model := cfg.RecognizerModel
						if model == "" {
							model = getDefaultModel(cfg.Recognizer)
						}
						var rec *legacyClient
						rec, e = newLegacyClient(providers.Config{Provider: cfg.Recognizer, Model: model, Timeout: timeout})
						if e == nil {
							var img providers.Image
							img, e = readLayoutImage(joinInput(cfg.Directory, row["image"]))
							if e == nil {
								r.Page, e = pipeline.Run(ctx, cachedSegmentation{seg, r}, rec, img, pipeline.Options{Level: level, Concurrency: 4, Prompt: "Return only the transcribed text.", Model: model})
							}
						}
					}
					cancel()
					if e == nil {
						hyp = r.Page
						var b []byte
						b, e = ocrpage.WriteJSON(hyp)
						if e == nil {
							hypFile = filepath.Join("evals", "segmentation", safeSegName(cfg.Model), safeSegName(cfg.Timestamp), fmt.Sprintf("%d.json", i))
							if e = os.MkdirAll(filepath.Dir(hypFile), 0700); e == nil {
								e = os.WriteFile(hypFile, b, 0600)
								if e == nil && len(r.Raw) > 0 {
									nativePath = hypFile + ".native"
									nativeFormat = r.RawFormat
									e = os.WriteFile(nativePath, r.Raw, 0600)
								}
							}
						}
					}
				}
				if e != nil {
					return segmentationResult{}, fmt.Errorf("hypothesis: %w", e)
				}
				report, e := segeval.Evaluate(gt, hyp, segeval.Options{Profile: rp, Level: cfg.Level, Ignore: cfg.Ignore})
				if e != nil {
					return segmentationResult{}, e
				}
				result := segmentationResult{Identifier: filepath.Base(gtFile), GroundTruth: gtFile, Segmentation: hypFile, ProfileDefinition: rp, Report: report, NativeResponse: nativePath, NativeFormat: nativeFormat}
				if cfg.Recognizer != "" && row["transcript"] != "" {
					text, e := readBoundedFile(joinInput(cfg.Directory, row["transcript"]), 64<<20)
					if e != nil {
						result.Error = fmt.Sprintf("transcript: %v", e)
						return result, nil
					}
					normalize := "none"
					if cfg.Recognizer == "mistral-ocr" {
						normalize = "markdown"
					}
					metrics := htrmetrics.Evaluate(string(text), hyp.PlainText(), htrmetrics.Options{Normalize: normalize})
					result.Transcription = &metrics
				}
				return result, nil
			}()
			result.Row = i
			if rowErr != nil {
				result.Identifier = filepath.Base(row["ground_truth"])
				result.GroundTruth = joinInput(cfg.Directory, row["ground_truth"])
				result.Segmentation = joinInput(cfg.Directory, row["segmentation"])
				result.Error = rowErr.Error()
			}
			if result.Error != "" {
				fmt.Fprintf(c.ErrOrStderr(), "Warning: row %d: %s\n", i, result.Error)
			}
			summary.Results = append(summary.Results, result)
			if output == "" {
				output = filepath.Join("evals", "segmentation", safeSegName(cfg.Model)+".yaml")
			}
			// Checkpoint each completed row so a later interruption preserves paid work.
			if e = saveSegSummary(output, summary); e != nil {
				return e
			}
		}
		if len(summary.Results) == 0 {
			return fmt.Errorf("no rows selected")
		}
		if output == "" {
			output = filepath.Join("evals", "segmentation", safeSegName(cfg.Model)+".yaml")
		}
		if e = saveSegSummary(output, summary); e != nil {
			return e
		}
		printSegSummary(c.OutOrStdout(), summary)
		_, e = fmt.Fprintf(c.OutOrStdout(), "Saved %s\n", output)
		return e
	}}
	c.Flags().StringVarP(&cfg.CSV, "csv", "c", "", "CSV with layout pairs")
	c.Flags().StringVar(&cfg.Directory, "dir", "./", "Base directory for CSV paths")
	c.Flags().IntSliceVar(&cfg.Rows, "rows", nil, "Zero-based CSV row numbers")
	c.Flags().StringVar(&cfg.Profile, "profile", "generic", "Document type profile")
	c.Flags().StringVar(&cfg.ProfileFile, "profile-file", "", "Custom YAML/JSON profile")
	c.Flags().Var((*levelValue)(&cfg.Level), "level", "Geometry level: line, word, region (default profile level)")
	c.Flags().StringSliceVar(&ignored, "ignore-type", nil, "Canonical region types to omit")
	c.Flags().StringVar(&configPath, "config", "", "Rerun a saved segmentation evaluation")
	c.Flags().StringVarP(&output, "output", "o", "", "Result YAML path")
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Per-image segmentation deadline")
	if external {
		c.Flags().StringVarP(&cfg.Model, "name", "n", "", "External model name")
		c.Flags().StringVar(&gtPath, "gt", "", "Single ground-truth file")
		c.Flags().StringVar(&hypPath, "segmentation", "", "Single predicted layout file")
	} else {
		c.Flags().StringVar(&cfg.Recognizer, "recognizer", "", "Optional transcription provider for each crop")
		c.Flags().StringVar(&cfg.RecognizerModel, "recognizer-model", "", "Recognizer model")
		c.Flags().StringVar(&cfg.Provider, "provider", "kraken", "Segmentation provider")
		c.Flags().StringVarP(&cfg.Model, "model", "m", "", "Model name or local path")
	}
	return c
}

type levelValue ocrpage.Level

func (v *levelValue) String() string { return string(*v) }
func (v *levelValue) Set(s string) error {
	if err := ocrpage.CheckLevel(ocrpage.Level(s)); err != nil {
		return err
	}
	*v = levelValue(s)
	return nil
}
func (v *levelValue) Type() string { return "level" }
func safeSegName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s = b.String()
	if s == "" || s == "." || s == ".." {
		return "external"
	}
	return s
}
func joinInput(dir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}
func resolveSegProfile(name, path string) (segeval.Profile, error) {
	if path != "" {
		f, e := os.Open(path)
		if e != nil {
			return segeval.Profile{}, e
		}
		defer f.Close()
		return segeval.LoadProfile(f)
	}
	p, ok := segeval.Lookup(name)
	if !ok {
		return p, fmt.Errorf("unknown profile %q", name)
	}
	return p, nil
}
func readSegCSV(path string) ([]map[string]string, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	r := csv.NewReader(io.LimitReader(f, 64<<20))
	header, e := r.Read()
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	for i, s := range header {
		s = strings.ToLower(strings.TrimSpace(s))
		if seen[s] {
			return nil, fmt.Errorf("duplicate CSV column %q", s)
		}
		seen[s] = true
		header[i] = s
	}
	if !seen["ground_truth"] || (!seen["segmentation"] && !seen["image"]) {
		return nil, fmt.Errorf("CSV requires ground_truth and segmentation or image columns")
	}
	var out []map[string]string
	for {
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		m := map[string]string{}
		for i, s := range row {
			m[header[i]] = strings.TrimSpace(s)
		}
		if m["ground_truth"] == "" {
			return nil, fmt.Errorf("CSV ground_truth must not be empty")
		}
		out = append(out, m)
	}
	return out, nil
}
func saveSegSummary(path string, s segmentationSummary) error {
	b, e := yaml.Marshal(s)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".htr-segeval-*.yaml")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func printSegSummary(w io.Writer, s segmentationSummary) {
	groups := map[string][]segeval.Report{}
	for _, r := range s.Results {
		if r.Error != "" {
			continue
		}
		groups[r.Report.Profile] = append(groups[r.Report.Profile], r.Report)
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		score, prima, mote, f1 := 0.0, 0.0, 0.0, 0.0
		for _, r := range groups[name] {
			score += r.Score
			if r.PRImA != nil {
				prima += r.PRImA.Score
			}
			if r.MOTe != nil {
				mote += r.MOTe.Total
			}
			if r.Matching != nil {
				f1 += r.Matching.MeanF1
			}
		}
		n := float64(len(groups[name]))
		fmt.Fprintf(w, "%s: pages=%d score=%.6f prima=%.6f mote=%.6f mean_f1=%.6f\n", name, len(groups[name]), score/n, prima/n, mote/n, f1/n)
	}
}
func contextWithTimeout(c *cobra.Command, t time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Context(), t)
}
func runSegmentFile(ctx context.Context, s segment.Segmenter, path, model, profile string) (segment.Result, error) {
	req := segment.Request{Model: model, Hints: segment.Hints{DocumentType: profile}}
	if file, ok := s.(segment.FileSegmenter); ok {
		return file.SegmentFile(ctx, path, req)
	}
	b, e := readBoundedFile(path, 50<<20)
	if e != nil {
		return segment.Result{}, e
	}
	req.Image = providers.Image{Data: b, MediaType: http.DetectContentType(b)}
	return s.Segment(ctx, req)
}
func defaultSegModel(provider string) string {
	switch provider {
	case "kraken", "layout+kraken", "newspapers":
		if m := os.Getenv("KRAKEN_SEGMENTATION_MODEL"); m != "" {
			return m
		}
		return "blla.mlmodel"
	case "ppdoclayout":
		return "PP-DocLayoutV3"
	case "azure-layout":
		return "prebuilt-layout"
	case "textract":
		return "analyze-document"
	}
	return ""
}

type cachedSegmentation struct {
	segment.Segmenter
	result segment.Result
}

func (s cachedSegmentation) Segment(context.Context, segment.Request) (segment.Result, error) {
	return s.result, nil
}
