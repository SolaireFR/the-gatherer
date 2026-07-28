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

// APIStatus conserve l'état de santé de chaque API
type APIStatus struct {
	SourceName string
	ErrorMsg   string
	LastCheck  string
}

// ==========================================
// 2. GESTION DE L'ÉTAT DES APIs (Thread-Safe)
// ==========================================

var (
	apiStatuses = make(map[string]APIStatus)
	statusMutex sync.RWMutex
)

// setAPIStatus met à jour le statut d'une API. Si errMsg est vide, c'est un succès.
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
// 3. BASE DE DONNÉES & MIGRATIONS
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

func getArticles(db *sql.DB, startDate, endDate string) ([]Article, error) {
	query := `
		SELECT id, title, description, date, link, source 
		FROM articles 
		WHERE date(date) >= date(?) AND date(date) <= date(?)
		ORDER BY date DESC
	`
	rows, err := db.Query(query, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var a Article
		if err := rows.Scan(&a.ID, &a.Title, &a.Description, &a.Date, &a.Link, &a.Source); err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, nil
}

// ==========================================
// 4. FETCH API GENERIQUE
// ==========================================

func fetchAllSources(db *sql.DB, sources []APISource) {
	log.Println("Début de la récupération des articles...")
	client := &http.Client{Timeout: 10 * time.Second}

	for _, source := range sources {
		log.Printf("Interrogation de %s...", source.Name)

		req, err := http.NewRequest("GET", source.URL, nil)
		if err != nil {
			msg := fmt.Sprintf("Erreur création requête: %v", err)
			log.Printf("[%s] %s", source.Name, msg)
			setAPIStatus(source.Name, msg)
			continue
		}

		for key, value := range source.Headers {
			req.Header.Add(key, value)
		}

		resp, err := client.Do(req)
		if err != nil {
			msg := fmt.Sprintf("Erreur réseau: %v", err)
			log.Printf("[%s] %s", source.Name, msg)
			setAPIStatus(source.Name, msg)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			msg := fmt.Sprintf("Erreur lecture réponse: %v", err)
			log.Printf("[%s] %s", source.Name, msg)
			setAPIStatus(source.Name, msg)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			msg := fmt.Sprintf("Code HTTP %d - Réponse: %s", resp.StatusCode, string(body))
			log.Printf("[%s] %s", source.Name, msg)
			setAPIStatus(source.Name, msg)
			continue
		}

		articles, err := source.Parse(body)
		if err != nil {
			msg := fmt.Sprintf("Erreur parsing JSON: %v", err)
			log.Printf("[%s] %s", source.Name, msg)
			setAPIStatus(source.Name, msg)
			continue
		}

		for i := range articles {
			articles[i].Source = source.Name
		}

		saveArticles(db, articles)
		log.Printf("[%s] %d articles traités.\n", source.Name, len(articles))
		
		// Si on arrive ici, l'API fonctionne parfaitement, on efface les erreurs précédentes
		setAPIStatus(source.Name, "")
	}
}

// ==========================================
// 5. CRON (TICKER)
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
// 6. SERVEUR WEB ET HTML
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
        .article { background: white; padding: 20px; border-radius: 8px; margin-bottom: 20px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .article h2 { margin-top: 0; font-size: 1.3em; }
        .article a { color: #0056b3; text-decoration: none; }
        .article a:hover { text-decoration: underline; }
        .meta { color: #666; font-size: 0.85em; margin-bottom: 10px; display: flex; gap: 15px; }
        .source { background: #e2e8f0; padding: 2px 8px; border-radius: 12px; font-weight: bold; }
        form { margin-bottom: 30px; background: white; padding: 15px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        input[type="date"], button { padding: 8px; margin-right: 10px; border: 1px solid #ccc; border-radius: 4px; }
        button { background-color: #0056b3; color: white; cursor: pointer; border: none; }
    </style>
</head>
<body>
    <h1>🚀 The Gatherer - Veille Informatique</h1>

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
    
    <form method="GET" action="/">
        <label>Du : <input type="date" name="start" value="{{.Start}}"></label>
        <label>Au : <input type="date" name="end" value="{{.End}}"></label>
        <button type="submit">Filtrer</button>
    </form>

    {{if .Articles}}
        {{range .Articles}}
        <div class="article">
            <h2><a href="{{.Link}}" target="_blank">{{.Title}}</a></h2>
            <div class="meta">
                <span class="source">{{.Source}}</span>
                <span>📅 {{.Date}}</span>
            </div>
            <p>{{.Description}}</p>
        </div>
        {{end}}
    {{else}}
        <p>Aucun article trouvé pour ces dates.</p>
    {{end}}
</body>
</html>
`

func handleIndex(db *sql.DB) http.HandlerFunc {
	tmpl := template.Must(template.New("index").Parse(htmlTemplate))

	return func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("start")
		end := r.URL.Query().Get("end")

		today := time.Now().Format("2006-01-02")
		if start == "" {
			start = today
		}
		if end == "" {
			end = today
		}

		articles, err := getArticles(db, start, end)
		if err != nil {
			http.Error(w, "Erreur serveur", http.StatusInternalServerError)
			return
		}

		// Récupération des erreurs API actives
		statusMutex.RLock()
		var activeErrors []APIStatus
		for _, status := range apiStatuses {
			if status.ErrorMsg != "" {
				activeErrors = append(activeErrors, status)
			}
		}
		statusMutex.RUnlock()

		// Tri alphabétique pour un affichage stable sur le front
		sort.Slice(activeErrors, func(i, j int) bool {
			return activeErrors[i].SourceName < activeErrors[j].SourceName
		})

		tmpl.Execute(w, struct {
			Start    string
			End      string
			Articles []Article
			Errors   []APIStatus
		}{start, end, articles, activeErrors})
	}
}

// ==========================================
// 7. MAIN & CONFIGURATION DES APIs
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