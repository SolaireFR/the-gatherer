package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ==========================================
// 1. STRUCTS DE BASE
// ==========================================

type Article struct {
	ID           int
	Title        string
	Description  string
	Date         string
	Link         string
	Source       string
	MistralValid bool
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
// 2. GESTION DE L'ÉTAT ET DES ERREURS
// ==========================================

var (
	apiStatuses = make(map[string]APIStatus)
	statusMutex sync.RWMutex
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
// 3. BASE DE DONNÉES
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

	// Mises à jour du schéma (ignorées si elles existent déjà)
	_, _ = db.Exec(`ALTER TABLE articles ADD COLUMN source TEXT DEFAULT 'Inconnue'`)
	_, _ = db.Exec(`ALTER TABLE articles ADD COLUMN created_at DATETIME DEFAULT CURRENT_TIMESTAMP`)

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

// Récupère les articles de la BDD (Dernières 24H par défaut ou selon les filtres)
func getArticles(db *sql.DB, start string, end string) ([]Article, error) {
	query := `SELECT id, title, description, date, link, source FROM articles WHERE 1=1`
	var args []interface{}

	if start != "" {
		query += ` AND created_at >= ?`
		args = append(args, start+" 00:00:00")
	} else {
		// Par défaut : les dernières 24 heures d'insertion
		query += ` AND created_at >= datetime('now', '-1 day')`
	}

	if end != "" {
		query += ` AND created_at <= ?`
		args = append(args, end+" 23:59:59")
	}

	query += ` ORDER BY created_at DESC`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var a Article
		if err := rows.Scan(&a.ID, &a.Title, &a.Description, &a.Date, &a.Link, &a.Source); err == nil {
			a.MistralValid = true // S'il est en BDD, c'est qu'il a été validé par l'IA
			articles = append(articles, a)
		}
	}
	return articles, nil
}

// ==========================================
// 4. PREPARATION API MISTRAL
// ==========================================

type MistralMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type MistralRequest struct {
	Model       string           `json:"model"`
	Messages    []MistralMessage `json:"messages"`
	Temperature float64          `json:"temperature,omitempty"`
}

func buildMistralPrompt(articles []Article) string {
	prompt := "Tu es un développeur logiciel senior chargé de filtrer une veille technique et technologique.\n"
	prompt += "Voici une liste d'articles avec leur ID.\n\n"
	prompt += "RÈGLES DE SÉLECTION :\n"
	prompt += "- INCLURE : Articles 100% concrets et techniques utiles à un développeur (ex: Go, Angular, NestJS, Flutter, Docker, architecture logicielle, auto-hébergement, LLM locaux, sécurité, CI/CD).\n"
	prompt += "- EXCLURE : Les sujets abstraits, la tech grand public (nouveaux smartphones), les levées de fonds, la cryptomonnaie, la politique, ou l'éthique de l'IA.\n\n"
	prompt += "Format de réponse exigé : Renvoie UNIQUEMENT un tableau JSON contenant les IDs pertinents, sans aucun autre texte. Exemple : [1, 5, 12]\n\n"
	prompt += "Articles :\n"

	for _, a := range articles {
		title := a.Title
		if len(title) > 100 {
			title = title[:100] + "..."
		}
		desc := a.Description
		if len(desc) > 150 {
			desc = desc[:150] + "..."
		}
		prompt += fmt.Sprintf("ID: %d | Titre: %s | Extrait: %s\n", a.ID, title, desc)
	}
	return prompt
}

// ==========================================
// 5. FETCH API GENERIQUE ET APPEL MISTRAL
// ==========================================

func fetchAllSources(db *sql.DB, sources []APISource) {
	log.Println("Début de la récupération des articles...")
	client := &http.Client{Timeout: 10 * time.Second}

	var allFetchedArticles []Article
	idCounter := 1

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

		for i := range articles {
			articles[i].Source = source.Name
			articles[i].ID = idCounter
			idCounter++
			allFetchedArticles = append(allFetchedArticles, articles[i])
		}

		setAPIStatus(source.Name, "")
	}

	log.Printf("Total de %d articles bruts récupérés. Envoi à Mistral...", len(allFetchedArticles))

	// APPEL MISTRAL ET SAUVEGARDE EN BDD
	mistralKey := os.Getenv("MISTRAL_API_KEY")
	if mistralKey != "" && len(allFetchedArticles) > 0 {
		prompt := buildMistralPrompt(allFetchedArticles)
		
		reqBody := MistralRequest{
			Model:       "mistral-large-latest",
			Temperature: 0.1,
			Messages: []MistralMessage{
				{Role: "user", Content: prompt},
			},
		}
		jsonBody, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", "https://api.mistral.ai/v1/chat/completions", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+mistralKey)

		mistralClient := &http.Client{Timeout: 30 * time.Second}
		resp, err := mistralClient.Do(req)

		if err != nil {
			log.Println("Erreur HTTP Mistral:", err)
			return
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var mistralResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		json.Unmarshal(bodyBytes, &mistralResp)

		if len(mistralResp.Choices) > 0 {
			content := mistralResp.Choices[0].Message.Content
			re := regexp.MustCompile(`\d+`)
			matches := re.FindAllString(content, -1)

			validIDs := make(map[int]bool)
			for _, m := range matches {
				if id, err := strconv.Atoi(m); err == nil {
					validIDs[id] = true
				}
			}

			// On isole uniquement les articles validés par l'IA
			var validArticles []Article
			for i := range allFetchedArticles {
				if validIDs[allFetchedArticles[i].ID] {
					allFetchedArticles[i].MistralValid = true
					validArticles = append(validArticles, allFetchedArticles[i])
				}
			}

			// SAUVEGARDE EN BDD : Uniquement les articles validés !
			saveArticles(db, validArticles)
			log.Printf("✅ %d articles validés par Mistral ont été sauvegardés en BDD.", len(validArticles))
		}
	}
}

// ==========================================
// 6. CRON (TICKER INTELLIGENT)
// ==========================================

func startCron(db *sql.DB, sources []APISource) {
	// Exécution au démarrage
	fetchAllSources(db, sources)

	go func() {
		for {
			now := time.Now()
			// Calcul de la date de la prochaine exécution à 5h00
			next := time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location())
			
			// Si on a déjà passé 5h00 aujourd'hui, on planifie pour demain à 5h00
			if now.After(next) {
				next = next.Add(24 * time.Hour)
			}
			
			duration := next.Sub(now)
			log.Printf("Prochaine récupération prévue dans %v (à %v)", duration, next.Format("2006-01-02 15:04:05"))
			
			// Le programme se met en pause jusqu'à l'heure cible
			time.Sleep(duration)
			
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
        body { font-family: Arial, sans-serif; max-width: 900px; margin: 40px auto; padding: 0 20px; background-color: #f4f4f9; color: #333; }
        .errors-container { background: #ffebee; border-left: 5px solid #f44336; padding: 15px; margin-bottom: 30px; border-radius: 4px; }
        .errors-container h3 { margin-top: 0; color: #d32f2f; font-size: 1.1em; }
        .error-item { margin-bottom: 8px; font-size: 0.9em; color: #333; }
        .error-item strong { color: #b71c1c; }
        form { margin-bottom: 30px; background: white; padding: 15px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        input[type="date"], button { padding: 8px; margin-right: 10px; border: 1px solid #ccc; border-radius: 4px; }
        button { background-color: #0056b3; color: white; cursor: pointer; border: none; }
        .reset-btn { text-decoration: none; padding: 8px 12px; background: #e0e0e0; border-radius: 4px; color: #333; font-size: 0.9em; }
        .article { background: white; padding: 20px; border-radius: 8px; margin-bottom: 20px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); border-left: 4px solid #0056b3; }
        .article h2 { margin-top: 0; font-size: 1.3em; }
        .article a { color: #0056b3; text-decoration: none; }
        .article a:hover { text-decoration: underline; }
        .meta { color: #666; font-size: 0.85em; margin-bottom: 10px; display: flex; gap: 15px; align-items: center; }
        .source { background: #e2e8f0; padding: 2px 8px; border-radius: 12px; font-weight: bold; }
        .mistral-badge { background: #4caf50; color: white; padding: 2px 8px; border-radius: 4px; font-weight: bold; }
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
    
    <form method="GET" action="/">
        <label>Du :</label> <input type="date" name="start" value="{{.Start}}">
        <label>Au :</label> <input type="date" name="end" value="{{.End}}">
        <button type="submit">Filtrer</button>
        <a href="/" class="reset-btn">Reset (24H)</a>
    </form>

    <!-- LISTE ARTICLES -->
    {{if .Articles}}
        {{range .Articles}}
        <div class="article">
            <h2><a href="{{.Link}}" target="_blank">{{.Title}}</a></h2>
            <div class="meta">
                <span class="source">{{.Source}}</span>
                <span>📅 {{.Date}}</span>
                {{if .MistralValid}}
                    <span class="mistral-badge">✨ Validé par l'IA</span>
                {{end}}
            </div>
            <p>{{.Description}}</p>
        </div>
        {{end}}
    {{else}}
        <p>Aucun article technique pertinent trouvé pour cette période.</p>
    {{end}}
</body>
</html>
`

func handleIndex(db *sql.DB) http.HandlerFunc {
	tmpl := template.Must(template.New("index").Parse(htmlTemplate))

	return func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("start")
		end := r.URL.Query().Get("end")

		// Récupération depuis la BDD directement
		articles, err := getArticles(db, start, end)
		if err != nil {
			log.Println("Erreur lors de la récupération des articles BDD:", err)
		}

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
		}{start, end, articles, activeErrors})
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
