package news

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// Réponse type de /v1/conversations avec web_search : le JSON de l'IA est découpé en chunks de texte, entrecoupés de
// références web
func TestParseConversationNews(t *testing.T) {
	body := []byte(`{"outputs": [
		{"type": "tool.execution", "name": "web_search"},
		{"type": "message.output", "content": [
			{"type": "text", "text": "Voici :\n{\"generales\": [{\"title\": \" A \", \"link\": \"https://ex.com/a\", \"source\": \"\", \"categories\": [\"Monde, Europe\", \"Monde  Europe\"]}],"},
			{"type": "tool_reference", "tool": "web_search", "title": "x", "url": "https://ex.com/a"},
			{"type": "text", "text": " \"tech\": [{\"title\": \"Sans lien\", \"link\": \"\"}, {\"title\": \"B\", \"link\": \"javascript:alert(1)\"}], \"alternatives\": [{\"title\": \"C\", \"link\": \"http://c.io\", \"source\": \"C News\"}]}"}
		]}
	]}`)

	text, err := conversationText(body)
	if err != nil {
		t.Fatal(err)
	}
	items, err := parseNews(text, "2026-11-09")
	if err != nil {
		t.Fatal(err)
	}
	want := []News{
		{Day: "2026-11-09", Section: "generales", Title: "A", Link: "https://ex.com/a", Source: "ex.com", Categories: "Monde Europe"},
		{Day: "2026-11-09", Section: "alternatives", Title: "C", Link: "http://c.io", Source: "C News", Categories: ""},
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("got %+v\nwant %+v", items, want)
	}

	// Sans outil, le contenu est une simple chaîne
	text, err = conversationText([]byte(`{"outputs": [{"type": "message.output", "content": "{\"tech\": []}"}]}`))
	if err != nil || text != `{"tech": []}` {
		t.Fatalf("contenu chaîne mal lu : %q, %v", text, err)
	}
}

// Sans technologie suivie, pas de rubrique « alternatives » ; sinon les technologies sont citées
func TestBuildNewsPrompt(t *testing.T) {
	day := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	if p := buildNewsPrompt(day, nil, nil); strings.Contains(p, `"alternatives" :`) || !strings.Contains(p, "09/11/2026") {
		t.Fatalf("prompt sans technologie inattendu :\n%s", p)
	}
	if p := buildNewsPrompt(day, []string{"Go", "Docker"}, []string{"crypto"}); !strings.Contains(p, "suivantes : Go, Docker.") || !strings.Contains(p, "EXCLURE de toutes les rubriques : crypto.") {
		t.Fatalf("prompt avec technologies inattendu :\n%s", p)
	}
}
