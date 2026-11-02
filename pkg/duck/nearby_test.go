package duck

import "testing"

func TestNeighborParagraph(t *testing.T) {
	text := "Source: book.pdf | page 1\nFirst paragraph.\n\nSecond paragraph.\n\nLast paragraph."
	if got := neighborParagraph(text, true); got != "Last paragraph." {
		t.Fatal(got)
	}
	if got := neighborParagraph(text, false); got != "First paragraph." {
		t.Fatal(got)
	}
	if got := neighborParagraph("Flattened chunk without paragraph breaks.", true); got != "Flattened chunk without paragraph breaks." {
		t.Fatal(got)
	}
}
