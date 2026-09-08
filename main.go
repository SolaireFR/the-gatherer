package main

import (
	"bytes"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
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
	Categories   []string
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

// LogEntry : un problème rencontré lors d'une récupération, consultable
// depuis l'onglet Journal.
type LogEntry struct {
	ID        int
	CreatedAt string
	Context   string
	Message   string
}

// PromptInfo : le dernier prompt envoyé à l'IA, conservé pour être consultable
// depuis le site (contenu exact + contexte de l'envoi).
type PromptInfo struct {
	CreatedAt   string
	Model       string
	Temperature float64
	NbArticles  int
	Size        int
	Content     string
}

// Criterion : un sujet bloqué, modifiable depuis le site. Kind vaut
// toujours "exclude" (les sujets recherchés n'existent plus).
type Criterion struct {
	ID    int
	Label string
	Kind  string
}

// ==========================================
// 2. CONFIGURATION (VARIABLES D'ENVIRONNEMENT)
// ==========================================

// getEnv renvoie la variable d'environnement, ou la valeur par défaut si elle
// est absente ou vide.
func getEnv(key string, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvFloat comme getEnv, pour un nombre décimal.
func getEnvFloat(key string, defaultValue float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		log.Printf("%s = %q n'est pas un nombre valide, valeur par défaut utilisée (%v)", key, raw, defaultValue)
		return defaultValue
	}
	return value
}

// ==========================================
// 3. GESTION DE L'ÉTAT ET DES ERREURS
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
// 4. BASE DE DONNÉES
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
	_, _ = db.Exec(`ALTER TABLE articles ADD COLUMN categories TEXT DEFAULT ''`)

	// Table des sujets bloqués envoyés à l'IA
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

	// Les sujets recherchés ont été supprimés : l'IA choisit désormais ses
	// propres catégories, seuls les sujets bloqués lui sont transmis.
	_, _ = db.Exec(`DELETE FROM criteria WHERE kind = 'include'`)

	// Dernier prompt envoyé à l'IA (une seule ligne conservée)
	promptQuery := `
	CREATE TABLE IF NOT EXISTS prompts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME,
		model TEXT,
		temperature REAL,
		nb_articles INTEGER,
		size INTEGER,
		content TEXT
	);`

	if _, err = db.Exec(promptQuery); err != nil {
		return nil, err
	}

	// Journal des problèmes (réponses Mistral invalides, etc.)
	logQuery := `
	CREATE TABLE IF NOT EXISTS logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME,
		context TEXT,
		message TEXT
	);`

	if _, err = db.Exec(logQuery); err != nil {
		return nil, err
	}

	return db, nil
}

// logProblem écrit un problème dans la console ET dans le journal en base.
func logProblem(db *sql.DB, context string, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)

	// Les réponses d'API peuvent être très longues : on tronque pour garder
	// un journal lisible.
	if len(message) > 1000 {
		message = message[:1000] + "... (tronqué)"
	}

	log.Printf("[%s] %s", context, message)

	_, err := db.Exec(`INSERT INTO logs (created_at, context, message) VALUES (?, ?, ?)`,
		time.Now().Format("2006-01-02 15:04:05"), context, message)
	if err != nil {
		log.Println("Erreur écriture du journal:", err)
	}
}

// getLogs renvoie les derniers problèmes enregistrés, du plus récent au plus ancien.
func getLogs(db *sql.DB, limit int) ([]LogEntry, error) {
	rows, err := db.Query(`SELECT id, created_at, context, message FROM logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.Context, &e.Message); err == nil {
			entries = append(entries, e)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// savePrompt remplace le prompt conservé par celui qui vient d'être envoyé.
func savePrompt(db *sql.DB, p PromptInfo) {
	if _, err := db.Exec(`DELETE FROM prompts`); err != nil {
		log.Println("Erreur nettoyage du prompt:", err)
		return
	}
	_, err := db.Exec(
		`INSERT INTO prompts (created_at, model, temperature, nb_articles, size, content) VALUES (?, ?, ?, ?, ?, ?)`,
		p.CreatedAt, p.Model, p.Temperature, p.NbArticles, p.Size, p.Content)
	if err != nil {
		log.Println("Erreur enregistrement du prompt:", err)
	}
}

// getLastPrompt renvoie le dernier prompt envoyé, ou nil s'il n'y en a pas.
func getLastPrompt(db *sql.DB) (*PromptInfo, error) {
	var p PromptInfo
	err := db.QueryRow(
		`SELECT created_at, model, temperature, nb_articles, size, content FROM prompts ORDER BY id DESC LIMIT 1`).
		Scan(&p.CreatedAt, &p.Model, &p.Temperature, &p.NbArticles, &p.Size, &p.Content)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// rateLimitHeaders extrait les en-têtes de limitation de débit renvoyés par
// l'API (x-ratelimit-*, ratelimitbysize-*, retry-after) pour les joindre au
// journal : sans eux, un 429 ne dit pas quelle limite a sauté ni quand elle
// se réinitialise.
func rateLimitHeaders(h http.Header) string {
	var parts []string
	for name, values := range h {
		lower := strings.ToLower(name)
		if !strings.Contains(lower, "ratelimit") && lower != "retry-after" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", lower, strings.Join(values, ", ")))
	}
	sort.Strings(parts)
	return strings.Join(parts, " | ")
}

// dateLayouts : formats de date renvoyes par les differentes APIs.
var dateLayouts = []string{
	time.RFC3339,                // 2026-07-29T13:16:30Z
	"2006-01-02 15:04:05 -0700", // 2026-07-29 13:16:30 +0000
	"2006-01-02T15:04:05.000Z",  // 2026-07-29T13:16:30.000Z
	"2006-01-02 15:04:05",       // 2026-07-29 13:16:30
	"2006-01-02T15:04:05",       // 2026-07-29T13:16:30
	"2006-01-02",                // 2026-07-29
}

// dayKey extrait le jour d'une date brute ("2026-07-29"). Les dates sont
// stockees telles que les APIs les renvoient : si le format est inconnu, on
// retombe sur les 10 premiers caracteres, qui suffisent pour les formats ISO.
func dayKey(raw string) string {
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("2006-01-02")
		}
	}
	if len(raw) >= 10 {
		return raw[:10]
	}
	return raw
}

// articleKey identifie un article par son titre et son jour de publication.
// La casse et les espaces sont normalises pour que deux sources qui titrent
// pareil ne passent pas deux fois.
func articleKey(title string, date string) string {
	return dayKey(date) + "|" + strings.ToLower(strings.Join(strings.Fields(title), " "))
}

// getProcessedKeys renvoie les articles deja traites, sous forme de couples
// titre + jour. Ils ne sont ni renvoyes a l'IA ni reenregistres.
func getProcessedKeys(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`SELECT title, date FROM articles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make(map[string]bool)
	for rows.Next() {
		var title, date string
		if err := rows.Scan(&title, &date); err == nil {
			keys[articleKey(title, date)] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

// splitCategories transforme "Go,Docker" en []string{"Go", "Docker"}.
func splitCategories(raw string) []string {
	var cats []string
	for _, c := range strings.Split(raw, ",") {
		if c = strings.TrimSpace(c); c != "" {
			cats = append(cats, c)
		}
	}
	return cats
}

// getAllCategories renvoie, triées, toutes les catégories distinctes présentes
// sur les articles. Les catégories étant inventées par l'IA, c'est la base qui
// fait référence pour alimenter le filtre du site.
func getAllCategories(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT DISTINCT categories FROM articles WHERE COALESCE(categories, '') != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]bool)
	var cats []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		for _, c := range splitCategories(raw) {
			if !seen[c] {
				seen[c] = true
				cats = append(cats, c)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(cats)
	return cats, nil
}

// getCriteria renvoie tous les critères d'un type donné ("exclude").
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

// addCriterion enregistre un sujet bloqué. Les sujets recherchés n'existent
// plus : l'IA choisit elle-même ses catégories.
func addCriterion(db *sql.DB, label string) {
	label = strings.TrimSpace(label)
	if label == "" {
		return
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO criteria (label, kind) VALUES (?, 'exclude')`, label)
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
	query := `INSERT OR IGNORE INTO articles (title, description, date, link, source, categories) VALUES (?, ?, ?, ?, ?, ?)`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Println("Erreur préparation DB:", err)
		return
	}
	defer stmt.Close()

	for _, a := range articles {
		_, err := stmt.Exec(a.Title, a.Description, a.Date, a.Link, a.Source, strings.Join(a.Categories, ","))
		if err != nil {
			log.Println("Erreur insertion article:", err)
		}
	}
}

// Récupère les articles de la BDD (Dernières 24H par défaut ou selon les filtres)
func getArticles(db *sql.DB, start string, end string, cats []string) ([]Article, error) {
	query := `SELECT id, title, description, date, link, source, COALESCE(categories, '') FROM articles WHERE 1=1`
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

	// Filtre par categories : l'article est garde s'il porte AU MOINS une
	// des categories cochees. Les virgules encadrantes evitent qu'une
	// categorie soit trouvee a l'interieur du nom d'une autre.
	if len(cats) > 0 {
		var conditions []string
		for _, c := range cats {
			conditions = append(conditions, `',' || COALESCE(categories, '') || ',' LIKE ?`)
			args = append(args, "%,"+c+",%")
		}
		query += ` AND (` + strings.Join(conditions, " OR ") + `)`
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
		var rawCats string
		if err := rows.Scan(&a.ID, &a.Title, &a.Description, &a.Date, &a.Link, &a.Source, &rawCats); err == nil {
			a.Categories = splitCategories(rawCats)
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
// 5. PREPARATION API MISTRAL
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

// parseMistralSelection extrait du texte renvoyé par l'IA les articles retenus
// et leurs catégories. L'IA encadre parfois sa réponse de texte ou de balises
// Markdown, on isole donc le tableau JSON entre le premier [ et le dernier ].
func parseMistralSelection(content string) (map[int][]string, error) {
	selected := make(map[int][]string)

	first := strings.Index(content, "[")
	last := strings.LastIndex(content, "]")
	if first == -1 || last == -1 || last < first {
		return selected, fmt.Errorf("aucun tableau JSON trouvé dans la réponse : %s", content)
	}

	var items []struct {
		ID         int      `json:"id"`
		Categories []string `json:"categories"`
	}
	if err := json.Unmarshal([]byte(content[first:last+1]), &items); err != nil {
		return selected, fmt.Errorf("JSON invalide (%v) : %s", err, content)
	}

	for _, item := range items {
		var cats []string
		for _, c := range item.Categories {
			if len(cats) >= maxCategoriesPerArticle {
				break
			}
			// La virgule est le séparateur en base : on la retire des libellés.
			c = truncate(strings.ReplaceAll(c, ",", " "), maxCategoryLen)
			if c != "" {
				cats = append(cats, c)
			}
		}
		selected[item.ID] = cats
	}
	return selected, nil
}

// labels extrait les libellés d'une liste de critères.
func labels(criteria []Criterion) []string {
	var out []string
	for _, c := range criteria {
		out = append(out, c.Label)
	}
	return out
}

// maxPromptTitleLen borne la longueur des titres envoyés à l'IA : la
// description n'est plus transmise et les titres sont tronqués pour garder la
// requête légère (l'API renvoie un 429 quand le prompt devient trop gros).
const maxPromptTitleLen = 120

// maxCategoryLen borne la longueur d'une catégorie inventée par l'IA.
const maxCategoryLen = 30

// maxCategoriesPerArticle borne le nombre de catégories gardées par article.
const maxCategoriesPerArticle = 3

// truncate raccourcit une chaîne à max caractères (runes) sans la couper au
// milieu d'un caractère multi-octets.
func truncate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

func buildMistralPrompt(articles []Article, excludes []Criterion) string {
	prompt := "Tu es un développeur logiciel senior chargé de filtrer une veille technique et technologique.\n"
	prompt += "Voici une liste d'articles avec leur ID et leur titre.\n\n"
	prompt += "RÈGLES DE SÉLECTION :\n"
	prompt += "- INCLURE : uniquement les articles concrets et techniques, utiles à un développeur.\n"
	if len(excludes) > 0 {
		prompt += fmt.Sprintf("- EXCLURE : %s.\n", strings.Join(labels(excludes), ", "))
	}
	prompt += "\n"
	prompt += fmt.Sprintf("Pour chaque article retenu, invente 1 a %d categories techniques de ton choix.\n", maxCategoriesPerArticle)
	prompt += fmt.Sprintf("Une categorie est un mot ou une courte expression (%d caracteres maximum), sans virgule.\n", maxCategoryLen)
	prompt += "Reutilise les memes libelles d'un article a l'autre quand le sujet est identique, pour limiter le nombre de categories differentes.\n"
	prompt += "Format de reponse exige : Renvoie UNIQUEMENT un tableau JSON, sans aucun autre texte.\n"
	prompt += "Chaque element contient l'id de l'article et ses categories.\n"
	prompt += "Exemple : [{\"id\": 1, \"categories\": [\"Backend\", \"DevOps\"]}, {\"id\": 5, \"categories\": [\"Securite\"]}]\n\n"
	prompt += "Articles :\n"

	for _, a := range articles {
		prompt += fmt.Sprintf("ID: %d | %s\n", a.ID, truncate(a.Title, maxPromptTitleLen))
	}
	return prompt
}

// ==========================================
// 6. FETCH API GENERIQUE ET APPEL MISTRAL
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

	// FILTRE DES ARTICLES DEJA TRAITES : un meme titre publie le meme jour a
	// deja ete soumis a l'IA, inutile de le repayer ni de le redemander.
	processed, err := getProcessedKeys(db)
	if err != nil {
		log.Println("Erreur lecture des articles déjà traités:", err)
		processed = make(map[string]bool)
	}

	seen := make(map[string]bool)
	var freshArticles []Article
	for _, a := range allFetchedArticles {
		key := articleKey(a.Title, a.Date)
		if processed[key] || seen[key] {
			continue
		}
		seen[key] = true
		freshArticles = append(freshArticles, a)
	}

	skipped := len(allFetchedArticles) - len(freshArticles)
	allFetchedArticles = freshArticles

	log.Printf("Total de %d articles bruts récupérés (%d déjà traités ignorés). Envoi à Mistral...", len(allFetchedArticles), skipped)

	// APPEL MISTRAL ET SAUVEGARDE EN BDD
	mistralKey := os.Getenv("MISTRAL_API_KEY")
	if mistralKey != "" && len(allFetchedArticles) > 0 {
		excludes, err := getCriteria(db, "exclude")
		if err != nil {
			log.Println("Erreur lecture des critères (exclude):", err)
		}

		prompt := buildMistralPrompt(allFetchedArticles, excludes)
		model := getEnv("MISTRAL_MODEL", "mistral-small-latest")
		temperature := getEnvFloat("MISTRAL_TEMPERATURE", 0.1)

		savePrompt(db, PromptInfo{
			CreatedAt:   time.Now().Format("2006-01-02 15:04:05"),
			Model:       model,
			Temperature: temperature,
			NbArticles:  len(allFetchedArticles),
			Size:        len(prompt),
			Content:     prompt,
		})

		reqBody := MistralRequest{
			Model:       model,
			Temperature: temperature,
			Messages: []MistralMessage{
				{Role: "user", Content: prompt},
			},
		}
		jsonBody, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", getEnv("MISTRAL_API_URL", "https://api.mistral.ai/v1/chat/completions"), bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+mistralKey)

		mistralClient := &http.Client{Timeout: 30 * time.Second}
		resp, err := mistralClient.Do(req)

		if err != nil {
			logProblem(db, "Appel Mistral", "Erreur réseau : %v", err)
			return
		}

		bodyBytes, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			logProblem(db, "Appel Mistral", "Lecture de la réponse impossible : %v", readErr)
			return
		}

		if resp.StatusCode != http.StatusOK {
			// Sur un 429, les en-têtes disent quelle limite a été atteinte et
			// quand elle se réinitialise : on les journalise avec l'erreur.
			if limits := rateLimitHeaders(resp.Header); limits != "" {
				logProblem(db, "Appel Mistral", "Code HTTP %d - Limites : %s - Réponse : %s", resp.StatusCode, limits, string(bodyBytes))
			} else {
				logProblem(db, "Appel Mistral", "Code HTTP %d - Réponse : %s", resp.StatusCode, string(bodyBytes))
			}
			return
		}

		var mistralResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(bodyBytes, &mistralResp); err != nil {
			logProblem(db, "Réponse Mistral", "Réponse illisible (%v) : %s", err, string(bodyBytes))
			return
		}

		if len(mistralResp.Choices) == 0 {
			logProblem(db, "Réponse Mistral", "Aucune réponse renvoyée par l'IA : %s", string(bodyBytes))
			return
		}

		content := mistralResp.Choices[0].Message.Content
		if strings.TrimSpace(content) == "" {
			logProblem(db, "Réponse Mistral", "L'IA a renvoyé une réponse vide.")
			return
		}

		selected, err := parseMistralSelection(content)
		if err != nil {
			logProblem(db, "Réponse Mistral", "%v", err)
			return
		}

		if len(selected) == 0 {
			logProblem(db, "Réponse Mistral", "L'IA n'a retenu aucun article sur les %d proposés. Réponse : %s", len(allFetchedArticles), content)
			return
		}

		// Vérification du contenu : les IDs doivent exister.
		knownIDs := make(map[int]bool)
		for _, a := range allFetchedArticles {
			knownIDs[a.ID] = true
		}

		var unknownIDs []string
		for id := range selected {
			if !knownIDs[id] {
				unknownIDs = append(unknownIDs, strconv.Itoa(id))
			}
		}
		if len(unknownIDs) > 0 {
			sort.Strings(unknownIDs)
			logProblem(db, "Réponse Mistral", "%d ID(s) inconnu(s) ignoré(s) : %s", len(unknownIDs), strings.Join(unknownIDs, ", "))
		}

		// On isole uniquement les articles validés par l'IA. Les catégories
		// sont librement choisies par l'IA : on les conserve telles quelles.
		var validArticles []Article
		for i := range allFetchedArticles {
			cats, ok := selected[allFetchedArticles[i].ID]
			if !ok {
				continue
			}

			allFetchedArticles[i].MistralValid = true
			allFetchedArticles[i].Categories = cats
			validArticles = append(validArticles, allFetchedArticles[i])
		}

		// SAUVEGARDE EN BDD : Uniquement les articles validés !
		saveArticles(db, validArticles)
		log.Printf("✅ %d articles validés par Mistral ont été sauvegardés en BDD.", len(validArticles))
	}
}

// ==========================================
// 7. CRON (TICKER INTELLIGENT)
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
// 8. PROTECTION PAR MOT DE PASSE GLOBAL
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
// 8 bis. FILTRE PAR PLAGE DE DATES
// ==========================================

// isoDay : format des bornes echangees avec les champs <input type="date">.
const isoDay = "2006-01-02"

// DateRange : une plage [Start, End] en ISO court, bornes eventuellement vides.
type DateRange struct {
	Start string
	End   string
}

// PresetLink : un prereglage de periode (Jour, Semaine...) propose dans le
// menu du filtre. L'URL porte deja la plage calculee, aucun JS n'est requis.
type PresetLink struct {
	Label  string
	URL    string
	Active bool
}

// DateFilter : tout ce dont le gabarit a besoin pour afficher la pastille de
// filtre par periode : libelle courant, prereglages et fleches de decalage.
type DateFilter struct {
	Start    string
	End      string
	Active   bool
	Label    string
	Presets  []PresetLink
	PrevURL  string
	NextURL  string
	ResetURL string
}

// presetKeys : les prereglages proposes, dans l'ordre d'affichage.
var presetKeys = []struct {
	Key   string
	Label string
}{
	{"day", "Jour"},
	{"week", "Semaine"},
	{"month", "Mois"},
	{"quarter", "Trimestre"},
	{"year", "Année"},
}

// endOfMonth renvoie le dernier jour du mois de d (le "jour 0" du mois suivant).
func endOfMonth(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, d.Location())
}

// computePresetRange calcule la plage d'un prereglage, relative a now.
// La semaine va du lundi au dimanche ; mois, trimestre et annee sont civils.
func computePresetRange(key string, now time.Time) DateRange {
	y, m, d := now.Date()
	loc := now.Location()
	day := func(t time.Time) string { return t.Format(isoDay) }

	switch key {
	case "day":
		return DateRange{day(now), day(now)}
	case "week":
		// Weekday() renvoie 0 = dimanche : on ramene lundi en tete.
		dow := (int(now.Weekday()) + 6) % 7
		monday := time.Date(y, m, d-dow, 0, 0, 0, 0, loc)
		return DateRange{day(monday), day(monday.AddDate(0, 0, 6))}
	case "month":
		first := time.Date(y, m, 1, 0, 0, 0, 0, loc)
		return DateRange{day(first), day(endOfMonth(first))}
	case "quarter":
		first := time.Date(y, (m-1)/3*3+1, 1, 0, 0, 0, 0, loc)
		return DateRange{day(first), day(first.AddDate(0, 3, 0).AddDate(0, 0, -1))}
	case "year":
		return DateRange{day(time.Date(y, 1, 1, 0, 0, 0, 0, loc)), day(time.Date(y, 12, 31, 0, 0, 0, 0, loc))}
	}
	return DateRange{}
}

// detectRangeKind identifie le "grain" d'une plage : correspond-elle exactement
// a un prereglage (jour, semaine, mois...) ou est-elle libre ("custom") ?
// C'est ce grain qui donne le pas des fleches de decalage.
func detectRangeKind(r DateRange) string {
	if r.Start == "" || r.End == "" || r.Start > r.End {
		return "custom"
	}
	s, err := time.Parse(isoDay, r.Start)
	if err != nil {
		return "custom"
	}
	e, err := time.Parse(isoDay, r.End)
	if err != nil {
		return "custom"
	}

	if r.Start == r.End {
		return "day"
	}
	if s.Month() == time.January && s.Day() == 1 && e.Equal(time.Date(s.Year(), 12, 31, 0, 0, 0, 0, s.Location())) {
		return "year"
	}
	if s.Day() == 1 && (int(s.Month())-1)%3 == 0 && e.Equal(s.AddDate(0, 3, 0).AddDate(0, 0, -1)) {
		return "quarter"
	}
	if s.Day() == 1 && e.Equal(endOfMonth(s)) {
		return "month"
	}
	if s.Weekday() == time.Monday && e.Equal(s.AddDate(0, 0, 6)) {
		return "week"
	}
	return "custom"
}

// shiftRange decale la plage d'un cran vers le passe (-1) ou le futur (+1).
// Le pas suit le grain detecte ; une plage libre se decale de sa propre duree.
// Une plage non bornee des deux cotes n'est pas decalable.
func shiftRange(r DateRange, direction int) (DateRange, bool) {
	if r.Start == "" || r.End == "" {
		return r, false
	}
	s, err := time.Parse(isoDay, r.Start)
	if err != nil {
		return r, false
	}
	e, err := time.Parse(isoDay, r.End)
	if err != nil {
		return r, false
	}
	day := func(t time.Time) string { return t.Format(isoDay) }

	switch detectRangeKind(r) {
	case "day":
		shifted := s.AddDate(0, 0, direction)
		return DateRange{day(shifted), day(shifted)}, true
	case "week":
		return DateRange{day(s.AddDate(0, 0, 7*direction)), day(e.AddDate(0, 0, 7*direction))}, true
	case "month":
		first := s.AddDate(0, direction, 0)
		return DateRange{day(first), day(endOfMonth(first))}, true
	case "quarter":
		first := s.AddDate(0, 3*direction, 0)
		return DateRange{day(first), day(first.AddDate(0, 3, 0).AddDate(0, 0, -1))}, true
	case "year":
		first := s.AddDate(direction, 0, 0)
		return DateRange{day(first), day(time.Date(first.Year(), 12, 31, 0, 0, 0, 0, first.Location()))}, true
	default:
		// Plage libre : on la decale de sa duree, bornes incluses.
		span := int(e.Sub(s).Hours()/24) + 1
		step := span * direction
		return DateRange{day(s.AddDate(0, 0, step)), day(e.AddDate(0, 0, step))}, true
	}
}

// filterURL construit le lien du filtre en conservant les categories cochees.
func filterURL(r DateRange, cats []string) string {
	params := url.Values{}
	if r.Start != "" {
		params.Set("start", r.Start)
	}
	if r.End != "" {
		params.Set("end", r.End)
	}
	for _, c := range cats {
		params.Add("cat", c)
	}
	if len(params) == 0 {
		return "/"
	}
	return "/?" + params.Encode()
}

// rangeLabel resume la plage courante pour la pastille du filtre.
func rangeLabel(r DateRange) string {
	day := func(v string) string {
		if t, err := time.Parse(isoDay, v); err == nil {
			return t.Format("02/01/2006")
		}
		return v
	}
	switch {
	case r.Start != "" && r.End != "":
		return day(r.Start) + " → " + day(r.End)
	case r.Start != "":
		return "dès le " + day(r.Start)
	case r.End != "":
		return "jusqu'au " + day(r.End)
	}
	return "Dernières 24H"
}

// buildDateFilter assemble l'etat du filtre par periode pour le gabarit.
func buildDateFilter(r DateRange, cats []string) DateFilter {
	f := DateFilter{
		Start:    r.Start,
		End:      r.End,
		Active:   r.Start != "" || r.End != "",
		Label:    rangeLabel(r),
		ResetURL: filterURL(DateRange{}, cats),
	}

	now := time.Now()
	for _, p := range presetKeys {
		preset := computePresetRange(p.Key, now)
		f.Presets = append(f.Presets, PresetLink{
			Label:  p.Label,
			URL:    filterURL(preset, cats),
			Active: preset == r,
		})
	}

	if prev, ok := shiftRange(r, -1); ok {
		f.PrevURL = filterURL(prev, cats)
	}
	if next, ok := shiftRange(r, 1); ok {
		f.NextURL = filterURL(next, cats)
	}
	return f
}

// ==========================================
// 9. SERVEUR WEB ET HTML
// ==========================================

// formatDate convertit les dates renvoyees par les APIs (formats varies)
// en un affichage lisible : "29/07/2026 13H".
// Si le format est inconnu, la valeur brute est renvoyee telle quelle.
func formatDate(raw string) string {
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("02/01/2006 15H")
		}
	}
	return raw
}

// formatDateTime affiche une date du journal avec l'heure precise : "31/08/2026 13:25".
// Le driver SQLite peut renvoyer la date au format RFC3339, on gere les deux.
func formatDateTime(raw string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("02/01/2006 15:04")
		}
	}
	return raw
}

// htmlTemplate : le gabarit de la page, edite dans page.html et embarque
// dans le binaire a la compilation (aucun fichier a deployer a cote).
//
//go:embed page.html
var htmlTemplate string

// faviconSVG : le logo du site, embarque lui aussi. Il sert a la fois d'icone
// d'onglet et de logo dans l'en-tete de la page.
//
//go:embed favicon.svg
var faviconSVG []byte

// handleFavicon sert le logo. Volontairement hors authentification : le
// navigateur le reclame avant meme que l'utilisateur ne se soit identifie.
func handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(faviconSVG)
}

func handleIndex(db *sql.DB) http.HandlerFunc {
	tmpl := template.Must(template.New("index").
		Funcs(template.FuncMap{"formatDate": formatDate, "formatDateTime": formatDateTime}).
		Parse(htmlTemplate))

	return func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("start")
		end := r.URL.Query().Get("end")
		criteresTab := r.URL.Query().Get("tab") == "criteres"
		logsTab := r.URL.Query().Get("tab") == "logs"
		selectedCats := r.URL.Query()["cat"]

		// Récupération depuis la BDD directement
		articles, err := getArticles(db, start, end, selectedCats)
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

		excludes, err := getCriteria(db, "exclude")
		if err != nil {
			log.Println("Erreur lors de la récupération des critères (exclude):", err)
		}

		// Les catégories sont inventées par l'IA : les cases à cocher du
		// filtre reprennent celles réellement présentes en base.
		allCats, err := getAllCategories(db)
		if err != nil {
			log.Println("Erreur lors de la récupération des catégories:", err)
		}

		type CategoryChoice struct {
			Name    string
			Checked bool
		}
		var catChoices []CategoryChoice
		for _, c := range allCats {
			checked := false
			for _, sel := range selectedCats {
				if sel == c {
					checked = true
					break
				}
			}
			catChoices = append(catChoices, CategoryChoice{c, checked})
		}

		logs, err := getLogs(db, 50)
		if err != nil {
			log.Println("Erreur lors de la récupération du journal:", err)
		}

		lastPrompt, err := getLastPrompt(db)
		if err != nil {
			log.Println("Erreur lors de la récupération du dernier prompt:", err)
		}

		tmpl.Execute(w, struct {
			Start       string
			End         string
			Articles    []Article
			Errors      []APIStatus
			Excludes    []Criterion
			CriteresTab bool
			LogsTab     bool
			Categories  []CategoryChoice
			NbSelected  int
			Logs        []LogEntry
			LastPrompt  *PromptInfo
			DateFilter  DateFilter
		}{start, end, articles, activeErrors, excludes, criteresTab, logsTab, catChoices, len(selectedCats), logs, lastPrompt,
			buildDateFilter(DateRange{start, end}, selectedCats)})
	}
}

// handleAddCriterion enregistre un nouveau sujet bloqué.
func handleAddCriterion(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/?tab=criteres", http.StatusSeeOther)
			return
		}
		addCriterion(db, r.FormValue("label"))
		http.Redirect(w, r, "/?tab=criteres", http.StatusSeeOther)
	}
}

// handleDeleteCriterion supprime un sujet bloqué.
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
// 10. MAIN & CONFIGURATION DES APIs
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
			URL:  getEnv("DEVTO_API_URL", "https://dev.to/api/articles?tag=programming&per_page=15"),
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
			URL:  getEnv("CURRENTS_API_URL", "https://api.currentsapi.services/v1/latest-news?language=en&category=technology"),
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
			URL:  getEnv("GNEWS_API_URL", "https://gnews.io/api/v4/top-headlines?category=technology&lang=en") + "&apikey=" + gnewsKey,
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
			URL:  getEnv("NEWSAPI_API_URL", "https://newsapi.org/v2/top-headlines?category=technology") + "&apiKey=" + newsapiKey,
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

	http.HandleFunc("/favicon.svg", handleFavicon)
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
