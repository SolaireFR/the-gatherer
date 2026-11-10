// Package news : veille quotidienne. Chaque jour à minuit, un seul prompt demande à Mistral (avec recherche web) les news
// de la veille : générales, informatique et technologie, alternatives récentes aux technologies suivies. Les news sont
// enregistrées, consultables jour par jour et marquables en favori.
package news

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/SolaireFR/gosvelte/gosvelte-core/auth"
	"github.com/SolaireFR/gosvelte/gosvelte-core/database"
	"github.com/SolaireFR/gosvelte/gosvelte-core/entity"
	httperror "github.com/SolaireFR/gosvelte/gosvelte-core/error"
	"github.com/SolaireFR/gosvelte/gosvelte-core/server"
)

// isoDay : format des jours (News.Day, FetchRun.Day, paramètre ?day=)
const isoDay = "2006-01-02"

// adminOnly : outil personnel, toutes les routes sont réservées au compte admin (inscription libre à fermer dans /admin)
var adminOnly = auth.Require("admin")

// News : une actualité renvoyée par l'IA
type News struct {
	database.BaseTable
	ID          int    `json:"id"`
	Day         string `json:"day"`     // jour de l'actualité (AAAA-MM-JJ) : la veille de la récupération
	Section     string `json:"section"` // rubrique : clé de newsSections
	Title       string `json:"title"`
	Description string `json:"description"`
	Link        string `json:"link"`
	Source      string `json:"source"`
	// Categories : choisies par l'IA, séparées par des virgules (la base ne stocke pas de liste)
	Categories string    `json:"categories"`
	Favorite   bool      `json:"favorite"`
	CreatedAt  time.Time `json:"created_at"`
}

func (n News) Fields() []database.Field {
	return database.Bind(&n,
		database.On(&n.ID),
		database.On(&n.Day).NotNull(),
		database.On(&n.Section).NotNull(),
		database.On(&n.Title).NotNull(),
		database.On(&n.Description).Text(),
		database.On(&n.Link).NotNull().Unique(),
		database.On(&n.Source),
		database.On(&n.Categories),
		database.On(&n.Favorite),
		database.On(&n.CreatedAt),
	)
}

// --- Routes (fonction …Endpoints lue par gosvelte generate) ---
func RegisterNewsEndpoints() {
	server.GET("/news/days", ListNewsDays, adminOnly)
	server.GET("/news/favorites", ListFavoriteNews, adminOnly)
	server.GET("/news", ListNews, adminOnly)
	server.POST("/news/{id}/favorite", ToggleFavorite, adminOnly)
}

func init() {
	entity.Register("News", entity.InitFuncForEntity(RegisterNewsEndpoints, database.EnsureTable[News]))
}

type ListNewsDaysParams struct{}

type ListNewsParams struct {
	Day server.Query
}

type ListFavoriteNewsParams struct{}

type ToggleFavoriteParams struct {
	ID server.IntPath
}

// ListNewsDays : jours qui ont des news, du plus récent au plus ancien (navigation jour par jour du front)
func ListNewsDays(_ ListNewsDaysParams) ([]string, error) {
	all, err := database.Select[News]()
	if err != nil {
		return nil, fmt.Errorf("lecture des news : %w", err)
	}
	days := []string{}
	for _, n := range all {
		if !slices.Contains(days, n.Day) {
			days = append(days, n.Day)
		}
	}
	slices.Sort(days)
	slices.Reverse(days)
	return days, nil
}

// ListNews : news d'un jour, rangées par rubrique (ordre de newsSections) puis dans l'ordre donné par l'IA
func ListNews(p ListNewsParams) ([]News, error) {
	if _, err := time.Parse(isoDay, p.Day.Value); err != nil {
		return nil, httperror.HTTPException{Status: http.StatusBadRequest, Code: "news.dayInvalid", Message: "Day expected as YYYY-MM-DD"}
	}
	items, err := database.Select[News](map[string]any{"day": p.Day.Value})
	if err != nil {
		return nil, fmt.Errorf("lecture des news du %s : %w", p.Day.Value, err)
	}
	slices.SortStableFunc(items, func(a, b News) int { return cmp.Compare(sectionRank(a.Section), sectionRank(b.Section)) })
	return items, nil
}

// ListFavoriteNews : toutes les news en favori, les plus récentes d'abord
func ListFavoriteNews(_ ListFavoriteNewsParams) ([]News, error) {
	items, err := database.Select[News](map[string]any{"favorite": true})
	if err != nil {
		return nil, fmt.Errorf("lecture des favoris : %w", err)
	}
	slices.SortStableFunc(items, func(a, b News) int { return cmp.Compare(b.Day, a.Day) })
	return items, nil
}

// ToggleFavorite : ajoute la news aux favoris, ou l'en retire ; renvoie la news à jour
func ToggleFavorite(p ToggleFavoriteParams) (News, error) {
	found, err := database.Select[News](map[string]any{"id": p.ID.Value})
	if err != nil {
		return News{}, fmt.Errorf("lecture de la news %d : %w", p.ID.Value, err)
	}
	if len(found) == 0 {
		return News{}, httperror.HTTPException{Status: http.StatusNotFound, Code: "news.notFound", Message: "News not found"}
	}
	item := found[0]
	item.Favorite = !item.Favorite
	if err := database.Update[News](item.ID, item); err != nil {
		return News{}, fmt.Errorf("mise à jour du favori %d : %w", item.ID, err)
	}
	return item, nil
}
