package hocr

// OCRResponse and related types for word detection
type OCRResponse struct {
	Responses []Response `json:"responses"`
}

type Response struct {
	FullTextAnnotation *FullTextAnnotation `json:"fullTextAnnotation"`
}

type FullTextAnnotation struct {
	Pages []Page `json:"pages"`
	Text  string `json:"text"`
}

type Page struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Blocks []Block `json:"blocks"`
}

type Block struct {
	BoundingBox BoundingPoly `json:"boundingBox"`
	Paragraphs  []Paragraph  `json:"paragraphs"`
	BlockType   string       `json:"blockType"`
}

type Paragraph struct {
	BoundingBox BoundingPoly `json:"boundingBox"`
	Words       []Word       `json:"words"`
}

type Word struct {
	BoundingBox BoundingPoly `json:"boundingBox"`
	Symbols     []Symbol     `json:"symbols"`
}

type Symbol struct {
	BoundingBox BoundingPoly `json:"boundingBox"`
	Text        string       `json:"text"`
}

type BoundingPoly struct {
	Vertices []Vertex `json:"vertices"`
}

type Vertex struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// WordBox represents a detected word with its bounding box
type WordBox struct {
	X, Y, Width, Height int
	Text                string
}

// LineBox represents a line of text containing multiple words
type LineBox struct {
	Words               []WordBox
	X, Y, Width, Height int
}
