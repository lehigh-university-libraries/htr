package kraken

import (
	"image"
	"testing"
)

func TestKrakenOCRCharacterCutsAndReadingOrder(t *testing.T) {
	data := []byte(`{"entities":[{"id":"right","type":"line","text":"文","boundary":[[100,20],[140,25],[140,45],[100,40]],"baseline":[[100,35],[140,40]],"cuts":[[[100,20],[140,25],[140,45],[100,40]]],"confidences":[0.8]},{"id":"left","type":"line","text":"a b","boundary":[[0,0],[60,10],[60,30],[0,20]],"baseline":[[0,15],[60,25]],"cuts":[[[0,0],[20,3],[20,23],[0,20]],[[20,3],[30,5],[30,25],[20,23]],[[30,5],[60,10],[60,30],[30,25]]],"confidences":[0.9,0.5,0.7]}],"line_orders":[["left","right"]]}`)
	lines, err := parseLegacyOCR(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Text != "a b" || lines[1].Text != "文" || len(lines[0].Words) != 2 || lines[0].Words[1].X != 30 || lines[0].Words[1].Width != 30 {
		t.Fatalf("lines=%#v", lines)
	}
	empty, err := parseLegacyOCR([]byte(`{"entities":[]}`))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty=%#v err=%v", empty, err)
	}
	if _, err := parseLegacyOCR([]byte(`{"entities":[{"type":"line","text":"a","boundary":[[0,0],[5,0],[5,5]],"cuts":[],"confidences":[]}]}`)); err == nil {
		t.Fatal("invalid cuts accepted")
	}
}

func TestKrakenOCRPreservesLineWithDegenerateWordCuts(t *testing.T) {
	raw := []byte(`{"entities":[{"id":"line","type":"line","text":"a ' b","boundary":[[0,0],[60,0],[60,30],[0,30]],"baseline":[[0,20],[60,20]],"cuts":[[[0,0],[10,0],[10,30],[0,30]],[],[[30,10],[30,10],[30,10],[30,10]],[],[[40,0],[60,0],[60,30],[40,30]]],"confidences":[0.9,0.9,0.9,0.9,0.9]}]}`)
	page, err := ParseOCR(raw, image.Pt(60, 30))
	if err != nil {
		t.Fatal(err)
	}
	lines := page.Lines()
	if len(lines) != 1 || lines[0].PlainText() != "a ' b" || len(lines[0].Words) != 0 || len(lines[0].Baseline) != 2 {
		t.Fatalf("line with unlocated token = %+v", lines)
	}
}
func parseLegacyOCR(raw []byte) ([]legacyLine, error) {
	p, e := ParseOCR(raw, image.Point{})
	var out []legacyLine
	for _, l := range p.Lines() {
		v := legacyLine{Text: l.Text}
		for _, w := range l.Words {
			v.Words = append(v.Words, legacyWord{X: w.Box.X1, Width: w.Box.X2 - w.Box.X1})
		}
		out = append(out, v)
	}
	return out, e
}

type legacyLine struct {
	Text  string
	Words []legacyWord
}
type legacyWord struct{ X, Width int }
