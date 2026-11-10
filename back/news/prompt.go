package news

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/SolaireFR/gosvelte/gosvelte-core/settings"
)

// maxCategoryLen, maxCategoriesPerNews : bornes des catégories inventées par l'IA
const (
	maxCategoryLen       = 30
	maxCategoriesPerNews = 3
)

// sectionKeys : rubriques, dans l'ordre d'affichage ; clés du JSON attendu et valeurs de News.Section
var sectionKeys = []string{"generales", "tech", "alternatives"}

// sectionRank : position de la rubrique dans sectionKeys (inconnue : à la fin)
func sectionRank(key string) int {
	if i := slices.Index(sectionKeys, key); i >= 0 {
		return i
	}
	return len(sectionKeys)
}

// newsPrompt : texte du prompt, réglage global modifiable dans /admin > Paramètres (champ sur une ligne : un paragraphe).
// Variables : {date} (obligatoire), {technologies}, {exclusions} (page Configuration). Le format JSON de la réponse
// (responseFormat) est toujours ajouté par le code : il conditionne la lecture des news.
var newsPrompt = settings.String("NEWS_PROMPT", settings.Options{Group: "news",
	Description: "Prompt envoyé chaque nuit à l'IA. Variables : {date}, {technologies}, {exclusions} ; format JSON de la réponse ajouté automatiquement",
	MaxLength:   8000, Validate: validatePrompt, Default: "Tu es un journaliste chargé d'une revue de presse quotidienne pour un développeur logiciel senior. " +
		"Utilise la recherche web pour trouver l'actualité du {date} (la veille). " +
		"Rubriques : « generales » : jusqu'à 10 news générales importantes (monde, France, économie, science...) ; " +
		"« tech » : jusqu'à 10 news informatique et technologie (sorties, sécurité, IA, développement, matériel, industrie...) ; " +
		"« alternatives » : jusqu'à 10 alternatives récentes (nouveaux outils, forks, projets annoncés ou qui font parler d'eux) " +
		"aux technologies suivantes : {technologies}, la description précise à quelle technologie c'est une alternative et ce qu'elle apporte. " +
		"Exclure de toutes les rubriques : {exclusions}. " +
		"Règles : uniquement des faits trouvés par la recherche web, jamais inventés ; chaque news porte le lien exact de l'article source ; " +
		"titre et description en français, description de 1 à 3 phrases ; 1 à 3 catégories par news, un mot ou une courte expression " +
		"(30 caractères maximum) sans virgule, en réutilisant les mêmes libellés quand le sujet est identique."})

// validatePrompt : {date} requis (sans lui, l'IA ne sait pas de quel jour donner les news)
func validatePrompt(v string) error {
	if !strings.Contains(v, "{date}") {
		return errors.New("the prompt must contain {date}")
	}
	return nil
}

// responseFormat : ajouté à tout prompt, quel qu'en soit le texte (lu par parseNews)
const responseFormat = "\n\nRéponds UNIQUEMENT avec un objet JSON, sans aucun autre texte ni balise Markdown, au format :\n" +
	`{"generales": [{"title": "...", "description": "...", "link": "https://...", "source": "Nom du média", "categories": ["..."]}], "tech": [...], "alternatives": [...]}` + "\n"

// buildNewsPrompt : réglage NEWS_PROMPT aux variables remplacées, puis format de réponse
func buildNewsPrompt(day time.Time, techs []string, excludes []string) string {
	technologies, exclusions := "aucune (laisse cette rubrique vide)", "aucun sujet"
	if len(techs) > 0 {
		technologies = strings.Join(techs, ", ")
	}
	if len(excludes) > 0 {
		exclusions = strings.Join(excludes, ", ")
	}
	text := strings.NewReplacer("{date}", day.Format("02/01/2006"), "{technologies}", technologies, "{exclusions}", exclusions).
		Replace(newsPrompt.Get())
	return text + responseFormat
}

// conversationText : texte de la réponse de l'API Conversations. Avec web_search, le contenu est une liste de chunks :
// du texte entrecoupé de références web (tool_reference), ignorées ; sans outil, une simple chaîne.
func conversationText(body []byte) (string, error) {
	var resp struct {
		Outputs []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("réponse de Mistral illisible (%v) : %s", err, truncate(string(body), 1000))
	}
	var text strings.Builder
	for _, out := range resp.Outputs {
		if out.Type != "message.output" {
			continue
		}
		if err := appendContent(&text, out.Content); err != nil {
			return "", err
		}
	}
	return text.String(), nil
}

// appendContent : ajoute à text le contenu d'un message (chaîne, ou chunks dont seuls ceux de type text sont gardés)
func appendContent(text *strings.Builder, content json.RawMessage) error {
	var s string
	if json.Unmarshal(content, &s) == nil {
		text.WriteString(s)
		return nil
	}
	var chunks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &chunks); err != nil {
		return fmt.Errorf("contenu de la réponse illisible (%v) : %s", err, truncate(string(content), 1000))
	}
	for _, c := range chunks {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return nil
}

// aiNews : une news telle que décrite par le prompt
type aiNews struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Link        string   `json:"link"`
	Source      string   `json:"source"`
	Categories  []string `json:"categories"`
}

// parseNews : news de la réponse de l'IA, datées de day. L'IA encadre parfois sa réponse de texte ou de Markdown : l'objet
// JSON est isolé entre la première { et la dernière }. Une news sans titre ou sans lien http(s) est ignorée.
func parseNews(content string, day string) ([]News, error) {
	first, last := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if first == -1 || last < first {
		return nil, fmt.Errorf("aucun objet JSON dans la réponse : %s", truncate(content, 1000))
	}
	var sections map[string][]aiNews
	if err := json.Unmarshal([]byte(content[first:last+1]), &sections); err != nil {
		return nil, fmt.Errorf("JSON invalide (%v) : %s", err, truncate(content, 1000))
	}
	var items []News
	for _, key := range sectionKeys {
		for _, n := range sections[key] {
			if item, ok := toNews(n, key, day); ok {
				items = append(items, item)
			}
		}
	}
	return items, nil
}

// toNews : news nettoyée (titre et lien http(s) requis, source par défaut = domaine du lien) ; false si inexploitable
func toNews(n aiNews, section, day string) (News, bool) {
	title, rawLink := strings.TrimSpace(n.Title), strings.TrimSpace(n.Link)
	link, err := url.Parse(rawLink)
	if title == "" || err != nil || (link.Scheme != "http" && link.Scheme != "https") || link.Host == "" {
		return News{}, false
	}
	source := truncate(n.Source, 40)
	if source == "" {
		source = link.Hostname()
	}
	return News{Day: day, Section: section, Title: title, Description: strings.TrimSpace(n.Description),
		Link: rawLink, Source: source, Categories: strings.Join(cleanCategories(n.Categories), ",")}, true
}

// cleanCategories : au plus maxCategoriesPerNews catégories non vides, bornées, sans virgule (séparateur en base)
func cleanCategories(raw []string) []string {
	cats := []string{}
	for _, c := range raw {
		c = truncate(strings.ReplaceAll(c, ",", " "), maxCategoryLen)
		if c != "" && !slices.Contains(cats, c) && len(cats) < maxCategoriesPerNews {
			cats = append(cats, c)
		}
	}
	return cats
}

// truncate : espaces normalisés, au plus max caractères (runes), sans couper un caractère multi-octets
func truncate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}
