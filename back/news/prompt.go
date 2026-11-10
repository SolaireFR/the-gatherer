package news

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// newsPerSection : nombre maximum de news demandées par rubrique
const newsPerSection = 10

// maxCategoryLen, maxCategoriesPerNews : bornes des catégories inventées par l'IA
const (
	maxCategoryLen       = 30
	maxCategoriesPerNews = 3
)

// newsSections : rubriques demandées à l'IA, dans l'ordre d'affichage ; Key = clé du JSON attendu et valeur de News.Section
var newsSections = []struct{ Key, Ask string }{
	{"generales", "news générales importantes (monde, France, économie, science...)"},
	{"tech", "news informatique et technologie (sorties, sécurité, IA, développement, matériel, industrie...)"},
	{"alternatives", "alternatives récentes (nouveaux outils, forks, projets annoncés ou qui font parler d'eux) aux technologies suivantes : %s. La description précise à quelle technologie c'est une alternative et ce qu'elle apporte"},
}

// sectionRank : position de la rubrique dans newsSections (inconnue : à la fin)
func sectionRank(key string) int {
	if i := slices.IndexFunc(newsSections, func(s struct{ Key, Ask string }) bool { return s.Key == key }); i >= 0 {
		return i
	}
	return len(newsSections)
}

// buildNewsPrompt : un seul prompt pour les trois rubriques ; sans technologie suivie, pas de rubrique « alternatives »
func buildNewsPrompt(day time.Time, techs []string, excludes []string) string {
	prompt := "Tu es un journaliste chargé d'une revue de presse quotidienne pour un développeur logiciel senior.\n"
	prompt += fmt.Sprintf("Utilise la recherche web pour trouver l'actualité du %s (la veille).\n\nRUBRIQUES :\n", day.Format("02/01/2006"))
	for _, s := range newsSections {
		if s.Key == "alternatives" && len(techs) == 0 {
			continue
		}
		ask := s.Ask
		if strings.Contains(ask, "%s") {
			ask = fmt.Sprintf(ask, strings.Join(techs, ", "))
		}
		prompt += fmt.Sprintf("- \"%s\" : jusqu'à %d %s.\n", s.Key, newsPerSection, ask)
	}
	if len(excludes) > 0 {
		prompt += fmt.Sprintf("- EXCLURE de toutes les rubriques : %s.\n", strings.Join(excludes, ", "))
	}
	prompt += "\nRÈGLES :\n"
	prompt += "- Uniquement des faits trouvés par la recherche web, jamais inventés. Chaque news porte le lien exact de l'article source.\n"
	prompt += "- Titre et description en français ; description de 1 à 3 phrases.\n"
	prompt += fmt.Sprintf("- 1 à %d catégories par news : un mot ou une courte expression (%d caractères maximum), sans virgule. Réutilise les mêmes libellés quand le sujet est identique.\n", maxCategoriesPerNews, maxCategoryLen)
	prompt += "- Réponds UNIQUEMENT avec un objet JSON, sans aucun autre texte ni balise Markdown.\n\nFormat exigé :\n"
	prompt += `{"generales": [{"title": "...", "description": "...", "link": "https://...", "source": "Nom du média", "categories": ["..."]}], "tech": [...], "alternatives": [...]}` + "\n"
	return prompt
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
	for _, s := range newsSections {
		for _, n := range sections[s.Key] {
			if item, ok := toNews(n, s.Key, day); ok {
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
