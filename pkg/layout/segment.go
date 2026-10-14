package layout

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/gif" // Register supported page image decoders.
	_ "image/jpeg"
	_ "image/png"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/remoteocr"
)

// Segment creates a blank PAGE 2019 template using Scribe's /v1/segment
// service. Each returned box is a line, in service reading order. The service
// exposes bounding rectangles, not Kraken's original baselines or polygons.
// JPEG, PNG and GIF dimensions are read from the same bytes sent to the service.
func Segment(ctx context.Context, client *remoteocr.Client, page providers.Image, model string) ([]byte, error) {
	if client == nil {
		return nil, fmt.Errorf("segmentation client is required")
	}
	size, _, err := image.DecodeConfig(bytes.NewReader(page.Data))
	if err != nil || size.Width <= 0 || size.Height <= 0 {
		return nil, fmt.Errorf("segmentation requires a valid JPEG, PNG or GIF image")
	}
	result, err := client.Segment(ctx, page, model)
	if err != nil {
		return nil, fmt.Errorf("segment page: %w", err)
	}
	if len(result.Words) == 0 {
		return nil, fmt.Errorf("segmentation returned no lines")
	}
	lines := make([]pageLine, len(result.Words))
	for i, box := range result.Words {
		// Subtraction avoids overflow from untrusted box coordinates.
		if box.X >= size.Width || box.Y >= size.Height || box.Width > size.Width-box.X || box.Height > size.Height-box.Y {
			return nil, fmt.Errorf("segmentation line %d lies outside the page", i+1)
		}
		lines[i] = pageLine{ID: fmt.Sprintf("line_%d", i+1), Coords: pageCoords{Points: rectangle(box.X, box.Y, box.Width, box.Height)}}
	}
	document := segmentedPage{}
	document.XMLNS = "http://schema.primaresearch.org/PAGE/gts/pagecontent/2019-07-15"
	document.Metadata.Creator = "HTR / " + result.Provider
	document.Metadata.Created = time.Now().UTC().Format(time.RFC3339)
	document.Metadata.LastChange = document.Metadata.Created
	document.Page.Filename = page.Filename
	document.Page.Width, document.Page.Height = size.Width, size.Height
	document.Page.Region.ID = "region_1"
	document.Page.Region.Coords.Points = rectangle(0, 0, size.Width, size.Height)
	document.Page.Region.Lines = lines
	encoded, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode segmented page: %w", err)
	}
	return append([]byte(xml.Header), encoded...), nil
}

func rectangle(x, y, width, height int) string {
	return fmt.Sprintf("%d,%d %d,%d %d,%d %d,%d", x, y, x+width, y, x+width, y+height, x, y+height)
}

type pageCoords struct {
	Points string `xml:"points,attr"`
}

type pageLine struct {
	ID     string     `xml:"id,attr"`
	Coords pageCoords `xml:"Coords"`
}

type segmentedPage struct {
	XMLName  xml.Name `xml:"PcGts"`
	XMLNS    string   `xml:"xmlns,attr"`
	Metadata struct {
		Creator    string
		Created    string
		LastChange string
	} `xml:"Metadata"`
	Page struct {
		Filename string `xml:"imageFilename,attr"`
		Width    int    `xml:"imageWidth,attr"`
		Height   int    `xml:"imageHeight,attr"`
		Region   struct {
			ID     string     `xml:"id,attr"`
			Coords pageCoords `xml:"Coords"`
			Lines  []pageLine `xml:"TextLine"`
		} `xml:"TextRegion"`
	} `xml:"Page"`
}
