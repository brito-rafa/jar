package chat

import (
	"strings"
	"testing"
)

func TestRemoteChatRequiresBothEndpointsBeforeConnecting(t *testing.T) {
	t.Setenv("DB_DISABLE_SSL", "")
	t.Setenv("CHAT_URL", "")
	t.Setenv("EMBEDDING_URL", "")
	for _, args := range [][]string{nil, {"-chat-url=http://chat.test"}, {"-embedding-url=http://embedding.test"}} {
		if err := Run(args); err == nil || !strings.Contains(err.Error(), "chat-url and embedding-url are required") {
			t.Fatalf("Run(%v): expected missing endpoint error, got %v", args, err)
		}
	}
}

func TestRemoteChatRejectsOldProviderSelectionFlags(t *testing.T) {
	t.Setenv("DB_DISABLE_SSL", "")
	for _, name := range []string{"remote-chat", "remote-embedding", "remote-chat-model", "remote-embedding-model"} {
		if err := Run([]string{"-" + name + "=example.test"}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("%s: expected unsupported flag, got %v", name, err)
		}
	}
}
