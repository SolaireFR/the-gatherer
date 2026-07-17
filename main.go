package main

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ==========================================
// 1. STRUCTS
// ==========================================

// Article représente notre modèle de données en base
type Article struct {
	ID          int
	Title       string
	Description string
	Date        string
	Link        string
}

// DevToArticle représente la structure JSON renvoyée par l'API
type DevToArticle struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
}

// ==========================================
// 2. BASE DE DONNÉES
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
	
	_, err = db.Exec(query)
	return db, err
}

func saveArticles(db *sql.DB, articles []Article) {
	// Le INSERT OR IGNORE permet d'éviter les doublons basés sur le lien (UNIQUE)
	query := `INSERT OR IGNORE INTO articles (title, description, date, link) VALUES (?, ?, ?, ?)`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Println("Erreur préparation DB:", err)
		return
	}
	defer stmt.Close()

	for _, a := range articles {
		_, err := stmt.Exec(a.Title, a.Description, a.Date, a.Link)
		if err != nil {
			log.Println("Erreur insertion article:", err)
		}
	}
}

func getArticles(db *sql.DB, startDate, endDate string) ([]Article, error) {
	// On utilise la fonction date() de SQLite pour filtrer proprement
	query := `
		SELECT id, title, description, date, link 
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
		if err := rows.Scan(&a.ID, &a.Title, &a.Description, &a.Date, &a.Link); err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, nil
}

// ==========================================
// 3. FETCH API
// ==========================================

func fetchNewsAPI(db *sql.DB) {
	log.Println("Début de la récupération des articles...")
	
	// API gratuite et sans clé, filtrée sur le tag "programming"
	apiURL := "https://dev.to/api/articles?tag=programming&per_page=30"
	
	resp, err := http.Get(apiURL)
	if err != nil {
		log.Println("Erreur lors de la requête API:", err)
		return
	}
	defer resp.Body.Close()

	var devToArticles []DevToArticle
	if err := json.NewDecoder(resp.Body).Decode(&devToArticles); err != nil {
		log.Println("Erreur lors du décodage JSON:", err)
		return
	}

	var articles []Article
	for _, da := range devToArticles {
		articles = append(articles, Article{
			Title:       da.Title,
			Description: da.Description,
			Date:        da.PublishedAt,
			Link:        da.URL,
		})
	}

	saveArticles(db, articles)
	log.Printf("%d articles récupérés et traités.\n", len(articles))
}

// ==========================================
// 4. CRON (TICKER)
// ==========================================

func startCron(db *sql.DB) {
	// Exécution immédiate au lancement
	fetchNewsAPI(db)

	// Puis exécution toutes les 24 heures
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		for range ticker.C {
			fetchNewsAPI(db)
		}
	}()
}

// ==========================================
// 5. SERVEUR WEB ET HTML
// ==========================================

const htmlTemplate = `
<!DOCTYPE html>
<html lang="fr">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Veille Informatique</title>
    <style>
        body { font-family: Arial, sans-serif; max-width: 800px; margin: 40px auto; padding: 0 20px; background-color: #f4f4f9; color: #333; }
        .article { background: white; padding: 20px; border-radius: 8px; margin-bottom: 20px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .article h2 { margin-top: 0; font-size: 1.4em; }
        .article a { color: #0056b3; text-decoration: none; }
        .article a:hover { text-decoration: underline; }
        .date { color: #888; font-size: 0.9em; margin-bottom: 10px; }
        form { margin-bottom: 30px; background: white; padding: 15px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        input[type="date"], button { padding: 8px; margin-right: 10px; border: 1px solid #ccc; border-radius: 4px; }
        button { background-color: #0056b3; color: white; cursor: pointer; border: none; }
    </style>
</head>
<body>
    <h1>🚀 Veille Informatique</h1>
    
    <form method="GET" action="/">
        <label>Du : <input type="date" name="start" value="{{.Start}}"></label>
        <label>Au : <input type="date" name="end" value="{{.End}}"></label>
        <button type="submit">Filtrer</button>
    </form>

    {{if .Articles}}
        {{range .Articles}}
        <div class="article">
            <h2><a href="{{.Link}}" target="_blank">{{.Title}}</a></h2>
            <div class="date">Publié le : {{.Date}}</div>
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

		// Par défaut, la date du jour
		today := time.Now().Format("2006-01-02")
		if start == "" {
			start = today
		}
		if end == "" {
			end = today
		}

		articles, err := getArticles(db, start, end)
		if err != nil {
			http.Error(w, "Erreur lors de la récupération des articles", http.StatusInternalServerError)
			log.Println(err)
			return
		}

		data := struct {
			Start    string
			End      string
			Articles []Article
		}{
			Start:    start,
			End:      end,
			Articles: articles,
		}

		tmpl.Execute(w, data)
	}
}

// ==========================================
// 6. MAIN
// ==========================================

func main() {
	dbPath := "./data/veille.db"
	db, err := initDB(dbPath)
	if err != nil {
		log.Fatal("Impossible d'initialiser la base de données:", err)
	}
	defer db.Close()

	// Lance la récupération des données en tâche de fond (cron)
	startCron(db)

	// Initialise le serveur web
	http.HandleFunc("/", handleIndex(db))
	
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	log.Printf("Serveur démarré sur http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}