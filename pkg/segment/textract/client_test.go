package textract

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/textract"
	"github.com/aws/aws-sdk-go-v2/service/textract/types"
	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func geom(x, y, w, h float32) *types.Geometry {
	return &types.Geometry{BoundingBox: &types.BoundingBox{Left: x, Top: y, Width: w, Height: h}}
}
func sampleOutput() sdk.AnalyzeDocumentOutput {
	return sdk.AnalyzeDocumentOutput{AnalyzeDocumentModelVersion: aws.String("v1"), Blocks: []types.Block{
		{Id: aws.String("region"), BlockType: types.BlockTypeLayoutText, Geometry: geom(0, 0, 1, .5), Relationships: []types.Relationship{{Type: types.RelationshipTypeChild, Ids: []string{"line"}}}},
		{Id: aws.String("line"), BlockType: types.BlockTypeLine, Text: aws.String("été"), Geometry: geom(0, 0, .5, .2), Relationships: []types.Relationship{{Type: types.RelationshipTypeChild, Ids: []string{"word"}}}},
		{Id: aws.String("word"), BlockType: types.BlockTypeWord, Text: aws.String("été"), Geometry: geom(0, 0, .5, .2), Confidence: aws.Float32(95)},
		{Id: aws.String("table"), BlockType: types.BlockTypeTable, Geometry: geom(0, 0, .5, .2), Relationships: []types.Relationship{{Type: types.RelationshipTypeChild, Ids: []string{"cell"}}}},
		{Id: aws.String("cell"), BlockType: types.BlockTypeCell, Geometry: geom(0, 0, .5, .2), RowIndex: aws.Int32(1), ColumnIndex: aws.Int32(1), Relationships: []types.Relationship{{Type: types.RelationshipTypeChild, Ids: []string{"word"}}}},
	}}
}
func TestParseAndFeatures(t *testing.T) {
	out := sampleOutput()
	raw, e := json.Marshal(out)
	if e != nil {
		t.Fatal(e)
	}
	p, e := ParseTextract(raw, image.Pt(200, 100))
	if e != nil {
		t.Fatal(e)
	}
	if p.PlainText() != "été" || len(p.Lines()) != 1 || p.Words()[0].Confidence != .95 || p.Regions[1].Table == nil || len(p.Regions[1].Table.Cells[0].LineIDs) != 1 {
		t.Fatal(p)
	}
	if !reflect.DeepEqual(Features("form"), []types.FeatureType{types.FeatureTypeLayout, types.FeatureTypeTables, types.FeatureTypeForms, types.FeatureTypeSignatures}) {
		t.Fatal(Features("form"))
	}
	out.Blocks[0].Relationships[0].Ids = []string{"region"}
	raw, e = json.Marshal(out)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ParseTextract(raw, image.Pt(200, 100)); e == nil {
		t.Fatal("accepted cycle")
	}
}
func TestSDKPayload(t *testing.T) {
	out := sampleOutput()
	response := map[string]any{"AnalyzeDocumentModelVersion": "v1", "Blocks": out.Blocks}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Target") != "Textract.AnalyzeDocument" || r.Header.Get("Authorization") == "" {
			t.Error("unsigned or incorrect operation")
		}
		var payload struct {
			Document     struct{ Bytes []byte }
			FeatureTypes []types.FeatureType
		}
		if e := json.NewDecoder(r.Body).Decode(&payload); e != nil {
			t.Error(e)
		}
		if len(payload.Document.Bytes) == 0 || !reflect.DeepEqual(payload.FeatureTypes, Features("form")) {
			t.Error(payload)
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	api := sdk.NewFromConfig(aws.Config{Region: "us-east-1", HTTPClient: server.Client(), Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
	})}, func(o *sdk.Options) { o.BaseEndpoint = aws.String(server.URL) })
	c, e := NewClient(context.Background(), Options{API: api})
	if e != nil {
		t.Fatal(e)
	}
	var img bytes.Buffer
	if e = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 200, 100))); e != nil {
		t.Fatal(e)
	}
	r, e := c.Segment(context.Background(), segment.Request{Image: providers.Image{Data: img.Bytes(), MediaType: "image/png"}, Hints: segment.Hints{DocumentType: "form"}})
	if e != nil || r.Page.PlainText() != "été" {
		t.Fatal(r, e)
	}
}

func TestTextractClampsPageEdgeCoordinates(t *testing.T) {
	out := sampleOutput()
	out.Blocks[1].Geometry = geom(-.001, -.002, 1.002, 1.003)
	out.Blocks[1].Geometry.Polygon = []types.Point{{X: -.001, Y: -.002}, {X: 1.001, Y: 0}, {X: 1.001, Y: 1.001}, {X: 0, Y: 1.001}}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseTextract(raw, image.Pt(200, 100))
	if err != nil {
		t.Fatal(err)
	}
	if p.Lines()[0].Box != (ocrpage.Rect{X1: 0, Y1: 0, X2: 200, Y2: 100}) {
		t.Fatal(p.Lines()[0])
	}
}
