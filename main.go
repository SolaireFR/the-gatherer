package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ==========================================
// 1. STRUCTS DE BASE
// ==========================================

type Article struct {
	ID          int
	Title       string
	Description string
	Date        string
	Link        string
	Source      string
}

type APISource struct {
	Name    string
	URL     string
	Headers map[string]string
	Parse   func(body []byte) ([]Article, error)
}

type APIStatus struct {
	SourceName string
	ErrorMsg   string
	LastCheck  string
}

// ==========================================
// 2. GESTION DE L'ÉTAT ET DES DONNÉES EN MÉMOIRE
// ==========================================

var (
	apiStatuses = make(map[string]APIStatus)
	statusMutex sync.RWMutex

	// Stockage en RAM temporaire pour afficher les news sans BDD
	memArticles []Article
	memMutex    sync.RWMutex
)

func setAPIStatus(name string, errMsg string) {
	statusMutex.Lock()
	defer statusMutex.Unlock()
	apiStatuses[name] = APIStatus{
		SourceName: name,
		ErrorMsg:   errMsg,
		LastCheck:  time.Now().Format("2006-01-02 15:04:05"),
	}
}

// ==========================================
// 3. BASE DE DONNÉES (Actuellement désactivée)
// ==========================================

func initDB(filepath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", filepath)
	if err != nil {
		return nil, err
	}

	query := `
	CREATE TABLE IF NOT EXISTS articles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT,
		description TEXT,
		date DATETIME,
		link TEXT UNIQUE
	);`

	if _, err = db.Exec(query); err != nil {
		return nil, err
	}

	_, _ = db.Exec(`ALTER TABLE articles ADD COLUMN source TEXT DEFAULT 'Inconnue'`)
	return db, nil
}

func saveArticles(db *sql.DB, articles []Article) {
	query := `INSERT OR IGNORE INTO articles (title, description, date, link, source) VALUES (?, ?, ?, ?, ?)`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Println("Erreur préparation DB:", err)
		return
	}
	defer stmt.Close()

	for _, a := range articles {
		_, err := stmt.Exec(a.Title, a.Description, a.Date, a.Link, a.Source)
		if err != nil {
			log.Println("Erreur insertion article:", err)
		}
	}
}

// ==========================================
// 4. PREPARATION API MISTRAL
// ==========================================

// Structures pour l'endpoint chat de Mistral (https://api.mistral.ai/v1/chat/completions)
type MistralMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type MistralRequest struct {
	Model       string           `json:"model"`
	Messages    []MistralMessage `json:"messages"`
	Temperature float64          `json:"temperature,omitempty"`
}

// Crée le prompt contenant la liste des articles avec leur ID
func buildMistralPrompt(articles []Article) string {
	prompt := "Tu es un expert en veille technologique informatique.\n"
	prompt += "Voici une liste d'articles récupérés aujourd'hui. Chaque article est précédé de son ID.\n"
	prompt += "Analyse les titres et renvoie-moi UNIQUEMENT une liste des IDs séparés par des virgules (ex: 1, 5, 12) pour les articles les plus pertinents et techniques.\n\n"

	for _, a := range articles {
		// On limite un peu la description pour ne pas exploser les tokens si besoin
		desc := a.Description
		if len(desc) > 150 {
			desc = desc[:150] + "..."
		}
		prompt += fmt.Sprintf("ID: %d | Titre: %s | Extrait: %s\n", a.ID, a.Title, desc)
	}
	return prompt
}

// ==========================================
// 5. FETCH API GENERIQUE
// ==========================================

func fetchAllSources(db *sql.DB, sources []APISource) {
	log.Println("Début de la récupération des articles...")
	client := &http.Client{Timeout: 10 * time.Second}

	var allFetchedArticles []Article
	idCounter := 1 // Compteur pour assigner un ID unique à chaque article récupéré

	for _, source := range sources {
		log.Printf("Interrogation de %s...", source.Name)

		req, err := http.NewRequest("GET", source.URL, nil)
		if err != nil {
			setAPIStatus(source.Name, fmt.Sprintf("Erreur création requête: %v", err))
			continue
		}

		for key, value := range source.Headers {
			req.Header.Add(key, value)
		}

		resp, err := client.Do(req)
		if err != nil {
			setAPIStatus(source.Name, fmt.Sprintf("Erreur réseau: %v", err))
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			setAPIStatus(source.Name, fmt.Sprintf("Erreur lecture: %v", err))
			continue
		}

		if resp.StatusCode != http.StatusOK {
			setAPIStatus(source.Name, fmt.Sprintf("Code HTTP %d - Réponse: %s", resp.StatusCode, string(body)))
			continue
		}

		articles, err := source.Parse(body)
		if err != nil {
			setAPIStatus(source.Name, fmt.Sprintf("Erreur parsing JSON: %v", err))
			continue
		}

		// Préparation des articles avec attribution de l'ID temporaire
		for i := range articles {
			articles[i].Source = source.Name
			articles[i].ID = idCounter
			idCounter++
			allFetchedArticles = append(allFetchedArticles, articles[i])
		}

		log.Printf("[%s] %d articles récupérés.\n", source.Name, len(articles))
		setAPIStatus(source.Name, "")
	}

	// SAUVEGARDE BDD COMMENTÉE POUR L'INSTANT[cite: 6]
	// saveArticles(db, allFetchedArticles) 
	// log.Println("Articles sauvegardés en BDD.")

	// PRÉPARATION MISTRAL
	mistralKey := os.Getenv("MISTRAL_API_KEY")
	if mistralKey != "" && len(allFetchedArticles) > 0 {
		prompt := buildMistralPrompt(allFetchedArticles)
		log.Println("🤖 Prompt Mistral généré (Prêt à être envoyé) :")
		log.Println(prompt[:200] + "... (tronqué pour les logs)")
		
		// TODO: Ici viendra la requête HTTP POST vers https://api.mistral.ai/v1/chat/completions
	}

	// Mise à jour de la mémoire pour affichage Web
	memMutex.Lock()
	memArticles = allFetchedArticles
	memMutex.Unlock()
	log.Printf("Mise à jour de la mémoire avec %d articles.", len(allFetchedArticles))
}

// ==========================================
// 6. CRON (TICKER)
// ==========================================

func startCron(db *sql.DB, sources []APISource) {
	fetchAllSources(db, sources)

	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		for range ticker.C {
			fetchAllSources(db, sources)
		}
	}()
}

// ==========================================
// 7. SERVEUR WEB ET HTML
// ==========================================

const htmlTemplate = `
<!DOCTYPE html>
<html lang="fr">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>The Gatherer - Veille</title>
    <style>
        body { font-family: Arial, sans-serif; max-width: 800px; margin: 40px auto; padding: 0 20px; background-color: #f4f4f9; color: #333; }
        .errors-container { background: #ffebee; border-left: 5px solid #f44336; padding: 15px; margin-bottom: 30px; border-radius: 4px; }
        .errors-container h3 { margin-top: 0; color: #d32f2f; font-size: 1.1em; }
        .error-item { margin-bottom: 8px; font-size: 0.9em; color: #333; }
        .error-item strong { color: #b71c1c; }
        .article { background: white; padding: 20px; border-radius: 8px; margin-bottom: 20px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); border-left: 4px solid #0056b3; }
        .article h2 { margin-top: 0; font-size: 1.3em; }
        .article a { color: #0056b3; text-decoration: none; }
        .article a:hover { text-decoration: underline; }
        .meta { color: #666; font-size: 0.85em; margin-bottom: 10px; display: flex; gap: 15px; align-items: center; }
        .source { background: #e2e8f0; padding: 2px 8px; border-radius: 12px; font-weight: bold; }
        .id-badge { background: #ff9800; color: white; padding: 2px 8px; border-radius: 4px; font-weight: bold; }
        form { margin-bottom: 30px; background: white; padding: 15px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        input[type="date"], button { padding: 8px; margin-right: 10px; border: 1px solid #ccc; border-radius: 4px; }
        button { background-color: #0056b3; color: white; cursor: pointer; border: none; }
    </style>
</head>
<body>
    <h1>🚀 The Gatherer - Veille</h1>

    {{if .Errors}}
    <div class="errors-container">
        <h3>⚠️ Problème de synchronisation API</h3>
        {{range .Errors}}
        <div class="error-item">
            <strong>{{.SourceName}}</strong> (Dernier essai : {{.LastCheck}}) <br>
            <i>Détail : {{.ErrorMsg}}</i>
        </div>
        {{end}}
    </div>
    {{end}}
    
    <div style="background: #e3f2fd; padding: 15px; border-radius: 8px; margin-bottom: 20px;">
        ℹ️ <strong>Mode Live :</strong> Les articles affichés sont directement récupérés depuis la mémoire (BDD désactivée). Chaque article possède un <strong>ID temporaire</strong> prêt pour l'analyse Mistral.
    </div>

    {{if .Articles}}
        {{range .Articles}}
        <div class="article">
            <h2><a href="{{.Link}}" target="_blank">{{.Title}}</a></h2>
            <div class="meta">
                <span class="id-badge">ID: {{.ID}}</span>
                <span class="source">{{.Source}}</span>
                <span>📅 {{.Date}}</span>
            </div>
            <p>{{.Description}}</p>
        </div>
        {{end}}
    {{else}}
        <p>Aucun article récupéré pour le moment (en attente du cron).</p>
    {{end}}
</body>
</html>
`

func handleIndex(db *sql.DB) http.HandlerFunc {
	tmpl := template.Must(template.New("index").Parse(htmlTemplate))

	return func(w http.ResponseWriter, r *http.Request) {
		// REQUÊTE EN BDD COMMENTÉE[cite: 6]
		/*
			start := r.URL.Query().Get("start")
			end := r.URL.Query().Get("end")
			articles, err := getArticles(db, start, end)
		*/

		// LECTURE DEPUIS LA MÉMOIRE RAM DIRECTEMENT
		memMutex.RLock()
		articles := make([]Article, len(memArticles))
		copy(articles, memArticles)
		memMutex.RUnlock()

		// Récupération des erreurs API actives
		statusMutex.RLock()
		var activeErrors []APIStatus
		for _, status := range apiStatuses {
			if status.ErrorMsg != "" {
				activeErrors = append(activeErrors, status)
			}
		}
		statusMutex.RUnlock()

		sort.Slice(activeErrors, func(i, j int) bool {
			return activeErrors[i].SourceName < activeErrors[j].SourceName
		})

		tmpl.Execute(w, struct {
			Start    string
			End      string
			Articles []Article
			Errors   []APIStatus
		}{"", "", articles, activeErrors})
	}
}

// ==========================================
// 8. MAIN & CONFIGURATION DES APIs
// ==========================================

func main() {
	dbPath := "./data/veille.db"
	db, err := initDB(dbPath)
	if err != nil {
		log.Fatal("Impossible d'initialiser la base:", err)
	}
	defer db.Close()

	currentsKey := os.Getenv("CURRENTS_API_KEY")
	gnewsKey := os.Getenv("GNEWS_API_KEY")
	newsapiKey := os.Getenv("NEWS_API_KEY")

	sources := []APISource{
		{
			Name: "Dev.to",
			URL:  "https://dev.to/api/articles?tag=programming&per_page=15",
			Parse: func(body []byte) ([]Article, error) {
				var devTo []struct {
					Title       string `json:"title"`
					Description string `json:"description"`
					URL         string `json:"url"`
					PublishedAt string `json:"published_at"`
				}
				if err := json.Unmarshal(body, &devTo); err != nil {
					return nil, err
				}
				var articles []Article
				for _, a := range devTo {
					articles = append(articles, Article{Title: a.Title, Description: a.Description, Link: a.URL, Date: a.PublishedAt})
				}
				return articles, nil
			},
		},
	}

	if currentsKey != "" {
		sources = append(sources, APISource{
			Name: "CurrentsAPI",
			URL:  "https://api.currentsapi.services/v1/latest-news?language=en&category=technology",
			Headers: map[string]string{
				"Authorization": currentsKey,
			},
			Parse: func(body []byte) ([]Article, error) {
				var res struct {
					News []struct {
						Title       string `json:"title"`
						Description string `json:"description"`
						URL         string `json:"url"`
						Published   string `json:"published"`
					} `json:"news"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					return nil, err
				}
				var articles []Article
				for _, a := range res.News {
					articles = append(articles, Article{Title: a.Title, Description: a.Description, Link: a.URL, Date: a.Published})
				}
				return articles, nil
			},
		})
	}

	if gnewsKey != "" {
		sources = append(sources, APISource{
			Name: "GNews",
			URL:  fmt.Sprintf("https://gnews.io/api/v4/top-headlines?category=technology&lang=en&apikey=%s", gnewsKey),
			Parse: func(body []byte) ([]Article, error) {
				var res struct {
					Articles []struct {
						Title       string `json:"title"`
						Description string `json:"description"`
						URL         string `json:"url"`
						PublishedAt string `json:"publishedAt"`
					} `json:"articles"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					return nil, err
				}
				var articles []Article
				for _, a := range res.Articles {
					articles = append(articles, Article{Title: a.Title, Description: a.Description, Link: a.URL, Date: a.PublishedAt})
				}
				return articles, nil
			},
		})
	}

	if newsapiKey != "" {
		sources = append(sources, APISource{
			Name: "NewsAPI",
			URL:  fmt.Sprintf("https://newsapi.org/v2/top-headlines?category=technology&apiKey=%s", newsapiKey),
			Parse: func(body []byte) ([]Article, error) {
				var res struct {
					Articles []struct {
						Title       string `json:"title"`
						Description string `json:"description"`
						URL         string `json:"url"`
						PublishedAt string `json:"publishedAt"`
					} `json:"articles"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					return nil, err
				}
				var articles []Article
				for _, a := range res.Articles {
					articles = append(articles, Article{Title: a.Title, Description: a.Description, Link: a.URL, Date: a.PublishedAt})
				}
				return articles, nil
			},
		})
	}

	startCron(db, sources)

	http.HandleFunc("/", handleIndex(db))

	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	log.Printf("Serveur démarré sur http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
