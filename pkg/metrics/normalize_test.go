package metrics

import "testing"

func TestNormalizeMarkdown(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"# Heading\nText", "Heading\nText"}, {"Title\n====\ntext", "Title\ntext"},
		{"**bold** *italic* __strong__ _under_ file_name", "bold italic strong under file_name"},
		{"- item\n2. second\n> quote", "item\n2. second\nquote"},
		{"$5 … $10\n12. Paid $20", "$5 … $10\n12. Paid $20"},
		{"[été](https://example.org) ![image](x)", "été"},
		{"<table><tr><td>A</td><td>B &amp; 水</td></tr></table>", "A B & 水"},
		{"```text\n`code`\n```", "code"}, {"$x$ and $$y+z$$", "x and y+z"},
	} {
		if got := NormalizeMarkdown(tt.in); got != tt.want {
			t.Errorf("%q => %q; want %q", tt.in, got, tt.want)
		}
	}
	r := Evaluate("Title", "# Title", Options{Normalize: "markdown"})
	if r.CharacterAccuracy != 1 {
		t.Fatal(r)
	}
}
