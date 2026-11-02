package duck

import (
	"strings"
	"testing"
)

func TestRenderConferences(t *testing.T) {
	passages := []conferencePassage{
		{path: "a.DOC", label: "a.DOC | section 10", id: 1, text: "last"},
		{path: "a.DOC", label: "a.DOC | section 2", id: 99, text: "Source: old\nsecond"},
		{path: "a.DOC", label: "a.DOC | section 1", id: 50, text: "first"},
		{path: "b.DOC", label: "b.DOC | section 1", id: 50, text: "first"},
	}
	got := renderConferences(passages, "1958-05-08")
	if strings.Index(got, "second") > strings.Index(got, "last") {
		t.Fatal("lexical rather than numeric order", got)
	}
	if strings.Count(got, "\nfirst\n") != 2 || strings.Contains(got, "Source: old") {
		t.Fatal("source aliases or legacy header mishandled", got)
	}
	if !strings.Contains(renderConferences(nil, "1958"), "No indexed conferences") {
		t.Fatal("missing empty result")
	}
}

func TestJoinIndexedPassages(t *testing.T) {
	overlap := strings.TrimSpace(strings.Repeat("palavra ", 30))
	got := joinIndexedPassages([]string{"Start. " + overlap, overlap + " Final."})
	if strings.Count(got, "palavra") != 30 || !strings.HasSuffix(got, "Final.") {
		t.Fatal(got)
	}
	if got := joinIndexedPassages([]string{"same short phrase", "same short phrase"}); strings.Count(got, "same short phrase") != 2 {
		t.Fatal("lost intentional repetition", got)
	}
	if got := dropWords("um\t dois\n três fim", 3); got != "fim" {
		t.Fatal(got)
	}
}
