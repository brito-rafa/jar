package duck

import "testing"

func TestChapterWindow(t *testing.T) {
	markers := []conferencePassage{
		{label: "book.pdf | page 1", id: 1, text: "CHAPTER I Beginning"},
		{label: "book.pdf | page 2", id: 4, text: "CHAPTER I Running header"},
		{label: "book.pdf | page 5", id: 10, text: "CAPÍTULO II Middle"},
		{label: "book.pdf | page 9", id: 20, text: "CHAPTER III End"},
	}
	start, end := chapterWindow(markers, conferencePassage{label: "book.pdf | page 7", id: 14})
	if start == nil || end == nil || start.id != 10 || end.id != 20 {
		t.Fatal(start, end)
	}
	start, end = chapterWindow(markers, conferencePassage{label: "book.pdf | page 10", id: 22})
	if start == nil || end != nil || start.id != 20 {
		t.Fatal(start, end)
	}
	if start, _ := chapterWindow(markers[:1], conferencePassage{label: "book.pdf | page 2", id: 4}); start != nil {
		t.Fatal("lone marker expanded to whole book")
	}
}
