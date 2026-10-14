package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/auth/gcpidtoken"
	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/layout"
	htrmetrics "github.com/lehigh-university-libraries/htr/pkg/metrics"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/remoteocr"
	"github.com/spf13/cobra"
)

var layoutCmd = &cobra.Command{
	Use:   "layout",
	Short: "Fill a blank PageXML or hOCR layout template with transcribed text",
	Long: `Send a page image and a blank PageXML or hOCR layout (for example, from a
segmentation model) to a vision model, and ask it to return the layout with
each line or word box transcribed.

Transcriptions are matched to the template by element ID and written into the
original template, so coordinates are never taken from the model. A report of
filled, missing, and geometry-altered boxes is printed to stderr.

Examples:
  # Transcribe into a Kraken/Loghi PageXML segmentation
  htr layout --provider gemini --model gemini-4-argon \
    --image page.jpg --template page.xml -o filled.xml

  # Align an existing transcript into the boxes and score it
  htr layout --provider gemini --model gemini-4-argon --gemini-thinking-level low \
    --image page.jpg --template page.hocr --transcript page.txt \
    --ground-truth page.gt.txt -o filled.hocr`,
	RunE: runLayout,
}

var (
	layoutImagePath          string
	layoutTemplatePath       string
	layoutSegmentorURL       string
	layoutSegmentorModel     string
	layoutSegmentorTokenFile string
	layoutTemplateOutput     string
	layoutTranscriptPath     string
	layoutGroundTruthPath    string
	layoutProvider           string
	layoutModel              string
	layoutInstructions       string
	layoutFormat             string
	layoutTemperature        float64
	layoutTimeout            time.Duration
	layoutOutputPath         string
	layoutResponsePath       string
	layoutDebug              bool
	layoutMaxResolution      string
	layoutReasoningEffort    string
	layoutThinkingLevel      string
	layoutMaxOutputTokens    int
)

func init() {
	RootCmd.AddCommand(layoutCmd)

	layoutCmd.Flags().StringVar(&layoutImagePath, "image", "", "Path to the page image (required)")
	layoutCmd.Flags().StringVar(&layoutTemplatePath, "template", "", "Path to the blank PageXML or hOCR template (or use --segmentor-url)")
	layoutCmd.Flags().StringVar(&layoutSegmentorURL, "segmentor-url", "", "Scribe segmentation service base URL; generates a PageXML template from the image")
	layoutCmd.Flags().StringVar(&layoutSegmentorModel, "segmentor-model", "kraken", "Server-registered segmentation model (e.g. kraken or newspapers)")
	layoutCmd.Flags().StringVar(&layoutSegmentorTokenFile, "segmentor-token-file", "", "File containing a Google identity token; otherwise use Google metadata identity")
	layoutCmd.Flags().StringVar(&layoutTemplateOutput, "template-output", "", "Save the generated blank PageXML template for reuse")
	layoutCmd.Flags().StringVar(&layoutTranscriptPath, "transcript", "", "Optional transcript to distribute across the boxes instead of transcribing from scratch")
	layoutCmd.Flags().StringVar(&layoutGroundTruthPath, "ground-truth", "", "Optional ground truth transcript; prints accuracy metrics for the filled text")
	layoutCmd.Flags().StringVar(&layoutProvider, "provider", "gemini", "Provider to use: openai, azure, claude, gemini, ollama")
	layoutCmd.Flags().StringVarP(&layoutModel, "model", "m", "", "Model to use (uses provider default when available)")
	layoutCmd.Flags().StringVarP(&layoutInstructions, "prompt", "p", "", "Replace the default task instructions (the transcript and template are always appended)")
	layoutCmd.Flags().StringVar(&layoutFormat, "format", "", "Template format: pagexml or hocr (detected when empty)")
	layoutCmd.Flags().Float64VarP(&layoutTemperature, "temperature", "t", 0.0, "Temperature for API (ignored by Gemini 4)")
	layoutCmd.Flags().DurationVar(&layoutTimeout, "timeout", 15*time.Minute, "Timeout for the API request")
	layoutCmd.Flags().StringVarP(&layoutOutputPath, "output", "o", "", "Write the filled template to a file instead of stdout")
	layoutCmd.Flags().StringVar(&layoutResponsePath, "response-output", "", "Also write the raw model response to this file")
	layoutCmd.Flags().BoolVar(&layoutDebug, "debug", false, "Print provider debug output when supported")
	layoutCmd.Flags().StringVar(&layoutMaxResolution, "gemini-max-resolution", "MEDIA_RESOLUTION_UNSPECIFIED", "Max resolution for Gemini models (e.g., MEDIA_RESOLUTION_HIGH)")
	addProviderSpendFlags(layoutCmd, &layoutThinkingLevel, &layoutMaxOutputTokens)
	addReasoningFlag(layoutCmd, &layoutReasoningEffort)

	for _, flag := range []string{"image"} {
		if err := layoutCmd.MarkFlagRequired(flag); err != nil {
			panic(err)
		}
	}
	layoutCmd.MarkFlagsMutuallyExclusive("template", "segmentor-url")
	layoutCmd.MarkFlagsOneRequired("template", "segmentor-url")
}

func runLayout(cmd *cobra.Command, args []string) error {
	if (layoutTemplatePath == "") == (layoutSegmentorURL == "") {
		return fmt.Errorf("provide exactly one of --template or --segmentor-url")
	}
	if layoutSegmentorURL == "" && (layoutSegmentorTokenFile != "" || layoutTemplateOutput != "") {
		return fmt.Errorf("--segmentor-token-file and --template-output require --segmentor-url")
	}
	if layoutSegmentorURL != "" && layoutFormat != "" && strings.ToLower(layoutFormat) != "pagexml" {
		return fmt.Errorf("--segmentor-url produces PageXML; --format must be pagexml or empty")
	}
	if !slices.Contains(allowedMediaResolutions, layoutMaxResolution) {
		return fmt.Errorf("invalid --gemini-max-resolution value '%s'. Allowed values are: %s", layoutMaxResolution, strings.Join(allowedMediaResolutions, ", "))
	}
	if err := validateGeminiSpendFlags(layoutThinkingLevel, layoutMaxOutputTokens); err != nil {
		return err
	}
	format := layout.Format(strings.ToLower(layoutFormat))
	if format != "" && format != layout.FormatPageXML && format != layout.FormatHOCR {
		return fmt.Errorf("invalid --format value %q: use pagexml or hocr", layoutFormat)
	}

	image, err := readLayoutImage(layoutImagePath)
	if err != nil {
		return err
	}
	transcript := ""
	if layoutTranscriptPath != "" {
		if transcript, err = readTextFile(layoutTranscriptPath); err != nil {
			return fmt.Errorf("failed to read transcript: %w", err)
		}
	}

	model := layoutModel
	if model == "" {
		model = getDefaultModel(layoutProvider)
	}
	client, err := newLegacyClient(providers.Config{
		Provider:        layoutProvider,
		Model:           model,
		Timeout:         layoutTimeout,
		Debug:           layoutDebug,
		MaxResolution:   layoutMaxResolution,
		ReasoningEffort: layoutReasoningEffort,
		ThinkingLevel:   layoutThinkingLevel,
		MaxOutputTokens: layoutMaxOutputTokens,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), layoutTimeout)
	defer cancel()
	template, err := readLayoutTemplate(ctx, image)
	if err != nil {
		return err
	}
	started := time.Now()
	result, fillErr := layout.Fill(ctx, client, layout.Request{
		Model:        model,
		Image:        image,
		Template:     template,
		Format:       format,
		Transcript:   transcript,
		Instructions: layoutInstructions,
		Temperature:  layoutTemperature,
	})
	elapsed := time.Since(started)

	if layoutResponsePath != "" && result.Response != "" {
		if err := os.WriteFile(layoutResponsePath, []byte(result.Response), 0644); err != nil {
			return fmt.Errorf("failed to write raw response: %w", err)
		}
	}
	printLayoutReport(os.Stderr, layoutProvider, model, elapsed, result)
	if fillErr != nil {
		return fmt.Errorf("layout fill failed: %w", fillErr)
	}

	if layoutGroundTruthPath != "" {
		groundTruth, err := readTextFile(layoutGroundTruthPath)
		if err != nil {
			return fmt.Errorf("failed to read ground truth: %w", err)
		}
		metrics := htrmetrics.Evaluate(groundTruth, result.Text, htrmetrics.Options{})
		fmt.Fprintf(os.Stderr, "Character similarity: %.4f\nWord similarity:      %.4f\nWord error rate:      %.4f\n",
			metrics.CharacterSimilarity, metrics.WordSimilarity, metrics.WordErrorRate)
	}

	if layoutOutputPath != "" {
		return os.WriteFile(layoutOutputPath, result.Document, 0644)
	}
	_, err = os.Stdout.Write(result.Document)
	return err
}

// printLayoutReport summarizes how faithfully the model followed the template.
func printLayoutReport(w io.Writer, providerName, model string, elapsed time.Duration, result layout.Result) {
	effectiveModel := result.EffectiveModel
	if effectiveModel == "" {
		effectiveModel = model
	}
	fmt.Fprintf(w, "Provider: %s  Model: %s  Format: %s  Time: %s\n", providerName, effectiveModel, result.Format, elapsed.Round(time.Millisecond))
	fmt.Fprintf(w, "Boxes filled: %d/%d  Missing: %d  Geometry changed by model: %d\n",
		result.Filled, result.Targets, len(result.Missing), len(result.GeometryChanged))
	fmt.Fprintf(w, "Tokens: input %d  output %d  thinking %d\n",
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.ThoughtTokens)
	if len(result.Missing) > 0 {
		fmt.Fprintf(w, "Missing IDs: %s\n", strings.Join(limitList(result.Missing, 20), ", "))
	}
	if len(result.GeometryChanged) > 0 {
		fmt.Fprintf(w, "Geometry changed IDs (template kept): %s\n", strings.Join(limitList(result.GeometryChanged, 20), ", "))
	}
}

func limitList(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return append(slices.Clone(values[:limit]), fmt.Sprintf("... (%d more)", len(values)-limit))
}

func readLayoutImage(path string) (providers.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return providers.Image{}, fmt.Errorf("failed to read image: %w", err)
	}
	mediaType := mime.TypeByExtension(filepath.Ext(path))
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	return providers.Image{Data: data, MediaType: mediaType, Filename: filepath.Base(path)}, nil
}

// legacyClient adapts a registered CLI provider to providers.Client so
// library packages such as layout can drive any CLI provider.
type legacyClient struct {
	provider providers.Provider
	config   providers.Config
}

func newLegacyClient(config providers.Config) (*legacyClient, error) {
	provider, err := providerRegistry.Get(config.Provider)
	if err != nil {
		return nil, fmt.Errorf("unsupported provider: %s", config.Provider)
	}
	if err := provider.ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid configuration for provider %s: %w", config.Provider, err)
	}
	return &legacyClient{provider: provider, config: config}, nil
}

// Name returns the wrapped provider name.
func (c *legacyClient) Name() string { return c.provider.Name() }

// Extract calls the wrapped provider with the request's model, prompt, and image.
func (c *legacyClient) Extract(ctx context.Context, request providers.Request) (providers.Result, error) {
	config := c.config
	config.Model = request.Model
	config.Prompt = request.Prompt
	config.Temperature = request.Temperature
	text, usage, err := c.provider.ExtractText(ctx, config, request.Image.Filename, base64.StdEncoding.EncodeToString(request.Image.Data))
	return providers.Result{Text: text, Usage: usage, EffectiveModel: request.Model}, err
}

// addProviderSpendFlags registers output budgets and Gemini thinking controls.
func addProviderSpendFlags(command *cobra.Command, thinkingLevel *string, maxOutputTokens *int) {
	command.Flags().StringVar(thinkingLevel, "gemini-thinking-level", "", "Gemini thinking level: minimal, low, medium, high (Gemini 4 rejects minimal; empty uses the model default)")
	command.Flags().IntVar(maxOutputTokens, "max-output-tokens", 0, "Output budget for OpenAI, Claude or Gemini (0 uses the provider default)")
	command.Flags().IntVar(maxOutputTokens, "gemini-max-output-tokens", 0, "Gemini output budget covering thinking and response tokens (0 uses the model default)")
}

func validateGeminiSpendFlags(thinkingLevel string, maxOutputTokens int) error {
	if !slices.Contains([]string{"", "minimal", "low", "medium", "high"}, strings.ToLower(thinkingLevel)) {
		return fmt.Errorf("invalid --gemini-thinking-level value %q: use minimal, low, medium, or high", thinkingLevel)
	}
	if maxOutputTokens < 0 {
		return fmt.Errorf("invalid output token budget %d: must not be negative", maxOutputTokens)
	}
	return nil
}

// readLayoutTemplate reuses the remote OCR protocol and its redirect-safe client.
func readLayoutTemplate(ctx context.Context, page providers.Image) ([]byte, error) {
	if layoutSegmentorURL == "" {
		data, err := os.ReadFile(layoutTemplatePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read template: %w", err)
		}
		return data, nil
	}
	endpoint, err := httpclient.ParseEndpoint(layoutSegmentorURL)
	if err != nil || endpoint.Scheme != "https" {
		return nil, fmt.Errorf("--segmentor-url must be an HTTPS service URL")
	}
	audience := endpoint.Scheme + "://" + endpoint.Host
	var authenticator httpclient.Authenticator
	if layoutSegmentorTokenFile != "" {
		file, err := os.Open(layoutSegmentorTokenFile)
		if err != nil {
			return nil, fmt.Errorf("cannot open segmentor token file")
		}
		defer func() { _ = file.Close() }()
		token, err := httpclient.ReadAll(file, 16<<10)
		if err != nil || len(strings.TrimSpace(string(token))) == 0 {
			return nil, fmt.Errorf("invalid segmentor token file")
		}
		authenticator = httpclient.StaticBearer(strings.TrimSpace(string(token)))
	} else {
		source, err := gcpidtoken.New(gcpidtoken.Options{})
		if err != nil {
			return nil, err
		}
		authenticator = httpclient.BearerAuthenticator{Source: source, Audience: audience}
	}
	client, err := remoteocr.NewClient(remoteocr.Options{
		Endpoint: endpoint.String(), Authenticator: authenticator, Timeout: 240 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	template, err := layout.Segment(ctx, client, page, layoutSegmentorModel)
	if err != nil {
		return nil, err
	}
	if layoutTemplateOutput != "" {
		if err := os.WriteFile(layoutTemplateOutput, template, 0644); err != nil {
			return nil, fmt.Errorf("write generated template: %w", err)
		}
	}
	return template, nil
}

func addReasoningFlag(command *cobra.Command, effort *string) {
	command.Flags().StringVar(effort, "reasoning-effort", "", "OpenAI/Claude reasoning effort: low, medium, high, xhigh, max (OpenAI also supports none on some models)")
}
