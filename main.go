package main

import (
	"bytes"
	"crypto/subtle"
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
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
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

// Criterion : un critère de recherche modifiable depuis le site.
// Kind vaut "include" (sujet recherché) ou "exclude" (sujet bloqué).
type Criterion struct {
	ID    int
	Label string
	Kind  string
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
	db, err := sql.Open("sqlite", filepath)
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

	// Table des critères de recherche envoyés à l'IA
	criteriaQuery := `
	CREATE TABLE IF NOT EXISTS criteria (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		label TEXT NOT NULL,
		kind TEXT NOT NULL,
		UNIQUE(label, kind)
	);`

	if _, err = db.Exec(criteriaQuery); err != nil {
		return nil, err
	}

	return db, nil
}

// getCriteria renvoie tous les critères d'un type donné ("include" ou "exclude").
func getCriteria(db *sql.DB, kind string) ([]Criterion, error) {
	rows, err := db.Query(`SELECT id, label, kind FROM criteria WHERE kind = ? ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var criteria []Criterion
	for rows.Next() {
		var c Criterion
		if err := rows.Scan(&c.ID, &c.Label, &c.Kind); err == nil {
			criteria = append(criteria, c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return criteria, nil
}

func addCriterion(db *sql.DB, label string, kind string) {
	label = strings.TrimSpace(label)
	if label == "" || (kind != "include" && kind != "exclude") {
		return
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO criteria (label, kind) VALUES (?, ?)`, label, kind)
	if err != nil {
		log.Println("Erreur ajout critère:", err)
	}
}

func deleteCriterion(db *sql.DB, id int) {
	if _, err := db.Exec(`DELETE FROM criteria WHERE id = ?`, id); err != nil {
		log.Println("Erreur suppression critère:", err)
	}
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
	if err := rows.Err(); err != nil {
		return nil, err
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

// labels extrait les libellés d'une liste de critères.
func labels(criteria []Criterion) []string {
	var out []string
	for _, c := range criteria {
		out = append(out, c.Label)
	}
	return out
}

func buildMistralPrompt(articles []Article, includes []Criterion, excludes []Criterion) string {
	prompt := "Tu es un développeur logiciel senior chargé de filtrer une veille technique et technologique.\n"
	prompt += "Voici une liste d'articles avec leur ID.\n\n"
	prompt += "RÈGLES DE SÉLECTION :\n"
	if len(includes) > 0 {
		prompt += fmt.Sprintf("- INCLURE : Articles 100%% concrets et techniques utiles à un développeur (ex: %s).\n", strings.Join(labels(includes), ", "))
	}
	if len(excludes) > 0 {
		prompt += fmt.Sprintf("- EXCLURE : %s.\n", strings.Join(labels(excludes), ", "))
	}
	prompt += "\n"
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
		includes, err := getCriteria(db, "include")
		if err != nil {
			log.Println("Erreur lecture des critères (include):", err)
		}
		excludes, err := getCriteria(db, "exclude")
		if err != nil {
			log.Println("Erreur lecture des critères (exclude):", err)
		}

		prompt := buildMistralPrompt(allFetchedArticles, includes, excludes)

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
// 7. PROTECTION PAR MOT DE PASSE GLOBAL
// ==========================================

// basicAuth bloque tout l'accès au site tant que le bon mot de passe
// (variable d'environnement SITE_PASSWORD) n'a pas été fourni.
// Si la variable est vide, le site reste ouvert.
func basicAuth(next http.HandlerFunc) http.HandlerFunc {
	password := os.Getenv("SITE_PASSWORD")

	if password == "" {
		log.Println("⚠️  SITE_PASSWORD non défini : le site est accessible sans mot de passe.")
		return next
	}

	return func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="The Gatherer"`)
			http.Error(w, "Accès refusé", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ==========================================
// 8. SERVEUR WEB ET HTML
// ==========================================

// formatDate convertit les dates renvoyees par les APIs (formats varies)
// en un affichage lisible : "29/07/2026 13H".
// Si le format est inconnu, la valeur brute est renvoyee telle quelle.
func formatDate(raw string) string {
	layouts := []string{
		time.RFC3339,                // 2026-07-29T13:16:30Z
		"2006-01-02 15:04:05 -0700", // 2026-07-29 13:16:30 +0000
		"2006-01-02T15:04:05.000Z",  // 2026-07-29T13:16:30.000Z
		"2006-01-02 15:04:05",       // 2026-07-29 13:16:30
		"2006-01-02T15:04:05",       // 2026-07-29T13:16:30
		"2006-01-02",                // 2026-07-29
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("02/01/2006 15H")
		}
	}
	return raw
}

const htmlTemplate = `
<!DOCTYPE html>
<html lang="fr">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>The Gatherer - Veille</title>
    <style>
        * { box-sizing: border-box; }
        body { font-family: Arial, sans-serif; max-width: 900px; margin: 30px auto; padding: 0 20px; background-color: #f4f4f9; color: #333; overflow-wrap: break-word; }
        h1 { font-size: 1.5em; }

        .errors-container { background: #ffebee; border-left: 5px solid #f44336; padding: 12px 15px; margin-bottom: 20px; border-radius: 4px; }
        .errors-container h3 { margin-top: 0; color: #d32f2f; font-size: 1em; }
        .error-item { margin-bottom: 8px; font-size: 0.85em; }
        .error-item strong { color: #b71c1c; }

        /* --- ONGLETS (sans JS : radios cachées + sélecteur :checked) --- */
        .tabs { margin-bottom: 20px; }
        .tabs > input { position: absolute; opacity: 0; pointer-events: none; }
        .tab-bar { display: flex; gap: 4px; }
        .tab-bar label { flex: 1; text-align: center; padding: 10px 6px; background: #e2e8f0; border-radius: 8px 8px 0 0; cursor: pointer; font-weight: bold; font-size: 0.9em; color: #555; }
        .panel { display: none; background: white; padding: 15px; border-radius: 0 0 8px 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        #tab-filtre:checked ~ .tab-bar label[for="tab-filtre"] { background: white; color: #0056b3; }
        #tab-criteres:checked ~ .tab-bar label[for="tab-criteres"] { background: white; color: #0056b3; }
        #tab-filtre:checked ~ .panel-filtre { display: block; }
        #tab-criteres:checked ~ .panel-criteres { display: block; }

        input[type="date"], input[type="text"], select, button { padding: 8px; border: 1px solid #ccc; border-radius: 4px; font-size: 0.9em; max-width: 100%; }
        button { background-color: #0056b3; color: white; cursor: pointer; border: none; }
        .reset-btn { text-decoration: none; padding: 8px 12px; background: #e0e0e0; border-radius: 4px; color: #333; font-size: 0.9em; }

        .filter-form { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; margin: 0; }
        .field { display: flex; align-items: center; gap: 6px; }

        /* --- CRITÈRES --- */
        .hint { color: #666; font-size: 0.85em; margin: 0 0 12px 0; }
        .criteria-cols { display: flex; flex-wrap: wrap; gap: 20px; }
        .criteria-col { flex: 1 1 240px; min-width: 0; }
        .criteria-col h4 { margin: 0 0 10px 0; font-size: 0.9em; }
        .tag { display: inline-flex; align-items: center; gap: 4px; padding: 3px 5px 3px 10px; border-radius: 12px; margin: 0 6px 6px 0; font-size: 0.85em; }
        .tag.include { background: #e8f5e9; color: #1b5e20; }
        .tag.exclude { background: #ffebee; color: #b71c1c; }
        .tag form { display: inline; margin: 0; }
        .tag button { background: none; border: none; cursor: pointer; color: inherit; font-size: 1em; padding: 0 3px; opacity: 0.6; }
        .tag button:hover { opacity: 1; }
        .add-form { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 15px; border-top: 1px solid #eee; padding-top: 15px; }
        .add-form input[type="text"] { flex: 1 1 200px; min-width: 0; }

        /* --- ARTICLES (compacts) --- */
        .article { background: white; padding: 10px 14px; border-radius: 6px; margin-bottom: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.08); border-left: 3px solid #0056b3; }
        .article h2 { margin: 0 0 4px 0; font-size: 1em; line-height: 1.3; }
        .article a { color: #0056b3; text-decoration: none; }
        .article a:hover { text-decoration: underline; }
        .meta { color: #666; font-size: 0.75em; margin-bottom: 5px; display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
        .source { background: #e2e8f0; padding: 1px 7px; border-radius: 10px; font-weight: bold; }

        /* --- DESCRIPTION REPLIABLE --- */
        .desc-short { margin: 0; font-size: 0.85em; line-height: 1.4; color: #444; }
        details.desc { font-size: 0.85em; line-height: 1.4; color: #444; }
        details.desc summary { display: block; cursor: pointer; list-style: none; }
        details.desc summary::-webkit-details-marker { display: none; }
        details.desc .txt { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
        details.desc[open] .txt { -webkit-line-clamp: unset; overflow: visible; }
        details.desc .more::after { content: "▾ voir plus"; color: #0056b3; font-size: 0.9em; font-weight: bold; }
        details.desc[open] .more::after { content: "▴ voir moins"; }

        /* --- MOBILE --- */
        @media (max-width: 600px) {
            body { margin: 15px auto; padding: 0 12px; }
            h1 { font-size: 1.2em; }
            .tab-bar label { font-size: 0.8em; padding: 10px 4px; }
            .panel { padding: 12px; }
            .filter-form .field { flex: 1 1 100%; }
            .filter-form .field input { flex: 1; }
            .filter-form button, .filter-form .reset-btn { flex: 1 1 100%; text-align: center; }
            .add-form input[type="text"], .add-form select, .add-form button { flex: 1 1 100%; }
            .criteria-cols { gap: 15px; }
            .article { padding: 9px 12px; }
        }
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

    <div class="tabs">
        <input type="radio" name="tab" id="tab-filtre" {{if not .CriteresTab}}checked{{end}}>
        <input type="radio" name="tab" id="tab-criteres" {{if .CriteresTab}}checked{{end}}>

        <div class="tab-bar">
            <label for="tab-filtre">📅 Filtre par date</label>
            <label for="tab-criteres">🎯 Critères de l'IA</label>
        </div>

        <!-- ONGLET 1 : FILTRE PAR DATE -->
        <div class="panel panel-filtre">
            <form class="filter-form" method="GET" action="/">
                <span class="field"><label>Du :</label> <input type="date" name="start" value="{{.Start}}"></span>
                <span class="field"><label>Au :</label> <input type="date" name="end" value="{{.End}}"></span>
                <button type="submit">Filtrer</button>
                <a href="/" class="reset-btn">Reset (24H)</a>
            </form>
        </div>

        <!-- ONGLET 2 : CRITÈRES DE RECHERCHE DE L'IA -->
        <div class="panel panel-criteres">
            <p class="hint">Ces critères sont envoyés à l'IA lors de la prochaine récupération pour filtrer les articles.</p>

            <div class="criteria-cols">
                <div class="criteria-col">
                    <h4>✅ Sujets recherchés</h4>
                    {{range .Includes}}
                    <span class="tag include">
                        {{.Label}}
                        <form method="POST" action="/criteres/supprimer">
                            <input type="hidden" name="id" value="{{.ID}}">
                            <button type="submit" title="Supprimer">✕</button>
                        </form>
                    </span>
                    {{else}}
                    <p class="hint">Aucun sujet recherché.</p>
                    {{end}}
                </div>

                <div class="criteria-col">
                    <h4>🚫 Sujets bloqués</h4>
                    {{range .Excludes}}
                    <span class="tag exclude">
                        {{.Label}}
                        <form method="POST" action="/criteres/supprimer">
                            <input type="hidden" name="id" value="{{.ID}}">
                            <button type="submit" title="Supprimer">✕</button>
                        </form>
                    </span>
                    {{else}}
                    <p class="hint">Aucun sujet bloqué.</p>
                    {{end}}
                </div>
            </div>

            <form class="add-form" method="POST" action="/criteres/ajouter">
                <input type="text" name="label" placeholder="Ex: CVE, Kubernetes, Rust..." required>
                <select name="kind">
                    <option value="include">✅ Rechercher</option>
                    <option value="exclude">🚫 Bloquer</option>
                </select>
                <button type="submit">Ajouter</button>
            </form>
        </div>
    </div>

    <!-- LISTE ARTICLES -->
    {{if .Articles}}
        {{range .Articles}}
        <div class="article">
            <h2><a href="{{.Link}}" target="_blank">{{.Title}}</a></h2>
            <div class="meta">
                <span class="source">{{.Source}}</span>
                <span>📅 {{formatDate .Date}}</span>
            </div>
            {{if gt (len .Description) 180}}
            <details class="desc">
                <summary><span class="txt">{{.Description}}</span><span class="more"></span></summary>
            </details>
            {{else}}
            <p class="desc-short">{{.Description}}</p>
            {{end}}
        </div>
        {{end}}
    {{else}}
        <p>Aucun article technique pertinent trouvé pour cette période.</p>
    {{end}}
</body>
</html>
`

func handleIndex(db *sql.DB) http.HandlerFunc {
	tmpl := template.Must(template.New("index").
		Funcs(template.FuncMap{"formatDate": formatDate}).
		Parse(htmlTemplate))

	return func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("start")
		end := r.URL.Query().Get("end")
		criteresTab := r.URL.Query().Get("tab") == "criteres"

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

		includes, err := getCriteria(db, "include")
		if err != nil {
			log.Println("Erreur lors de la récupération des critères (include):", err)
		}
		excludes, err := getCriteria(db, "exclude")
		if err != nil {
			log.Println("Erreur lors de la récupération des critères (exclude):", err)
		}

		tmpl.Execute(w, struct {
			Start       string
			End         string
			Articles    []Article
			Errors      []APIStatus
			Includes    []Criterion
			Excludes    []Criterion
			CriteresTab bool
		}{start, end, articles, activeErrors, includes, excludes, criteresTab})
	}
}

// handleAddCriterion enregistre un nouveau critère de recherche.
func handleAddCriterion(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/?tab=criteres", http.StatusSeeOther)
			return
		}
		addCriterion(db, r.FormValue("label"), r.FormValue("kind"))
		http.Redirect(w, r, "/?tab=criteres", http.StatusSeeOther)
	}
}

// handleDeleteCriterion supprime un critère de recherche.
func handleDeleteCriterion(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/?tab=criteres", http.StatusSeeOther)
			return
		}
		if id, err := strconv.Atoi(r.FormValue("id")); err == nil {
			deleteCriterion(db, id)
		}
		http.Redirect(w, r, "/?tab=criteres", http.StatusSeeOther)
	}
}

// ==========================================
// 9. MAIN & CONFIGURATION DES APIs
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

	http.HandleFunc("/", basicAuth(handleIndex(db)))
	http.HandleFunc("/criteres/ajouter", basicAuth(handleAddCriterion(db)))
	http.HandleFunc("/criteres/supprimer", basicAuth(handleDeleteCriterion(db)))

	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	log.Printf("Serveur démarré sur http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
