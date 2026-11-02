package chat

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/duck"
	"github.com/brito-rafa/jar/pkg/inference"
)

// Opt-in model check; ordinary unit tests never load or download models.
func TestLiveEnglishMultilingualContext(t *testing.T) {
	if os.Getenv("JAR_LIVE_CHAT_TEST") == "" {
		t.Skip("set JAR_LIVE_CHAT_TEST=1 for local model verification")
	}
	_, info, err := inference.Install(inference.DefaultEmbedding, inference.DefaultChat)
	if err != nil {
		t.Fatal(err)
	}
	krn, err := inference.New(info)
	if err != nil {
		t.Fatal(err)
	}
	defer krn.Unload(context.Background())
	docs := []duck.Document{{ID: 1, Text: "Source: english.txt | section 1\n" + englishPassage}, {ID: 2, Text: "Source: portugues.txt | section 1\n" + portuguesePassage}}
	question := []model.D{model.TextMessage("user", "How can keeping a journal help us make better decisions?")}
	answer, _, err := citedAnswer(docs, question, func(prompt []model.D) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		stream, err := performChat(ctx, krn, model.D{"messages": prompt, "max_tokens": 512, "temperature": 0.2})
		if err != nil {
			return "", err
		}
		answer, err := collectAnswer(stream)
		t.Logf("Model draft: %s", answer)
		return answer, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := detectLanguage(answer); got != "en" {
		t.Fatalf("answer language: %s", got)
	}
}
