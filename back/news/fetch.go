package news

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/SolaireFR/gosvelte/gosvelte-core/apis"
	"github.com/SolaireFR/gosvelte/gosvelte-core/database"
	"github.com/SolaireFR/gosvelte/gosvelte-core/entity"
	httperror "github.com/SolaireFR/gosvelte/gosvelte-core/error"
	"github.com/SolaireFR/gosvelte/gosvelte-core/server"
	"github.com/SolaireFR/gosvelte/gosvelte-core/settings"
)

// mistralAPI : seule l'API Conversations donne accès à l'outil web_search (Chat Completions n'a pas accès au web et
// inventerait les news de la veille). Clé : /admin > Clés d'API, ou variable API_MISTRAL_KEYS.
var mistralAPI = apis.Register("mistral", apis.Options{
	BaseURL:     "https://api.mistral.ai/v1",
	Description: "Mistral AI : news de la veille (API Conversations + recherche web), chaque jour à minuit",
	Key:         &apis.Key{Header: "Authorization", Prefix: "Bearer "},
	// La recherche web enchaîne plusieurs requêtes côté Mistral ; PerDay borne le coût des relances manuelles
	Limits:   apis.Limits{PerDay: 20, TimeoutSeconds: 300},
	TestPath: "/models",
})

var (
	mistralModel = settings.String("MISTRAL_MODEL", settings.Options{Group: "news",
		Description: "Modèle Mistral interrogé pour les news", Default: "mistral-small-latest", MaxLength: 100})
	mistralTemperature = settings.String("MISTRAL_TEMPERATURE", settings.Options{Group: "news",
		Description: "Température du modèle, de 0 (factuel) à 1.5", Default: "0.1", Validate: validateTemperature})
)

func validateTemperature(v string) error {
	if t, err := strconv.ParseFloat(v, 64); err != nil || t < 0 || t > 1.5 {
		return errors.New("number between 0 and 1.5 expected")
	}
	return nil
}

// maxListedRuns : récupérations renvoyées au journal ; maxErrorLength : longueur gardée d'une erreur
const (
	maxListedRuns  = 50
	maxErrorLength = 2000
)

// FetchRun : une récupération (journal) : prompt envoyé et résultat
type FetchRun struct {
	database.BaseTable
	ID        int       `json:"id"`
	Day       string    `json:"day"` // jour demandé (AAAA-MM-JJ)
	CreatedAt time.Time `json:"created_at"`
	Model     string    `json:"model"`
	Prompt    string    `json:"prompt"`
	Saved     int       `json:"saved"` // news enregistrées
	Error     string    `json:"error"` // vide = succès
}

func (r FetchRun) Fields() []database.Field {
	return database.Bind(&r,
		database.On(&r.ID),
		database.On(&r.Day).NotNull(),
		database.On(&r.CreatedAt),
		database.On(&r.Model),
		database.On(&r.Prompt).Text(),
		database.On(&r.Saved),
		database.On(&r.Error).Text(),
	)
}

// FetchStarted : récupération lancée en tâche de fond (résultat dans le journal)
type FetchStarted struct {
	Day string `json:"day"`
}

// running : une seule récupération à la fois (minuit et relance manuelle)
var running sync.Mutex

// --- Routes (fonction …Endpoints lue par gosvelte generate) ---
func RegisterFetchEndpoints() {
	server.GET("/fetch-runs", ListFetchRuns, adminOnly)
	server.POST("/fetch-runs", StartFetch, adminOnly)
}

func init() {
	entity.Register("FetchRun", entity.InitFuncForEntity(RegisterFetchEndpoints, database.EnsureTable[FetchRun]))
	entity.RegisterWorker("news-daily", runDaily)
}

type ListFetchRunsParams struct{}

type StartFetchParams struct{}

// ListFetchRuns : dernières récupérations, de la plus récente à la plus ancienne
func ListFetchRuns(_ ListFetchRunsParams) ([]FetchRun, error) {
	runs, err := database.Select[FetchRun]()
	if err != nil {
		return nil, fmt.Errorf("lecture du journal : %w", err)
	}
	slices.SortFunc(runs, func(a, b FetchRun) int { return cmp.Compare(b.ID, a.ID) })
	return runs[:min(len(runs), maxListedRuns)], nil
}

// StartFetch : relance à la main la récupération de la veille, en tâche de fond ; 409 si une récupération est en cours
func StartFetch(_ StartFetchParams) (FetchStarted, error) {
	if !running.TryLock() {
		return FetchStarted{}, httperror.HTTPException{Status: http.StatusConflict, Code: "news.fetchRunning", Message: "A fetch is already running"}
	}
	day := time.Now().AddDate(0, 0, -1)
	go func() {
		defer running.Unlock()
		fetchNews(context.Background(), day)
	}()
	return FetchStarted{Day: day.Format(isoDay)}, nil
}

// runDaily : chaque jour à 0h00, les news de la veille. Au démarrage, la veille est rattrapée si aucune récupération ne
// l'a tentée (serveur éteint à minuit) ; un échec n'est pas retenté à chaque redémarrage : relance depuis le journal.
func runDaily(ctx context.Context) {
	catchUp(ctx)
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		log.Printf("[news] prochaine récupération : %s", next.Format(time.DateTime))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			fetchLocked(ctx, next.AddDate(0, 0, -1))
		}
	}
}

func catchUp(ctx context.Context) {
	yesterday := time.Now().AddDate(0, 0, -1)
	runs, err := database.Select[FetchRun](map[string]any{"day": yesterday.Format(isoDay)})
	if err != nil {
		log.Printf("[news] rattrapage de la veille impossible : %v", err)
		return
	}
	if len(runs) == 0 {
		fetchLocked(ctx, yesterday)
	}
}

func fetchLocked(ctx context.Context, day time.Time) {
	running.Lock()
	defer running.Unlock()
	fetchNews(ctx, day)
}

// fetchNews : récupère et enregistre les news de day, puis note la tentative au journal (running doit être tenu)
func fetchNews(ctx context.Context, day time.Time) {
	run := FetchRun{Day: day.Format(isoDay), CreatedAt: time.Now(), Model: mistralModel.Get()}
	saved, err := collectNews(ctx, &run, day)
	run.Saved = saved
	if err != nil {
		run.Error = truncate(err.Error(), maxErrorLength)
		log.Printf("[news] récupération du %s en échec : %s", run.Day, run.Error)
	} else {
		log.Printf("[news] %d news du %s enregistrées", saved, run.Day)
	}
	if err := database.Insert(&run); err != nil {
		log.Printf("[news] journal non enregistré : %v", err)
	}
}

func collectNews(ctx context.Context, run *FetchRun, day time.Time) (int, error) {
	techs, excludes, err := loadTopics()
	if err != nil {
		return 0, err
	}
	run.Prompt = buildNewsPrompt(day, techs, excludes)
	text, err := askMistral(ctx, run.Model, run.Prompt)
	if err != nil {
		return 0, err
	}
	items, err := parseNews(text, run.Day)
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, fmt.Errorf("aucune news exploitable (titre + lien) dans la réponse : %s", truncate(text, 1000))
	}
	return saveNews(items)
}

// loadTopics : libellés de la page Configuration, par type
func loadTopics() (techs, excludes []string, err error) {
	topics, err := database.Select[Topic]()
	if err != nil {
		return nil, nil, fmt.Errorf("lecture de la configuration : %w", err)
	}
	for _, t := range topics {
		if t.Kind == kindTech {
			techs = append(techs, t.Label)
		} else {
			excludes = append(excludes, t.Label)
		}
	}
	return techs, excludes, nil
}

// conversationRequest : corps de POST /v1/conversations (modèle seul, sans agent pré-créé)
type conversationRequest struct {
	Model          string         `json:"model"`
	Inputs         string         `json:"inputs"`
	Tools          []mistralTool  `json:"tools"`
	CompletionArgs completionArgs `json:"completion_args"`
	Store          bool           `json:"store"` // conversation non conservée par Mistral
}

type mistralTool struct {
	Type string `json:"type"`
}

type completionArgs struct {
	Temperature float64 `json:"temperature"`
}

// askMistral : texte de la réponse du modèle, qui a accès à la recherche web
func askMistral(ctx context.Context, model, prompt string) (string, error) {
	temperature, _ := strconv.ParseFloat(mistralTemperature.Get(), 64) // validée par le réglage
	res, err := mistralAPI.Do(ctx, apis.Request{Method: http.MethodPost, Path: "/conversations", Body: conversationRequest{
		Model: model, Inputs: prompt, Tools: []mistralTool{{Type: "web_search"}}, CompletionArgs: completionArgs{temperature},
	}})
	if err != nil && res != nil {
		return "", fmt.Errorf("appel Mistral : %w - réponse : %s", err, truncate(string(res.Body), 1000))
	}
	if err != nil {
		return "", fmt.Errorf("appel Mistral : %w", err)
	}
	return conversationText(res.Body)
}

// saveNews : news enregistrées, sauf lien déjà connu (même article un autre jour, ou deux fois dans la réponse)
func saveNews(items []News) (int, error) {
	existing, err := database.Select[News]()
	if err != nil {
		return 0, fmt.Errorf("lecture des news : %w", err)
	}
	known := make(map[string]bool, len(existing))
	for _, n := range existing {
		known[n.Link] = true
	}
	saved := 0
	for i := range items {
		if known[items[i].Link] {
			continue
		}
		known[items[i].Link] = true
		items[i].CreatedAt = time.Now()
		if err := database.Insert(&items[i]); err != nil {
			return saved, fmt.Errorf("enregistrement de « %s » : %w", items[i].Title, err)
		}
		saved++
	}
	return saved, nil
}
