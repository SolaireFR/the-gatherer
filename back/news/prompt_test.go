package news

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SolaireFR/gosvelte/gosvelte-core/settings"
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

// Variables du réglage NEWS_PROMPT remplacées (valeur par défaut ou modifiée) ; format JSON toujours ajouté
func TestBuildNewsPrompt(t *testing.T) {
	day := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	p := buildNewsPrompt(day, nil, nil)
	if !strings.Contains(p, "l'actualité du 09/11/2026") || !strings.Contains(p, "suivantes : aucune (laisse cette rubrique vide)") ||
		!strings.Contains(p, "rubriques : aucun sujet.") || !strings.HasSuffix(p, responseFormat) {
		t.Fatalf("prompt par défaut sans configuration inattendu :\n%s", p)
	}
	if p := buildNewsPrompt(day, []string{"Go", "Docker"}, []string{"crypto"}); !strings.Contains(p, "suivantes : Go, Docker,") ||
		!strings.Contains(p, "rubriques : crypto.") {
		t.Fatalf("prompt par défaut avec configuration inattendu :\n%s", p)
	}

	t.Cleanup(settings.Override("NEWS_PROMPT", "News du {date} sur {technologies}"))
	if p := buildNewsPrompt(day, []string{"Go"}, nil); p != "News du 09/11/2026 sur Go"+responseFormat {
		t.Fatalf("prompt modifié inattendu :\n%s", p)
	}
	if validatePrompt("News d'hier") == nil {
		t.Fatal("prompt sans {date} accepté")
	}
}
